# Performance Phase 2 — Typed Value Dispatch
*Replacing `any`-based evaluation with a structured `Value` type*

---

## Motivation

The current query engine uses Go's `any` (`interface{}`) throughout the evaluation
and materialisation layers. This was a deliberate Phase 1 choice — simplicity first,
correctness verified by tests, performance deferred. The three concrete costs are:

1. **Heap allocation per evaluated scalar.** Every call to `evalScalar()` or
   `CompileScalar()` boxes numeric/bool values onto the heap to return them as `any`.
2. **`fmt.Sprintf` group keys.** `groupKey()` in `physical/group_key.go` calls
   `fmt.Sprintf` once per row per group boundary to produce a map key string. For
   high-cardinality aggregations this is the dominant CPU cost.
3. **`[][]any` materialised rows.** `SortOp`, `HashAggregateOp`, and the build-phase
   of `HashJoinOp` all store rows as `[][]any`, boxing every value.

These costs are entirely internal to the query engine. The external API
(`api/execute.go`) and the operator interface (`Operator.Next()`) are unaffected
by this refactor.

---

## The `Value` Type

Add `query/expr/value.go`:

```go
type ValueKind uint8

const (
    KindNull     ValueKind = iota
    KindInt32
    KindInt64
    KindFloat64
    KindBool
    KindString
    KindDatetime
    KindTimespan
    KindDynamic
)

// Value is a discriminated union. Numeric and boolean values are stored
// inline (no heap allocation). String kinds use a Go string header
// (already heap-allocated). KindNull uses zero values for all fields.
type Value struct {
    Kind ValueKind  // uint8
    // 7 bytes padding (alignment)
    I    int64      // KindInt32, KindInt64, KindBool (0/1), KindTimespan (nanos), KindDatetime (UnixNano UTC)
    F    float64    // KindFloat64
    S    string     // KindString, KindDynamic
}
```

### In-memory layout (64-bit)

```
Offset  Size  Field
──────  ────  ─────────────────────────────────────────
  0       1   Kind  (uint8)
  1       7   [padding — to align I to 8-byte boundary]
  8       8   I     (int64)
 16       8   F     (float64)
 24      16   S     (string = ptr 8 + len 8)
──────  ────
 40 bytes total
```

**40 bytes per `Value`.** Numeric and boolean kinds waste the `F` and `S` fields,
but avoiding `unsafe` union tricks is worth the trade-off at this scale.

A naive addition of `T time.Time` would push the struct to 64 bytes — `time.Time`
alone is 24 bytes (wall `uint64` + ext `int64` + loc `*Location`). Instead,
datetimes are encoded as UTC UnixNano in `I`, with conversion at the column-vector
boundary:

```go
// DatetimeVector → Value
v := Value{Kind: KindDatetime, I: t.UnixNano()}

// Value → time.Time (e.g. when building result rows)
t := time.Unix(0, v.I).UTC()
```

Gobbler datetimes are already UTC with nanosecond resolution from CSV, so no
precision or timezone information is lost.

`Value` is passed by value. For numeric/bool kinds there is no allocation.
For string kinds the 16-byte string header is copied (the backing array is already
heap-allocated and shared — same cost as today).
`KindNull` uses zero values for all fields.

---

## Changes Required (four steps)

### Step 1 — `evalScalar` and `CompileScalar` return `Value`

**Files:** `query/expr/eval.go`, `query/expr/scalar.go`

- Change `evalScalar(e ast.ScalarExpr, row *batch.Batch, i int) (any, error)` to
  return `(Value, error)`.
- Change `ScalarEval` func type from `func(*batch.Batch, int) (any, error)` to
  `func(*batch.Batch, int) (Value, error)`.
- Change `CompiledProjectItem.Eval` field accordingly.
- All internal switch cases construct a typed `Value{}` instead of returning a
  bare Go value.

`Compile() RowPredicate` is unaffected — it still returns `bool`. Internally it
calls `evalScalar` and compares two `Value` results; comparison logic moves into
a `Value.Compare(other Value) int` helper.

### Step 2 — Aggregation accumulators accept `Value`

**File:** `query/expr/agg.go`

- Change `AggAccumulator.Ingest(val any, null bool)` to
  `Ingest(val Value)` (null is encoded in `val.Kind == KindNull`).
- Update all concrete accumulators: `countAcc`, `sumInt64Acc`, `avgAcc`, `minAcc`,
  `maxAcc`, `dcountAcc`.
- `Result()` returns `Value` instead of `any`.

### Step 3 — Replace `[][]any` with `[][]Value`

**File:** `query/physical/rows.go`

