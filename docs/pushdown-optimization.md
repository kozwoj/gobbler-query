# Source Pushdown Optimization

## Motivation

The current execution pipeline for a query like `requests (last 24h) | where statusCode >= 400 | project userId, durationMs` creates three separate operators:

```
SourceOp → FilterOp → ProjectOp
```

`SourceOp` reads CSV files via `csv.Reader` and emits full-width batches. `FilterOp` evaluates the predicate, collects a list of passing row indices, and calls `compact()` — a full copy of the passing rows into a new batch. `ProjectOp` then materializes the output columns into a third batch.

Two sources of waste:
- **Column waste**: `csv.Reader` parses and allocates every field on every row, even columns that are never used downstream.
- **Row waste**: `compact()` copies passing rows into a new batch; if most rows are rejected, the initial allocation was largely wasted.

## Approach

### 1. Bulk file read

Replace `os.Open` + `csv.Reader` with `os.ReadFile` (local) or `DownloadBuffer` (blob). The entire file lands in a `[]byte` in one syscall. All subsequent processing is in-memory with no further I/O.

### 2. Custom byte-slice field scanner

Replace `csv.Reader` with a custom scanner that operates on the `[]byte` buffer. For each row it scans fields by advancing past commas — without allocating per-field strings. Columns not in `wantCols` are skipped by advancing past the delimiter, not by parsing. Only the `dynamic` column type (which contains quoted JSON) requires full quoted-field handling.

### 3. Predicate evaluation and column pruning inside the reader

The reader accepts an optional `pred expr.RowPredicate` and `wantCols []int`. `GetNextBatch` builds a full-width candidate batch (all schema columns) exactly as it does today, then applies filter and projection in one combined compact step before returning:

```
build full-width candidate batch (all columns, up to batchSize rows)
for row 0..Length-1:
    ok = pred(candidateBatch, row)   // identical call to FilterOp — no scratch batch needed
    if ok: passing = append(passing, row)
compact candidate batch to wantCols only   // combined filter + project in one pass
return narrow batch
```

The predicate closures reference column indices into the full schema, so the candidate batch must cover all columns — those indices stay valid with no remapping. Narrowing to `wantCols` only happens in the compact step.

`wantCols []int` is a sorted list of column indices (into the source schema) that must appear in the output batch. The emitted batch schema contains only those columns. When `wantCols` is nil the compact step is skipped and the full-width batch is returned unchanged (no regression for queries without a project stage).

This eliminates `FilterOp`, the separate `compact()` copy, and reduces the memory traffic of the compact step to `len(wantCols)` columns instead of all N.

For the `Where → Project → Source` pattern the predicate is compiled against the projected schema rather than the source schema — its column indices reference the projected column positions. `ReaderOptions` gains a `PredCols []int` field to handle this. When set, `GetNextBatch` evaluates `Pred` against `projectBatch(candidate, PredCols)` — a thin wrapper that shares column pointers with no data copy — instead of the full candidate. For a pure-column-select project `PredCols` equals `WantCols`.

## Planned logical pattern matching

The planner detects the following patterns in the logical tree and folds them into a single source reader call instead of building separate operators:

| Logical pattern | Reader receives | Operators eliminated |
|---|---|---|
| `Source` | `wantCols` (if known from context) | — |
| `Where → Source` | `pred`; `wantCols` = nil (all columns kept) | `FilterOp` |
| `Project → Source` | `wantCols` from project items; `pred` = nil | `ProjectOp` if pure column-select |
| `Project → Where → Source` | `pred` from Where; `wantCols` from Project items | `FilterOp`; `ProjectOp` if pure column-select |
| `Where → Project → Source` | `pred` (compiled against projected schema); `wantCols` from Project; `predCols = wantCols` | `FilterOp`; `ProjectOp` if pure column-select |

"Pure column-select" means every project item is a bare `FieldRefExpr` with no alias or computed expression. In that case the output batch schema matches the project output and `ProjectOp` can be dropped entirely. Otherwise `ProjectOp` is kept but operates on the already-narrow batch.

`Source → Project → Filter` (where predicate references computed columns) is not handled — the predicate cannot be pushed below the project without expression rewriting.

## Applies to joins

The same optimization applies to the **left-hand (probe) sub-pipeline** of a join as well as to the main pipeline. The right-hand (build) side of a `HashJoinOp` is a sub-query that is fully materialized, so the same pattern matching applies there too.

## Implementation steps

Each step is independently testable before the next begins.

**Step 1 — Add `ReaderOptions` and thread it through the API** (`source/`)  
Add `ReaderOptions{Pred expr.RowPredicate, WantCols []int}` and pass it (as nil) through `NewTableReader`, `NewFileTableReader`, and `NewBlobTableReader`. No behavior change.  
*Test*: all existing source tests pass unchanged.

**Step 2 — Column pruning in `FileTableReader.GetNextBatch`** (`source/`)  
When `WantCols != nil`, compact the candidate batch to those columns before returning. `Pred` is still ignored.  
*Test*: new `FileTableReader` unit tests that pass `WantCols` and assert the output batch has the correct narrow schema and values.

