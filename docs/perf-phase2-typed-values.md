# Performance Phase 2 — Typed Compute + Compact Row Blocks
*Eliminate `any` boxing and replace `[][]any` row materialization with contiguous byte slabs*

---

## Motivation

Phase 1 intentionally prioritized correctness and simplicity. The current hot-path costs are:

1. Scalar evaluation returns `any`, which boxes values.
2. Group keys rely on string formatting (`fmt.Sprintf`) in tight loops.
3. Blocking operators materialize rows as `[][]any`.

The external API (`api/execute.go`) and operator interface (`Operator.Next()`, `Close()`) remain unchanged.

---

## Core Decisions

### 1) Keep `Value` for compute paths

`Value` remains the common typed payload for:

- scalar evaluation (`evalScalar`, `CompileScalar`)
- predicate comparisons
- aggregate accumulator ingest/result
- key encoding helpers

`Value` is **not** the storage format for blocking row materialization.

### 2) Use per-row byte encoding for row storage

Blocking operators (`SortOp`, `HashAggregateOp`, build side of `HashJoinOp`) use `rowStore` (a slice of `encodedRow`) rather than `[][]any`. Each `encodedRow` is a self-contained `[]byte` with an offsets table — no shared slab, no cross-row index arithmetic.

### 3) Move `ColumnType` to `query/batch` and add to `ColumnMeta`

`source.ColumnType` currently lives in `query/source/schema.go`, which already imports `query/batch`. Placing `source.ColumnType` directly in `batch.ColumnMeta` would create a cycle. The fix is to move `ColumnType` (and its constants) into `query/batch/batch.go` — it is a semantic schema concept, not a source-layer implementation detail. `query/source` then references `batch.ColumnType`, which is the correct dependency direction.

```go
// In query/batch/batch.go
type ColumnType int

const (
    TypeInt32 ColumnType = iota
    TypeInt64
    TypeFloat64
    TypeString
    TypeBool
    TypeDatetime
    TypeTimespan
    TypeDynamic
)

type ColumnMeta struct {
    Name   string
    Origin string
    Type   ColumnType
}
```

All existing `source.ColumnType` references in `query/source`, `query/planner`, `query/physical`, and `query/logical` are updated to `batch.ColumnType`.

### 4) Keep logical metadata separate from physical layout metadata

- Logical metadata (`ColumnMeta`): `Name`, `Origin`, `Type`
- Physical layout metadata (row-block internal): fixed offsets, varlen slot mapping, block growth policy

Offsets and packing policy are execution/layout details and must not be stored in shared schema metadata.

---

## `Value` Type (Compute Layer)

File: `query/expr/value.go`

```go
type ValueKind uint8

const (
    KindNull ValueKind = iota
    KindInt32
    KindInt64
    KindFloat64
    KindBool
    KindString
    KindDatetime
    KindTimespan
    KindDynamic
)

// Value is 40 bytes on 64-bit: 1 (Kind) + 7 (pad) + 8 (I) + 8 (F) + 16 (S ptr+len).
type Value struct {
    Kind ValueKind
    I    int64   // int32/int64/bool/timespan/datetime(unix nanos UTC)
    F    float64 // float64
    S    string  // string/dynamic (16-byte header; backing array shared, not copied)
}
```

Notes:

- Datetime is encoded as UTC UnixNano in `I`.
- Null is represented by `KindNull`.

---

## Row Store Spec (Storage Layer)

File: `query/physical/rows.go`

### Design

Each materialized row is a self-contained `encodedRow`: a `[]byte` holding all field values sequentially, plus a `[]uint32` offsets table with `ncols+1` entries. Consecutive offsets give the byte range of each field: field `i` occupies `data[offsets[i]:offsets[i+1]]`. A separate packed null bitmap covers the whole row.

This is simpler than a shared-slab design. Variable-length fields are handled naturally — the row just grows. Random access to any field is `data[offsets[i]:offsets[i+1]]` with no cross-row index arithmetic.

```go
// encodedRow is one materialized row.
type encodedRow struct {
    data    []byte   // field bytes packed sequentially
    offsets []uint32 // ncols+1 entries; field i is data[offsets[i]:offsets[i+1]]
    nulls   []uint64 // packed bitset; bit i set means field i is null
}

// rowStore holds all materialized rows for one blocking operator.
type rowStore struct {
    schema []batch.ColumnMeta // Name, Origin, Type per column
    rows   []encodedRow
}
```

### Encoding rules

All values are encoded as bytes in `encodedRow.data` in column order:

- `int32`: 4 bytes little-endian
- `int64`, `datetime`, `timespan`: 8 bytes little-endian
- `float64`: IEEE-754 bits, 8 bytes little-endian
- `bool`: 1 byte (`0` or `1`)
- `string`, `dynamic`: raw UTF-8 bytes (no length prefix — length is `offsets[i+1]-offsets[i]`)
- null fields: zero bytes written; nullness is in `nulls` bitset; `offsets[i] == offsets[i+1]`

`schema[i].Type` drives which encoding is used when appending and which decode is used when reading — no separate kind array needed.

### API signatures

```go
func newRowStore(schema []batch.ColumnMeta) *rowStore
func (rs *rowStore) appendBatch(b *batch.Batch) error
func (rs *rowStore) cellAt(row, col int) (expr.Value, bool)
func (rs *rowStore) buildBatch(start, end int) *batch.Batch
func compareRows(rs *rowStore, i, j int, keys []CompiledSortKey) int
```

---

## Changes by Step

### Step 1 — Typed scalar compute

Files: `query/expr/eval.go`, `query/expr/scalar.go`

- change scalar eval signatures from `any` to `Value`
- keep `RowPredicate` return type as `bool`

