# Execution Pipeline
*Batch model · Operators · Logical → Physical plan · Streaming vs blocking*

---

## 1. Batch Model

The unit of data flowing through the pipeline is a `Batch`. Every operator consumes and produces batches.

```go
type Batch struct {
    Length  int          // number of rows in this batch
    Schema  []ColumnMeta // parallel to Columns; one entry per column
    Columns []ColumnVector
}
```

`Length` is always the actual row count. There is no selection vector in Phase 1 —
every batch is dense. `FilterOp` compacts passing rows into new column vectors
rather than maintaining a `Sel` index. A `Sel` field may be added in Phase 2 if
profiling shows compaction cost matters.

Every operator allocates and returns a new `*Batch`. No batch pointer is ever
mutated after it is returned, and no backing arrays are aliased between the
returned batch and the operator's internal state. This eliminates lifetime
contracts at the cost of allocation, which is acceptable for Phase 1 workloads
(short-lived batches, low live set, negligible GC pressure).

The exception is `SortOp`, `HashAggregateOp`, and `HashJoinOp`: all three
fully materialise their input (or one side of it) into internal storage before
emitting any output, so they carry the same class of heap pressure regardless
of the allocation strategy. `HashJoinOp` materialises the entire right
(build) subquery into a hash table; the left (probe) side then streams through
it batch by batch.

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

Ambiguity resolution after a join: a `FieldRef{Table:"", Name:"x"}` matches any
column where `Name == "x"`; if two or more columns match, the expression compiler
returns an error and the query must qualify the reference (e.g. `requests.x`).
A `FieldRef{Table:"requests", Name:"x"}` matches only columns where both
`Name == "x"` and `Origin == "requests"`.

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
type Int32Vector    struct { Values []int32;       Nulls []uint64 }
type Float64Vector  struct { Values []float64;     Nulls []uint64 }
type StringVector   struct { Values []string;      Nulls []uint64 } // Phase 1; dictionary-encoded in Phase 2
type BoolVector     struct { Values []bool;        Nulls []uint64 }
type DatetimeVector struct { Values []time.Time;   Nulls []uint64 }
type TimespanVector struct { Values []time.Duration; Nulls []uint64 }
type DynamicVector  struct { Values []string;      Nulls []uint64 } // plain JSON string
```

`Nulls` is a packed bitset: bit `i` set means row `i` is null.
All concrete types implement `Len()`, `IsNull(i int) bool`, and `columnVector()`.

---

## 2. Operator Interface

Every operator implements a two-method pull-based interface:

```go
package physical

import "gobbler-query/query/batch"

type Operator interface {
    Next() (*batch.Batch, error)
    Close() error
}
```

`Next()` returns `nil, io.EOF` when the operator is exhausted. Operators chain by calling `Next()` on their input. `Close()` releases any held resources (file handles, hash tables).

---

## 3. Operator Catalogue

### SourceOp
Wraps the batch reader. Root of every plan.

```go
type SourceOp struct {
    reader source.BatchReader
}

