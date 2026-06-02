# Source Layer
*Catalog · Schema · CSVBatchReader · File selection · Time window*

---

## 1. Catalog

Before the engine can execute any query it must be given a **catalog** — a map
from query-visible table names to their physical storage locations. Table names
are never resolved to paths inside the planner or operators; resolution happens
once, at `SourceOp` construction time.

### Storage model

Gobbler writes each item type's CSV files into a named bucket:

- **File mode** — a subdirectory of `outputDir`
- **Blob mode** — an Azure Blob Storage container

The bucket name is the item definition's `folder` property when set, or the type
name when `folder` is unset. Multiple item types can share a bucket (same
`folder` value). The query-visible table name is always the item type's `name`
field — never the `folder` value.

Because mode is per-entry, a catalog can in principle mix file-backed and
blob-backed tables in the same query.

### Types

```go
package catalog

type StorageMode int

const (
    StorageModeFile StorageMode = iota
    StorageModeBlob
)

// TableEntry describes where one query table's data lives in storage.
// StorageBucket is pre-resolved from the item definition's "folder" property
// (or the type name when "folder" is unset). The engine never sees that
// indirection — it works only with the resolved name.
type TableEntry struct {
    StorageBucket string      // subdirectory name (file) or container name (blob)
    Mode        StorageMode

    // File mode only.
    OutputDir string

    // Blob mode only.
    AccountName string
    AccountKey  string // never serialised into the logical or physical plan
}

// Resolve returns the fully-qualified path (file mode) or URL (blob mode)
// for this entry's storage bucket.
func (e *TableEntry) Resolve() (string, error) {
    switch e.Mode {
    case StorageModeFile:
        return filepath.Join(e.OutputDir, e.StorageBucket), nil
    case StorageModeBlob:
        return fmt.Sprintf("https://%s.blob.core.windows.net/%s",
            e.AccountName, e.StorageBucket), nil
    default:
        return "", fmt.Errorf("unknown storage mode %d", e.Mode)
    }
}

// Catalog maps query-visible table names to their storage locations.
// Key: item type name (the "name" field in the item definition).
// Value: pre-resolved storage entry.
type Catalog map[string]*TableEntry
```

### Example

An auth-service Gobbler instance using file mode with three types, two of which
share a folder:

```json
{
  "name": "login",   "folder": "auth-events" }
  "name": "signup",  "folder": "auth-events" }
  "name": "userinfo"                          }
```

Catalog constructed from those definitions:

```go
Catalog{
    "login":    &TableEntry{StorageBucket: "auth-events", Mode: StorageModeFile, OutputDir: "/data"},
    "signup":   &TableEntry{StorageBucket: "auth-events", Mode: StorageModeFile, OutputDir: "/data"},
    "userinfo": &TableEntry{StorageBucket: "userinfo",    Mode: StorageModeFile, OutputDir: "/data"},
}
```

`login` and `signup` resolve to the same directory (`/data/auth-events`); their
CSV files are interleaved in that directory and distinguished by type name in the
filename. `userinfo` resolves to `/data/userinfo`.

### Engine usage

```go
entry, ok := catalog[tableName]
if !ok {
    return nil, fmt.Errorf("unknown table %q", tableName)
}
path, err := entry.Resolve()
if err != nil {
    return nil, err
}
src, err := source.NewFileSource(path, tableName, start, end, batchSize)
```

How the catalog is constructed and passed to the engine is a gobbler-query API
concern, to be designed separately.

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

### BatchReader interface

`SourceOp` depends on this interface, not on any concrete type:

```go
// BatchReader is the interface satisfied by FileSource (and BlobSource in future).
type BatchReader interface {
    NextBatch() (*batch.Batch, error)
    Close() error
}
```

### FileSource

`FileSource` implements `BatchReader`. It treats the set of selected CSV files as
one logical sequence of rows and fills each batch to exactly `batchSize` rows,
crossing file boundaries as needed. Only the final batch of the entire sequence
may be smaller.

