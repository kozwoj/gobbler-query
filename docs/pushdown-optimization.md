# Source Pushdown Optimization

Consider the following query: `requests (last 24h) | where statusCode >= 400 | project userId, durationMs`. Logically it is a sequence of three operators: 

```
SourceOp → FilterOp → ProjectOp
```

`SourceOp` reads CSV files line at a time and emits full-width batches. `FilterOp` evaluates the predicate, collects a list of passing row indices, and calls `compact()` to build a new batch. `ProjectOp` then materializes the output columns into a third batch.

Pushdown optimization pushes both filtering and projection into the source, where it can be done more efficiently and without creating intermediary batches. 

## Approach

### 1. Bulk file read

Replace `os.Open` + `csv.Reader` with `os.ReadFile` (local) or `DownloadBuffer` (blob). The entire file lands in a `[]byte` in one syscall. All subsequent processing is in-memory with no further I/O.

### 2. Custom byte-slice field scanner

Replace `csv.Reader` with a custom scanner that operates on the `[]byte` buffer. For each row it scans fields by advancing past commas — without allocating per-field strings. Columns not in `wantCols` are skipped by advancing past the delimiter, not by parsing. Only the `dynamic` column type (which contains quoted JSON) requires full quoted-field handling.

### 3. Predicate evaluation and column pruning inside the reader

The source reader accepts two optional arguments: 
- `pred expr.RowPredicate`, which is the predicate of the `where` stage, and 
- `wantCols []int`, which is the list of columns of the `project` stage. 

`GetNextBatch` builds a full-width candidate batch (all schema columns) exactly as it does today, then applies filter and projection in one combined compact step before returning:

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