**Step 3 — Predicate filter in `FileTableReader.GetNextBatch`** (`source/`)  
When `Pred != nil`, evaluate it row-by-row on the candidate batch, collect `passing[]`, then compact to `WantCols` (or full width if nil). Loop until a non-empty batch is produced or EOF, matching `FilterOp` behavior.  
*Test*: new unit tests combining `Pred` and `WantCols`; assert filtered narrow output. Test the all-rows-rejected-loop-until-EOF path.

**Step 4 — Bulk read + byte-slice scanner in `FileTableReader`** (`source/`)  
Replace `os.Open` + `csv.Reader` with `os.ReadFile` + custom scanner. Add `AppendBytes(cell []byte)` to each `columnBuilder`. The `dynamic` column type still uses full quoted-field handling; all others use a fast comma-scan.  
*Test*: all existing and new `FileTableReader` tests pass unchanged (behavior identical, path faster).

**Step 5 — Bulk download + scanner in `BlobTableReader`** (`source/`)  
Replace the streaming `csv.Reader` path with `DownloadBuffer` + the same scanner from Step 4. Thread `ReaderOptions` through.  
*Test*: existing blob integration tests pass; new unit test with `Pred` and `WantCols` against an in-memory fake blob.

**Step 6 — Planner: pushdown analysis** (`planner/`)  
Add `analyzePushdown(n logical.Node) pushdownPlan` — a pure pattern-matching function with no side effects. It inspects the top of the logical subtree rooted at `n` and classifies it into one of:
- `pushdownNone` — no recognised pattern
- `pushdownPred` — `LogicalWhere → LogicalSource`
- `pushdownCols` — pure-column-select `LogicalProject → LogicalSource`
- `pushdownPredThenCols` — pure-column-select `LogicalProject → LogicalWhere → LogicalSource`
- `pushdownColsThenPred` — `LogicalWhere` → pure-column-select `LogicalProject → LogicalSource`

The returned `pushdownPlan` struct carries the matched AST nodes (not compiled values). Steps 7–10 call `analyzePushdown` and dispatch on the result.  
*Test*: unit tests directly on `analyzePushdown` covering all five cases, including aliased/computed project items (must return `pushdownNone` or `pushdownPred` only) and subtrees with no `LogicalSource`.

**Step 7 — Planner: `Where → Source` pattern** (`planner/`)  
Using the `pushdownPred` result from `analyzePushdown`: compile the predicate into `ReaderOptions.Pred`, build the source reader directly, return a `SourceOp` — no `FilterOp`.  
*Test*: `api.Execute` e2e test on testdata with a `where` clause; assert correct filtered row count and values.

**Step 8 — Planner: `Project → Source` pattern** (`planner/`)  
Using the `pushdownCols` result: compute `WantCols` from the project items and pass to the reader. For pure column-select projects eliminate `ProjectOp`; for computed/renamed columns keep `ProjectOp` but feed it the narrow batch.  
*Test*: e2e test with `project` on a source; assert narrow output schema and correct values.

**Step 9 — Planner: `Project → Where → Source` pattern** (`planner/`)  
Using the `pushdownPredThenCols` result: fold both `pred` and `wantCols` into the reader; suppress `FilterOp` and `ProjectOp` (or keep `ProjectOp` for computed columns).  
*Test*: e2e test for `source | where ... | project ...`; assert filtered narrow output. Cross-check result against the unoptimized path.

**Step 10 — Planner + source: `Where → Project → Source` pattern** (`planner/`, `source/`)  
Add `PredCols []int` to `ReaderOptions`. In `GetNextBatch`, when `PredCols != nil`, evaluate `Pred` against `projectBatch(candidate, PredCols)` instead of the full candidate batch (shared column pointers, no data copy).  
Using the `pushdownColsThenPred` result: compute `wantCols` from the project items, set `predCols = wantCols`, compile the pred (which references projected-schema column indices), build the source reader with all three options set.  
*Test*: e2e test for `source | project ... | where ...` (pure column-select project); assert filtered narrow output matches the unoptimized path.

---

## Scope of changes

| Package | Change |
|---|---|
| `query/source/` | `csvScanner` byte-slice field scanner; `fileReader` switches to `os.ReadFile`; `blobReader` switches to `DownloadBuffer`; `ReaderOptions{Pred, WantCols, PredCols}` threaded through `NewTableReader`; `GetNextBatch` does combined filter+compact internally when any option is set; when `PredCols != nil`, `Pred` is evaluated against a projected view of the candidate batch |
| `query/source/builders.go` | `AppendBytes(cell []byte)` variant on each `columnBuilder` to avoid `string(cell)` conversion in the hot path |
| `query/planner/planner.go` | `analyzePushdown` classifies the source-adjacent subtree into one of five patterns; `build*` methods dispatch on the result to fold `pred`, `wantCols`, and `predCols` into the reader and suppress the corresponding operators |