```go
func NewFileSource(typeDir string, typeName string, start, end time.Time, batchSize int) (*FileSource, error)
```

- `typeDir` — the resolved directory for this type (from `TableEntry.Resolve`).
- `typeName` — stored as the `Origin` in every `ColumnMeta` this source emits.
- `start`, `end` — the resolved time window (zero = open bound).
- Loads `type.json` from `typeDir` on construction; returns error if missing or
  malformed.
- Runs file-selection pruning at construction time; stores the ordered file list.
- Opens the first file immediately so schema field-count validation happens before
  any `NextBatch` call.

**Batch size is a configurable parameter** passed to `NewFileSource`. Tests use
256 rows — small enough to produce multiple batches per testgen file (500 rows)
and to exercise cross-file batch boundaries, while remaining a realistic value
that needs no special-casing in production code.

### Logical concatenation and row-level filtering

The selected files are logically concatenated into a single ordered row stream.
`timestamp` is always column 0 (Gobbler prepends it). Row-level timestamp checks
are applied only to the boundary files:

| Position in selection | Row-level rule |
|---|---|
| First file only | Skip rows where `timestamp < start` (leading skip) |
| Middle files | All rows included — no per-row check |
| Last file only | Stop when `timestamp > end` (trailing stop) |
| First == Last (one file) | Both leading skip and trailing stop apply |
| Open bound (`start` or `end` zero) | The corresponding check is skipped |
| Full scan `(*)` | No row-level filtering at all |

This means the inner read loop performs a timestamp parse only for rows in the
boundary files; middle-file rows are appended to the builders without any
timestamp inspection.

### Internal structure

```go
type FileSource struct {
    files       []string        // ordered selected file paths
    fileIdx     int             // index of the currently open file
    file        *os.File        // currently open file handle
    csv         *csv.Reader     // wraps file
    schema      *Schema         // loaded once from type.json
    typeName    string
    batchSize   int
    start       time.Time       // zero = open lower bound
    end         time.Time       // zero = open upper bound
    colBuilders []columnBuilder // scratch buffers; sized batchSize, reused across all batches
    done        bool
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

Concrete builders: `int32Builder`, `float64Builder`, `stringBuilder`, `boolBuilder`,
`datetimeBuilder`, `timespanBuilder`, `dynamicBuilder`.

`timespanBuilder` parses cells with `time.ParseDuration` and stores `time.Duration`.

`dynamicBuilder` is identical to `stringBuilder`. Gobbler writes dynamic fields as
CSV-quoted JSON, so Go's `csv.Reader` automatically unquotes the field before
`Append` is called — the builder stores a plain JSON string.

### NextBatch logic

``` go
rows = 0
loop until rows == batchSize:
    rec, err = csv.Read()
    if err == EOF:
        advance to next file
        if no next file: break          // end of sequence
        continue                        // refill from new file
    if err != nil: return error

    ts = parse rec[0] as datetime
    if isFirstFile and ts < start: continue   // leading skip
    if isLastFile  and ts > end:  break       // trailing stop

    for each column: builders[col].Append(rec[col])
    rows++

if rows == 0: return nil, io.EOF
build and return Batch{Length: rows, ...}
reset all builders
```

No allocations inside the inner loop. Builders pre-allocate their `values` and
`nulls` slices once at construction (sized `batchSize` and `batchSize/64`
respectively) and reuse them across every batch.

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

- **Empty cell** — treated as null for all types (null bit set). Gobbler writes
  `""` for any absent optional field.
- **Malformed non-empty value** — treated as null in Phase 1 rather than failing.
- **CSV parse errors** — propagated immediately.
- **Schema mismatch** — on opening each file, the first data row's field count is
  compared against `type.json`. If mismatched, returns an error before any row is
  appended (descriptive: file path, expected count, actual count). This catches
  stale CSV files left over after a type definition change.

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
