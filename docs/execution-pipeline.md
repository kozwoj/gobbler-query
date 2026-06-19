# Execution Pipeline Overview

*Batch model · Operators · Logical plan → Physical plan · Streaming vs blocking*

---

## 1. Batch Model

The unit of data flowing through the pipeline is a `Batch`. Every stage operator consumes and produces batches.

```go
type Batch struct {
    Length  int          // number of rows in the batch
    Schema  []ColumnMeta // ColumnMeta describes the corresponding column's name and origin
    Columns []ColumnVector
}
```

Every operator allocates and returns a new `*Batch`. No batch pointer is ever mutated after it is returned, and no backing arrays are aliased between the returned batch and the operator's internal state. This eliminates lifetime contracts at the cost of allocation, which is acceptable for small to mid-size workloads (short-lived batches, low live set, negligible GC pressure).

The exception is `SortOp`, `HashAggregateOp`, and `HashJoinOp`: all three fully materialize their input (or one side of it) into internal storage before emitting any output, so they carry the same class of heap pressure regardless of the allocation strategy. `HashJoinOp` materializes the entire right subquery into a hash table; the left side then streams through it batch by batch.

`ColumnMeta` describes a single column's identity:

```go
// ColumnMeta carries the name and origin of a column.
// Origin is the source type name (e.g. "requests") for columns that come
// directly from a CSV source, and empty for computed or aggregate columns.
type ColumnMeta struct {
    Name   string
    Origin string
}
```

Ambiguity resolution after a join: 
- a `FieldRef{Table:"", Name:"x"}` matches any column where `Name == "x"`
- if two or more columns match, the expression, validator returns an error and the query must qualify the reference (e.g. `requests.x`).
- a `FieldRef{Table:"requests", Name:"x"}` matches only columns where both `Name == "x"` and `Origin == "requests"`.

`ColumnVector` is an interface:

```go
// ColumnVector is implemented by each typed vector.
// Expression evaluators type-assert to the concrete type; Len and IsNull
// allow generic null-checking without a type assertion.
type ColumnVector interface {
    Len() int
    IsNull(i int) bool
    columnVector() // unexported marker; prevents external implementations
}
```

Concrete types (one per `ColumnType`):

```go
type Int32Vector    struct { Values []int32;         Nulls []uint64 }
type Int64Vector    struct { Values []int64;         Nulls []uint64 } // used for count() output and dcount()
type Float64Vector  struct { Values []float64;       Nulls []uint64 }
type StringVector   struct { Values []string;        Nulls []uint64 }
type BoolVector     struct { Values []bool;          Nulls []uint64 }
type DatetimeVector struct { Values []time.Time;     Nulls []uint64 }
type TimespanVector struct { Values []time.Duration; Nulls []uint64 }
type DynamicVector  struct { Values []string;        Nulls []uint64 } // plain JSON string
```

`Nulls` is a packed bitset: bit `i` set means row/element `i` in the vector is null. 
All concrete types implement `Len()`, `IsNull(i int) bool`, and `columnVector()`.

---

## 2. Operator Interface

Every stage operator implements a two-method pull-based interface:

```go
type Operator interface {
    Next() (*batch.Batch, error)
    Close() error
}
```

`Next()` returns `nil, io.EOF` when the operator exhausted all input. Operators chain by calling `Next()` on their input operator. `Close()` releases any held resources (file handles, hash tables).

---

## 3. Operator Catalogue

> **Note:** struct definitions below are illustrative. All fields are exported in the actual code (e.g. `Input`, not `input`). See the source files in `query/physical/` for exact signatures.

### SourceOp
Stage implemented: `source` stage (`LogicalSource`).

Wraps the batch reader. Root of every plan.

```go
type SourceOp struct {
    reader source.TableReader
}

func (op *SourceOp) Next() (*batch.Batch, error) {
    return op.reader.GetNextBatch()
}

func (op *SourceOp) Close() error {
    return op.reader.Close()
}
```

### FilterOp
Stage implemented: `where` stage (`LogicalWhere`).

Evaluates a compiled `BoolExpr` row-by-row and **compacts** the passing rows into a new `*Batch` with fresh column vectors. The output should be always a smaller batch with `Length` equal to the number of passing rows.

```go
type FilterOp struct {
    Input Operator
    Pred  expr.RowPredicate
}
```

### ProjectOp
Stage implemented: `project` stage (`LogicalProject`).

Evaluates scalar expressions and produces new column vectors.

```go
type ProjectOp struct {
    input Operator
    items []expr.ScalarExprEvaluator
    names []string
}
```

GQL is a subset of KQL and in KQL projection is more than just selection of columns for the next stage. The ProjectOp is designs to:  
- Selects which columns survive
- Computes new columns
- Renames columns
- Reorders columns
- Drops everything not listed