### Step 2 — Typed aggregators

File: `query/expr/agg.go`

- `AggAccumulator.Ingest` takes `Value`
- `Result()` returns `Value`

### Step 3 — Per-row byte encoding

File: `query/physical/rows.go`

- replace `[][]any` with `rowStore` (slice of `encodedRow`)
- `schema[i].Type` drives field encoding width and decode logic

### Step 4 — Binary group keys

File: `query/physical/group_key.go`

Replace `fmt.Sprintf`-based key construction with a type-prefixed, length-prefixed binary encoding that is injective (distinct values always produce distinct bytes):

```go
// appendValueKey encodes v into buf and returns the extended slice.
// The encoding is injective: different values always produce different bytes.
func appendValueKey(buf []byte, v Value) []byte {
    buf = append(buf, byte(v.Kind))
    switch v.Kind {
    case KindNull:
        // kind byte alone is sufficient
    case KindInt32, KindInt64, KindBool, KindTimespan, KindDatetime:
        buf = binary.LittleEndian.AppendUint64(buf, uint64(v.I))
    case KindFloat64:
        buf = binary.LittleEndian.AppendUint64(buf, math.Float64bits(v.F))
    case KindString, KindDynamic:
        buf = binary.LittleEndian.AppendUint32(buf, uint32(len(v.S)))
        buf = append(buf, v.S...)
    }
    return buf
}

// groupKey encodes the group-by columns of one row into scratch and returns
// it as a string. scratch is reused across calls (owned by the operator).
func groupKey(scratch []byte, row []Value, indices []int) string {
    scratch = scratch[:0]
    for _, idx := range indices {
        scratch = appendValueKey(scratch, row[idx])
    }
    return string(scratch) // one allocation per unique group key
}
```

Each operator owns a `scratch []byte` field pre-allocated once at construction and reused for every row.

---

## Migration Notes

- Keep existing `materializedRows` call sites, replace internals with `*rowStore`
- `SortOp`: sort `[]int` row index into `rs.rows` instead of sorting row slices
- `HashJoinOp`: build-side rows stay in `rowStore`; probe decodes only joined columns
- `HashAggregateOp`: group state holds row index into `rowStore`, not copied `[]any`

---

## What Is Not Changing

- `Operator` interface
- parser/lexer/AST
- logical plan model
- source-layer CSV reading APIs
- public `api.Execute` behavior

---

## Expected Impact

| Operator | Current bottleneck | Expected improvement |
|---|---|---|
| HashAggregate (high cardinality) | formatted string keys + boxing | 3-5x throughput |
| HashAggregate (low cardinality) | `any` boxing | 1.5-2x |
| Sort | `[][]any` allocation and pointer chasing | 1.5-2x |
| HashJoin (build side) | `[][]any` allocation | 1.5-2x |
| Filter/Project on small windows | often IO-bound | minimal |

---

## Benchmarking Plan

Add operator-focused benchmarks before refactor work:

1. `BenchmarkHashAgg_HighCardinality` (100k rows, 10k groups)
2. `BenchmarkHashAgg_LowCardinality` (100k rows, 5 groups)
3. `BenchmarkSort_100k` (100k rows, one int32 key)
4. `BenchmarkHashJoin_10k_x_50` (left 10k, right 50)

Run:

```bash
go test -bench=. -benchmem ./query/physical/
```

Record before/after `ns/op`, `B/op`, `allocs/op` in this doc.

Column meanings (reported by `go test -benchmem`):

| Column | Meaning |
|---|---|
| `ns/op` | Nanoseconds per benchmark iteration. Lower is faster. |
| `B/op` | Bytes allocated on the heap per iteration. Lower means less GC pressure. |
| `allocs/op` | Number of distinct heap allocations per iteration. Each allocation has a fixed overhead; reducing this count matters as much as reducing total bytes. |

Baseline: Windows/arm64, Go 1.24, `go test -bench=. -benchmem -count=1 ./query/physical/`

| Benchmark | ns/op (before) | B/op (before) | allocs/op (before) | ns/op (after) | B/op (after) | allocs/op (after) |
|---|---|---|---|---|---|---|
| BenchmarkHashAgg_HighCardinality | 42,853,788 | 19,113,341 | 850,412 | 23,578,856 | 16,236,689 | 470,366 |
| BenchmarkHashAgg_LowCardinality | 26,599,375 | 11,536,373 | 740,580 | 11,328,606 | 8,647,389 | 380,228 |
| BenchmarkSort_100k | 62,865,338 | 37,531,297 | 301,439 | 126,939,788 | 55,270,172 | 201,410 |
| BenchmarkHashJoin_10k_x_50 | 4,166,561 | 3,578,349 | 100,691 | 5,660,988 | 7,952,732 | 144,092 |

---

## Implementation Order

1. **Define `Value`** — add `query/expr/value.go` with `Value`, `ValueKind`, and `appendValueKey`. No dependencies on other changes; compiles standalone.
2. **Move `ColumnType` to `query/batch`** — move constants and type from `query/source/schema.go`; update all import sites. Add `Type batch.ColumnType` to `ColumnMeta`.
3. **Typed scalar compute (Step 1)** — change eval/scalar signatures; depends on `Value`.
4. **Typed accumulators (Step 2)** — depends on Step 1 (`Value`).
5. **Binary group keys (Step 4)** — depends on Step 1 (`Value`); isolated, immediate benchmark payoff.
6. **Row store (Step 3)** — depends on Step 2 (`ColumnMeta.Type`) and Step 1 (`Value`). Replace `materializedRows` with `rowStore`; migrate `SortOp`, `HashJoinOp`, `HashAggregateOp`.
7. **Benchmark** — run before step 1 (baseline) and after step 6 (result); fill in table above.