- Change `materializedRows` field type from `[][]any` to `[][]Value`.
- Update `appendRow()`, `buildBatchFromRows()`, and the null-tracking parallel
  `[]bool` slice (null is now encoded in `Value.Kind`, so the separate null slice
  can be removed).
- Update all operators that call these helpers: `SortOp`, `HashAggregateOp`,
  `HashJoinOp`.

### Step 4 — Replace `groupKey()` with binary encoding

**File:** `query/physical/group_key.go`

Current code calls `fmt.Sprintf` on each `[]any` row. Replace with a `[]byte`
scratch buffer encoded per `Value.Kind`:

```go
// Encode a Value into buf; buf is grown as needed and reused across calls.
// The encoding must be injective: distinct values must produce distinct bytes.
func appendValueKey(buf []byte, v Value) []byte {
    buf = append(buf, byte(v.Kind))
    switch v.Kind {
    case KindNull:
        // kind byte is sufficient
    case KindInt32, KindInt64, KindBool, KindTimespan:
        buf = binary.LittleEndian.AppendUint64(buf, uint64(v.I))
    case KindFloat64:
        buf = binary.LittleEndian.AppendUint64(buf, math.Float64bits(v.F))
    case KindString, KindDynamic:
        buf = binary.LittleEndian.AppendUint32(buf, uint32(len(v.S)))
        buf = append(buf, v.S...)
    }
    return buf
}
```

`groupKey()` becomes:

```go
func groupKey(scratch []byte, row []Value, indices []int) string {
    scratch = scratch[:0]
    for _, idx := range indices {
        scratch = appendValueKey(scratch, row[idx])
    }
    return string(scratch) // one allocation; no fmt.Sprintf
}
```

Each operator that calls `groupKey` owns a `scratch []byte` field, pre-allocated
once at `Open()` time and reused for every row.

---

## What Is Not Changing

- `Operator` interface (`Next()`, `Close()`) — unchanged.
- `batch.Batch` / `ColumnVector` types — unchanged.
- `ast.*` expression nodes — unchanged.
- `logical.*` plan nodes — unchanged.
- `api/execute.go` public API — unchanged.
- The source layer (`query/source/`) — unchanged.
- The lexer and parser — unchanged.

---

## Expected Impact

| Operator | Current bottleneck | Expected improvement |
|----------|-------------------|----------------------|
| `HashAggregateOp` (high cardinality) | `fmt.Sprintf` per row | 3–5× throughput |
| `HashAggregateOp` (low cardinality) | agg `any` boxing | 1.5–2× |
| `SortOp` | `[][]any` allocation | 1.5–2× |
| `HashJoinOp` (build phase) | `[][]any` allocation | 1.5–2× |
| `FilterOp` / `ProjectOp` | minimal; I/O-bound | negligible |
| CSV read + parse (source layer) | disk I/O | unchanged |

Queries that are purely filter/project over a small time window are already
I/O-bound and will see little change. Queries with `summarize`, multi-key `join`,
or `sort` over large result sets will benefit most.

---

## Benchmarking Plan

Before starting the refactor, add `_test.go` files with `Benchmark*` functions
targeting the hot operators so the improvement can be measured objectively:

1. `BenchmarkHashAgg_HighCardinality` — 100k rows, 10k unique groups
2. `BenchmarkHashAgg_LowCardinality` — 100k rows, 5 groups
3. `BenchmarkSort_100k` — 100k rows, single int32 sort key
4. `BenchmarkHashJoin_10k_x_50` — left 10k rows, right 50 rows

Run before and after each step (`go test -bench=. -benchmem ./query/physical/`).
Commit baseline numbers in this doc once measured.

---

## Baseline Numbers
*(to be filled in before work begins)*

| Benchmark | ns/op | B/op | allocs/op |
|-----------|-------|------|-----------|
| BenchmarkHashAgg_HighCardinality | — | — | — |
| BenchmarkHashAgg_LowCardinality | — | — | — |
| BenchmarkSort_100k | — | — | — |
| BenchmarkHashJoin_10k_x_50 | — | — | — |

---

## Implementation Order

1. Add `query/expr/value.go` with `Value` + `ValueKind` + `appendValueKey`.
2. Step 4 (`groupKey`): isolated change, immediate benchmark payoff, no API surface.
3. Step 1 (`evalScalar` / `CompileScalar`): compiles the expression tree.
4. Step 2 (agg accumulators): depends on Step 1.
5. Step 3 (`rows.go` / operators): depends on Steps 1 and 2.
6. Run benchmarks; fill in "after" column; commit.