The new columns can be computed using `alias = expression` clause of the language. 
```
ProjectStage ::= "project" ProjectItem ( "," ProjectItem )*
ProjectItem  ::= Identifier "=" ScalarExpr     (* alias = expression *)
               | FieldRef                      (* bare column reference *)
```

### HashAggregateOp
Stage implemented: `summarize` stage (`LogicalSummarize`).

Consumes all input batches, updates a `map[GroupKey]*AggState`, then emits one final batch.

```go
type HashAggregateOp struct {
    input   Operator
    aggs    []expr.AggFunc
    groupBy []expr.FieldRef
    hash    map[GroupKey]*AggState
    emitted bool
}
```

### HashJoinOp
Stage implemented: `join` stage (`LogicalJoin`).

The operator loads the right subquery into a hash table, and then streams the left source through it. 

```go
type HashJoinOp struct {
	left  Operator
	right Operator
	// leftKeyIdxs are column indices into the left schema.
	// rightKeyIdxs are the corresponding column indices into the right (build) schema.
	leftKeyIdxs  []int
	rightKeyIdxs []int
	// outSchema is the output schema: left columns || right columns.
	outSchema []batch.ColumnMeta
	batchSize int // rows per output batch; 0 → defaultBatchSize

	// ── right-side build phase state properties ────────────
	...

	// ── join phase state properties ─────────────────────────
    ...
}
```

### SortOp
Stage implemented: `sort` stage (`LogicalSort`).

Materializes all rows from input, sorts them, and then emits sorted batches.

```go
type SortOp struct {
    input     Operator
    keys      []CompiledSortKey
    batchSize int
    // rows and offset populated on first Next()
}
```

### LimitOp
Stage implemented: `take` stage (`LogicalTake`).

Passes through batches until `remaining` rows have been emitted.

```go
type LimitOp struct {
    input     Operator
    remaining int
}
```

### CountOp
Stage implemented: `count` stage (`LogicalCount`).

Sugar for `summarize count()`. Accumulates a counter across all input batches, emits one row.

```go
type CountOp struct {
    input Operator
    done  bool
    count int64
}
```

---

## 4. Logical Plan

The logical plan is a direct structural mirror of the AST — one node per grammar stage, each wrapping its input. No optimization passes are applied in this implementation of Gobbler.

> **Why keep the logical/physical split**  
> The separation was kept in case we decide to convert groups of CSV files/blobs to columnar segments. That storage will allow optimization passes (predicate pushdown, partition pruning, join reordering) that slot cleanly between logical and physical. Keeping the split now means we can add those optimization without restructuring the parser, the AST, or any logical operator.

### Logical nodes

- `LogicalSource` — type name + resolved time window 
- `LogicalWhere` — `BoolExpr`
- `LogicalProject` — `[]ProjectItem`
- `LogicalSummarize` — `[]AggItem`, `[]FieldRef` (group-by)
- `LogicalJoin` — left input, right input, `[]JoinKey`
- `LogicalSort` — `[]SortItem`
- `LogicalTake` — `int64`
- `LogicalCount`

### Logical → Physical mapping

| Logical node | Physical operator | Notes |
|---|---|---|
| `LogicalSource` | `SourceOp` | Wraps `source.TableReader` (file or blob) |
| `LogicalWhere` | `FilterOp` | Compiled `BoolExpr` |
| `LogicalProject` | `ProjectOp` | Compiled scalar expressions |
| `LogicalSummarize` | `HashAggregateOp` | Hash table + agg state |
| `LogicalJoin` | `HashJoinOp` | Right side = build; left side = probe |
| `LogicalSort` | `SortOp` | Full in-memory materialize |
| `LogicalTake` | `LimitOp` | Row-count limit |
| `LogicalCount` | `CountOp` | Sugar for `summarize count()` |

### Physical plan builder

The builder lives in `package planner` and imports `logical`, `physical`, `source`, `catalog`, and `expr`.

```go
// BuildPhysical translates a validated logical plan into a physical operator
// tree ready to execute. Arguments must contain a source.Schema for every table
// referenced in the plan (they are used here for column-type resolution when
// building project and summarize operators). batchSize 0 uses the default (512).
func BuildPhysical(
    node      logical.LogicalNode,
    cat       catalog.Catalog,
    schemas   map[string]*source.Schema,
    batchSize int,
) (physical.Operator, error)
```

The builder dispatches on the node type and recurses:

| Logical node | Physical construction |
|---|---|
| `LogicalSource` | `source.NewTableReader` → `SourceOp` |
| `LogicalWhere` | `expr.Compile(pred)` → `FilterOp` |
| `LogicalProject` | per-item type inference + `expr.Compile` → `ProjectOp` |
| `LogicalSummarize` | `NewHashAggregateOp(input, aggs, groupBy)` |
| `LogicalJoin` | `NewHashJoinOp(left, right, leftKeyIdxs, rightKeyIdxs, outSchema, outKinds)` |
| `LogicalSort` | `NewSortOp(input, keys)` |
| `LogicalTake` | `LimitOp{Input, Remaining}` |
| `LogicalCount` | `CountOp{Input}` |