func (op *SourceOp) Next() (*batch.Batch, error) {
    return op.reader.NextBatch()
}
```

### FilterOp
Evaluates a compiled `BoolExpr` row-by-row and **compacts** the passing rows
into a new `*Batch` with fresh column vectors. The output is always a dense batch
with `Length` equal to the number of passing rows.

```go
type FilterOp struct {
    input Operator
    expr  expr.BoolExprEvaluator
}
```

### ProjectOp
Evaluates scalar expressions and produces new column vectors.

```go
type ProjectOp struct {
    input Operator
    items []expr.ScalarExprEvaluator
    names []string
}
```

### HashAggregateOp
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
Build side loads the right subquery into a hash table; probe side streams the left source through it.

```go
type HashJoinOp struct {
    left      Operator
    right     Operator
    buildSide buildTable
    probeKeys []expr.FieldRef
    buildKeys []expr.FieldRef
    state     joinState
}
```

### SortOp
Materialises all rows from input, sorts, then emits sorted batches.

```go
type SortOp struct {
    input Operator
    items []SortItem
    done  bool
    rows  *materializedRows
}
```

### LimitOp
Passes through batches until `remaining` rows have been emitted.

```go
type LimitOp struct {
    input     Operator
    remaining int
}
```

### CountOp
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

The logical plan is a direct structural mirror of the AST — one node per grammar stage, each wrapping its input. No optimization passes are applied in Phase 1.

> **Why keep the logical/physical split in Phase 1?**  
> Phase 2 columnar segment storage will require optimization passes (predicate pushdown, partition pruning, join reordering) that slot cleanly between logical and physical. Keeping the split now means Phase 2 adds passes without restructuring the parser, the AST, or any operator.

### Logical nodes

- `LogicalSource` — type name + resolved time window bounds
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
| `LogicalSource` | `SourceOp` | Wraps `FileSource` or `BlobSource` |
| `LogicalWhere` | `FilterOp` | Compiled `BoolExpr` |
| `LogicalProject` | `ProjectOp` | Compiled scalar expressions |
| `LogicalSummarize` | `HashAggregateOp` | Hash table + agg state |
| `LogicalJoin` | `HashJoinOp` | Right side = build; left side = probe |
| `LogicalSort` | `SortOp` | Full in-memory materialize |
| `LogicalTake` | `LimitOp` | Row-count limit |
| `LogicalCount` | `CountOp` | Sugar for `summarize count()` |

### Physical plan builder

```go
func buildPhysical(node LogicalNode, root catalog.StorageRoot) (physical.Operator, error) {
    switch n := node.(type) {

    case *LogicalSource:
        path, err := root.Resolve(n.TypeName)
        if err != nil {
            return nil, err
        }
        return newSourceOp(path, n.TimeWindow), nil

    case *LogicalWhere:
        child, _ := buildPhysical(n.Input, root)
        return &physical.FilterOp{Input: child, Expr: expr.CompileBoolExpr(n.Expr)}, nil

    case *LogicalProject:
        child, _ := buildPhysical(n.Input, root)
        return &physical.ProjectOp{Input: child, Items: expr.CompileProjectItems(n.Items)}, nil

    case *LogicalSummarize:
        child, _ := buildPhysical(n.Input, root)
        return physical.NewHashAggregateOp(child, expr.CompileAggFuncs(n.Aggs), expr.CompileGroupBy(n.GroupBy)), nil

    case *LogicalJoin:
        left, _ := buildPhysical(n.Left, root)
        right, _ := buildPhysical(n.Right, root)
        return physical.NewHashJoinOp(left, right, expr.CompileJoinKeys(n.Keys)), nil

    case *LogicalSort:
        child, _ := buildPhysical(n.Input, root)
        return physical.NewSortOp(child, expr.CompileSortItems(n.Items)), nil

    case *LogicalTake:
        child, _ := buildPhysical(n.Input, root)
        return &physical.LimitOp{Input: child, Remaining: n.N}, nil

    case *LogicalCount:
        child, _ := buildPhysical(n.Input, root)
        return &physical.CountOp{Input: child}, nil
    }

    return nil, fmt.Errorf("unknown logical node: %T", node)
}
```

---

## 5. Streaming vs Blocking

### Classification

**Streaming operators** — process one batch and return immediately; O(batch) memory:

| Operator | Why it streams |
|---|---|
| `FilterOp` | Predicate applied per-batch; result returned immediately |
| `ProjectOp` | Column transforms applied per-batch |
| `LimitOp` | Stops pulling once the row count is reached |

**Blocking operators** — must consume all upstream output before emitting anything:

| Operator | Memory footprint | Why it blocks |
|---|---|---|
| `HashAggregateOp` | O(distinct groups × agg state) | Final group totals unknown until last row |
| `CountOp` | O(1) | Final count unknown until EOF |
| `SortOp` | O(total rows) | Minimum row unknown until all rows are seen |
| `HashJoinOp` (build side) | O(right subquery rows) | Hash table must be fully loaded before probing |

`HashJoinOp` is **semi-blocking**: the right (build) side runs to completion once, then the left (probe) side streams through it. After the build phase the join is streaming.

### Pipeline breaks

A blocking operator creates a **pipeline break** — everything upstream runs to completion before anything downstream receives a row.

```
Logs | where status >= 400 | summarize count() by region | sort by count_ desc | take 5
```

```
streaming:   SourceOp → FilterOp       (batches flow continuously)
                  ↓ break
blocking:    HashAggregateOp            (consumes all; emits one result batch)
                  ↓ break
blocking:    SortOp                     (sorts the single result batch)
                  ↓
streaming:   LimitOp                   (passes first 5 rows)
```

The second break is trivially cheap here — `summarize` has already collapsed the input to at most one row per group, so `SortOp` sees a tiny batch regardless of CSV volume.

### Planner implications

**Stage ordering** — `sort` before `summarize` would materialise potentially millions of raw rows; `summarize` discards the order anyway. `take` before `summarize` changes query semantics (limits input rows, not output groups). Phase 1 executes any ordering correctly; a warning pass is a Phase 2 addition.

**`take` short-circuit** — `LimitOp` stops calling `Next()` once the limit is reached. After a purely streaming chain this cancels I/O mid-file. After a blocking operator there is no I/O benefit — the operator already consumed everything.

**Join build-side** — `HashJoinOp` always loads the right (subquery) side. Put the smaller relation on the right. Phase 2 can reorder based on estimated cardinalities.

**Time window** — the source time window (`last 24h`, `datetime(T1) .. datetime(T2)`, `*`) is passed to `SourceOp` at construction time, not translated into a `FilterOp`. The source layer handles file-level pruning and boundary-row filtering internally; the pipeline has no knowledge of the window. Explicit `timestamp` predicates in `where` stages are independent ordinary expressions evaluated by `FilterOp` and may or may not be consistent with the source window.

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
      SourceOp  ← opens only files within last 1h; skips boundary rows outside the window
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

The right subquery runs to completion before any `Requests` row is processed. After the build phase, `Requests` batches stream through the join and feed the aggregate.
