# Source Layer
*StorageRoot · Schema · CSVBatchReader · File selection · Time window*

---

## 1. StorageRoot

Before the engine can execute any query it must be given a **storage root** — the context that maps type names to data locations. Type names are never resolved to paths inside the planner or operators; resolution happens once, at `SourceOp` construction time.

```go
package catalog

// StorageRoot resolves type names to data source paths.
type StorageRoot interface {
    Resolve(typeName string) (path string, err error)
}
```

### File mode

```go
// FileRoot points to a local directory that gobbler has written output into.
type FileRoot struct {
    Dir string // e.g. "C:\\temp\\gobbler-output"
}

func (r *FileRoot) Resolve(typeName string) (string, error) {
    return filepath.Join(r.Dir, typeName), nil
}
```

### Blob mode

```go
// BlobRoot points to an Azure Blob Storage account.
// The account key is held here so it is never serialised into the logical or physical plan.
type BlobRoot struct {
    AccountName string // e.g. "mystorageaccount"
    AccountKey  string // base64-encoded storage account key
}

func (r *BlobRoot) Resolve(typeName string) (string, error) {
    // Type name maps to a container of the same name within the account.
    return fmt.Sprintf("https://%s.blob.core.windows.net/%s", r.AccountName, typeName), nil
}
```

| Mode | Root | Type name resolves to |
|---|---|---|
| File | Root directory path | `<rootDir>/Logs/` — subdirectory gobbler writes for that type |
| Blob | Storage account name + key | Azure container named `Logs` in the given account |

---

## 2. Schema Representation

The schema for a type is stored in `type.json` in the same directory or container as the CSV files. Gobbler writes this file when the directory/container is created. Column names, order, and types are taken directly from it — no inference is performed.

```go
type ColumnType int

const (
    TypeInt32 ColumnType = iota
    TypeFloat64
    TypeString
    TypeBool
    TypeDatetime
    TypeTimespan // Go duration string, e.g. "1h10m10s"; stored as time.Duration
    TypeDynamic  // opaque; stored as unquoted JSON string
)

type ColumnSchema struct {
    Name string
    Type ColumnType
}

type Schema struct {
    Columns []ColumnSchema
}
```

---

## 3. CSVBatchReader

### Public API

```go
package csvreader

type Reader struct {
    r         *csv.Reader
    schema    *Schema
    batchSize int
}

func NewReader(path string, batchSize int) (*Reader, error)

func (r *Reader) Schema() *Schema

func (r *Reader) NextBatch() (*batch.Batch, error)
```

- `Schema()` returns the schema read from `type.json`.
- `NextBatch()` returns `nil, io.EOF` when the file is exhausted.
- `batch.Batch` is from `query/batch`.

### Internal structure

```go
type Reader struct {
    file         *os.File
    r            *csv.Reader
    schema       *Schema
    batchSize    int
    colBuilders  []columnBuilder // scratch buffers reused across batches
}
```

`columnBuilder` is a small decoding helper:

```go
type columnBuilder interface {
    Append(raw string)
    Build(n int) batch.ColumnVector
    Reset()
}
```

Concrete builders: `int32Builder`, `float64Builder`, `stringBuilder`, `boolBuilder`, `datetimeBuilder`, `timespanBuilder`, `dynamicBuilder`.

`timespanBuilder` parses cells with `time.ParseDuration` and stores `time.Duration`.

`dynamicBuilder` is identical to `stringBuilder`. Gobbler writes dynamic fields as CSV-quoted JSON, so Go's `csv.Reader` automatically unquotes the field before `Append` is called — the builder stores a plain JSON string.

### NextBatch

```go
func (r *Reader) NextBatch() (*batch.Batch, error) {
    rows := 0

    for rows < r.batchSize {
        rec, err := r.r.Read()
        if err == io.EOF {
            break
        }
        if err != nil {
            return nil, err
        }

        for colIdx, raw := range rec {
            r.colBuilders[colIdx].Append(raw)
        }
        rows++
    }

    if rows == 0 {
        return nil, io.EOF
    }

    cols := make([]batch.ColumnVector, len(r.schema.Columns))
    for i := range cols {
        cols[i] = r.colBuilders[i].Build(rows)
        r.colBuilders[i].Reset()
    }

    return &batch.Batch{
        Length:  rows,
        Columns: cols,
    }, nil
}
```