## 5. Streaming vs Blocking

### Classification

**Streaming operator** — processes one batch obtained from its input node (upstream neighbor) and return immediately; O(batch) memory:

| Operator | Why it streams |
|---|---|
| `FilterOp` | Predicate applied per-batch; result returned immediately |
| `ProjectOp` | Column transforms applied per-batch |
| `LimitOp` | Stops pulling once the row count is reached |

**Blocking operator** — must consume all batches from upstream node/stage before emitting anything:

| Operator | Memory footprint | Why it blocks |
|---|---|---|
| `HashAggregateOp` | O(distinct groups × agg state) | Final group totals unknown until last row |
| `CountOp` | O(1) | Final count unknown until EOF |
| `SortOp` | O(total rows) | Minimum row unknown until all rows are seen |
| `HashJoinOp` (build side) | O(right subquery rows) | Hash table must be fully loaded before probing |

`HashJoinOp` is **semi-blocking**: the right (build) side runs to completion once, then the left (probe) side streams through it. After the build phase the join is returned to downstream nod/stage.

### Pipeline breaks

A blocking operator creates a **pipeline break** — everything upstream runs to completion before anything downstream receives the resulting batch. Below is an example:

```
Logs | where status >= 400 | summarize count() by region | sort by count_ desc | take 5
```

```
streaming:   SourceOp → FilterOp    (batches flow continuously)
                  ↓ 
blocking:    HashAggregateOp        (consumes all upstream batches and builds one result batch)
                  ↓ 
blocking:    SortOp                 (consumes all upstream batches before sorting and returns one sorted batch)
                  ↓
streaming:   LimitOp                (passes first 5 rows as result from this query)
```

The second break is trivially cheap here — `summarize` has already collapsed the input to at most one row per group, so `SortOp` sees a tiny batch regardless of CSV volume.

### Planner implications

**Stage ordering** — `sort` before `summarize` would materialize potentially millions of raw rows; `summarize` discards the order anyway. `take` before `summarize` changes query semantics (limits input rows, not output groups).

**`take` short-circuit** — `LimitOp` stops calling `Next()` once the limit is reached. After a purely streaming chain this cancels I/O mid-file. After a blocking operator there is no I/O benefit — the operator already consumed everything.

**Join build-side** — build side is the right subquery side. `HashJoinOp` always loads the right (subquery) side. Then it streams the left side and joins them one at a time with the right side result, 

>**Note:** Put the smaller relation on the right. In the future the join stage may optimize the build side bases on statistics. 

**Time window** — the source time window (`last 24h`, `datetime(T1) .. datetime(T2)`, `*`) is passed to `SourceOp` at construction time, and not translated into a `FilterOp` like in KQL. The source layer handles file-level pruning and boundary-row filtering internally; the pipeline has no knowledge of the window. Explicit `timestamp` predicates in `where` stages are independent ordinary expressions evaluated by `FilterOp` and may or may not be consistent with the source time window.

---
## 6. End-to-End Examples

### Example 1 — filter, project, take

```
Logs(last 1h)
| where statusCode >= 400
| project timestamp, region
| take 3
```

**Parse → AST:**
```
Query
  Source: "Logs", TimeWindow: last 1h
  Stages: Where(statusCode >= 400) · Project(timestamp, region) · Take(3)
```

**Logical plan:**
```
LogicalTake(3)
  LogicalProject(timestamp, region)
    LogicalWhere(statusCode >= 400)
      LogicalSource("Logs", last 1h)
```

**Physical plan:**
```
LimitOp(3)
  ProjectOp(timestamp, region)
    FilterOp(statusCode >= 400)
      SourceOp  ← opens and reads only files within last 1h; skips boundary rows outside that time window
```

**Execution loop:**
```go
for {
    b, err := rootOp.Next()
    if err == io.EOF { break }
    printBatch(b)
}
```

`LimitOp` stops pulling after 3 rows are emitted, which may cancel the CSV read mid-file.

---
### Example 2 — join

```
Requests(last 24h)
| join (Users(*) | project userId, tier) on userId
| summarize count() by tier
```

**Logical plan:**
```
LogicalSummarize(count() by tier)
  LogicalJoin(on userId)
    Left:  LogicalSource("Requests", last 24h)
    Right: LogicalProject(LogicalSource("Users", *), userId, tier)
```

**Physical plan:**
```
HashAggregateOp(count() by tier)
  HashJoinOp(on userId)
    Left (probe):  SourceOp(Requests)          ← streams
    Right (build): ProjectOp(SourceOp(Users))  ← fully loaded into hash table first
```