No allocations inside the inner loop. Builders reuse their `values` and `nulls` slices across every batch (each sized `batchSize` / `batchSize/64` respectively), keeping GC pressure low.

### Column builders

`int32Builder`:

```go
type int32Builder struct {
    values []int32
    nulls  []uint64
    idx    int
}

func (b *int32Builder) Append(raw string) {
    if raw == "" {
        setNull(b.nulls, b.idx)
        b.values[b.idx] = 0
    } else {
        v, _ := strconv.ParseInt(raw, 10, 32)
        b.values[b.idx] = int32(v)
    }
    b.idx++
}

func (b *int32Builder) Build(n int) batch.ColumnVector {
    return &batch.Int32Vector{Values: b.values[:n], Nulls: b.nulls}
}

func (b *int32Builder) Reset() { b.idx = 0 }
```

`stringBuilder` (Phase 1 — raw strings; Phase 2 will use dictionary encoding):

```go
type stringBuilder struct {
    values []string
    nulls  []uint64
    idx    int
}

func (b *stringBuilder) Append(raw string) {
    if raw == "" {
        setNull(b.nulls, b.idx)
        b.values[b.idx] = ""
    } else {
        b.values[b.idx] = raw
    }
    b.idx++
}
```

### Error handling

- **Empty cell** — treated as null for all types (null bit set). Gobbler writes `""` for any absent optional field.
- **Malformed non-empty value** — treated as null in Phase 1 rather than failing.
- **CSV parse errors** — propagated immediately.
- **Schema mismatch** — `NewReader` reads the first data row on open and counts its fields. If the field count does not match `type.json`, it returns an error before any row is processed (descriptive: file path, expected count, actual count). This catches stale CSV files left over after a type definition change.

---

## 4. File Selection and Time-Range Pruning

### Filename convention

Gobbler names each CSV file after the timestamp of its **first item**:

```
2024-01-15_13-22-07.123_logs.csv
2024-01-15_13-35-41.009_logs.csv
```

The `file_timestamp` is the **lower bound** of item timestamps in that file. Items are written in strictly increasing timestamp order, so file ordering by name is reliable.

### File selection rule

For a query window `[T_start, T_end]`, sort all files in the type's directory by `file_timestamp` ascending:

- **First file (N)**: first file where `file_timestamp >= T_start`
- **Last file (M)**: last file where `file_timestamp <= T_end`
- **Read**: files N through M inclusive

Row-level filtering within the boundary files:

| File | Rule |
|---|---|
| File N | Skip rows where `timestamp < T_start` |
| Files N+1 … M-1 | All rows pass — no per-row check needed |
| File M | Skip rows where `timestamp > T_end` |

File selection is pure I/O optimisation. The `FilterOp` predicate is still the source of truth for correctness.

**Owner**: `source/pruning.go`. `FileSource` applies this rule; the planner extracts `[T_start, T_end]` from the `TimeWindow` AST node and passes it into `SourceOp` at construction time.

### Time window forms

Every source requires an explicit time window — it is part of the `Source` syntax and a parse error to omit it. This prevents accidental full-table scans.

| Form | Example | Meaning |
|---|---|---|
| Relative lookback | `Logs(last 24h)` | All files from `now() − 24h` onward |
| Absolute range | `Logs(datetime(2026-01-15 09:00:00) .. datetime(2026-01-15 18:00:00))` | Files overlapping the given range |
| Full scan | `Logs(*)` | All files — no time filter |

**`DatetimeLit` format** — Gobbler's native format: `YYYY-MM-DD HH:MM:SS.mmm` (space separator, no `T`, no timezone designator). Time part and milliseconds are optional:

```
datetime(2026-01-15)
datetime(2026-01-15 09:30:00)
datetime(2026-01-15 09:30:00.000)
```

**`last <duration>`** — the planner computes `T_start = now() − duration` at query start time. No upper-bound file pruning is applied for this form.

**`*` (full scan)** — all files in the type's directory/container are read. Requiring the literal `*` rather than allowing a bare source name makes the cost intentionally visible in the query text.

In blob mode every matching blob must be opened and downloaded. Use narrow windows for large types.

**Phase 2**: once segment metadata (min/max statistics per segment) is available, the planner will use the time window to prune segments before opening any files or blobs, without changing query syntax.
