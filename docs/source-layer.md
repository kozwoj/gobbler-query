# Source Layer
*Catalog · Schema · TableReader · Entry selection · Time window*

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
    TypeName      string      // query-visible table name (equals key in Catalog)
    StorageBucket string      // subdirectory name (file) or container name (blob)
    Mode          StorageMode

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
    "login":    &TableEntry{TypeName: "login",    StorageBucket: "auth-events", Mode: StorageModeFile, OutputDir: "/data"},
    "signup":   &TableEntry{TypeName: "signup",   StorageBucket: "auth-events", Mode: StorageModeFile, OutputDir: "/data"},
    "userinfo": &TableEntry{TypeName: "userinfo", StorageBucket: "userinfo",    Mode: StorageModeFile, OutputDir: "/data"},
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
reader, err := source.NewTableReader(entry, start, end, batchSize)
```

How the catalog is constructed and passed to the engine is a gobbler-query API
concern, to be designed separately.

---

## 2. Schema Representation

The schema for a type is stored in `{typeName}.json` in the same directory or container as the CSV files. Gobbler writes this file when the directory/container is created. Column names, order, and types are taken directly from it — no inference is performed.

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

## 3. TableReader

### TableReader interface

`SourceOp` depends on this interface and never on a concrete type.

```go
// TableReader is the interface implemented by FileTableReader and BlobTableReader.
type TableReader interface {
    GetNextBatch() (*batch.Batch, error)
    Close() error
}
```

`GetNextBatch` returns the next dense batch of rows. It returns `(nil, io.EOF)` when the
sequence is exhausted. Any other error is a hard failure.

### Factory

```go
// NewTableReader constructs the appropriate TableReader based on entry.Mode.
// entry.TypeName must be set.
func NewTableReader(entry *catalog.TableEntry, start, end time.Time, batchSize int) (TableReader, error)
```

Callers that know the mode at compile time may use the concrete constructors directly.

### FileTableReader

`FileTableReader` treats the set of selected CSV files as one logical sequence of
rows and fills each batch to exactly `batchSize` rows, crossing file boundaries
as needed. Only the final batch of the entire sequence may be smaller.

```go
func NewFileTableReader(typeDir, typeName string, start, end time.Time, batchSize int) (*FileTableReader, error)
```

- `typeDir` — the resolved directory for this type (from `TableEntry.Resolve`).
- `typeName` — stored as the `Origin` in every `ColumnMeta` this reader emits.
- `start`, `end` — the resolved time window (zero = open bound).
- Loads `{typeName}.json` from `typeDir` on construction; returns error if missing or
  malformed.
- Runs entry-selection pruning at construction time; stores the ordered file list.
- Opens the first file immediately so schema field-count validation happens before
  any `GetNextBatch` call.

**Batch size is a configurable parameter** passed to `NewFileTableReader`. Tests
use 256 rows — small enough to produce multiple batches per testgen file (500 rows)
and to exercise cross-entry batch boundaries, while remaining a realistic value
that needs no special-casing in production code.

```go
type FileTableReader struct {
    files       []string        // ordered selected file paths
    fileIdx     int             // index of the currently open file
    file        *os.File        // currently open file handle
    csv         *csv.Reader     // wraps file
    schema      *Schema         // loaded once from {typeName}.json
    typeName    string
    batchSize   int
    start       time.Time       // zero = open lower bound
    end         time.Time       // zero = open upper bound
    colBuilders []columnBuilder // scratch buffers; sized batchSize, reused across all batches
    done        bool
}
```

### BlobTableReader

`BlobTableReader` is the blob-mode equivalent. It selects blobs by name (same
naming convention as files) and streams each blob directly as an `io.ReadCloser`
— no temporary files are created.

```go
func NewBlobTableReader(accountName, accountKey, container, typeName string, start, end time.Time, batchSize int) (*BlobTableReader, error)
```

- `container` — the Azure Blob Storage container name (pre-resolved from
  `TableEntry.StorageBucket`).
- Reads `{typeName}.json` from the container on construction (a blob named `"{typeName}.json"`).
- Lists blobs in the container and applies the same entry-selection pruning as
  `FileTableReader`.
- Opens (downloads) the first blob immediately for the same upfront schema
  validation.

```go
type BlobTableReader struct {
    blobs       []string        // ordered selected blob names
    blobIdx     int             // index of the currently streaming blob
    stream      io.ReadCloser   // current blob download stream
    csv         *csv.Reader     // wraps stream
    schema      *Schema
    typeName    string
    batchSize   int
    start       time.Time
    end         time.Time
    colBuilders []columnBuilder
    client      *azblob.Client  // Azure SDK client; holds account credentials
    container   string
    done        bool
}
```

### Shared helper functions

`FileTableReader` and `BlobTableReader` share logic through package-private helper
functions rather than a shared internal interface:

| Helper | Purpose |
|---|---|
| `parseSchema(data []byte) (*Schema, error)` | Unmarshal `{typeName}.json` bytes |
| `selectEntries(names []string, start, end time.Time) []string` | Prune entry list to time window |
| `newColumnBuilders(schema *Schema, batchSize int) []columnBuilder` | Allocate per-column builders |
| `fillBatch(r *csv.Reader, builders []columnBuilder, batchSize int, isFirst, isLast bool, start, end time.Time) (rows int, done bool, err error)` | Core read loop; applies boundary filtering |
| `validateFieldCount(schema *Schema, rec []string) error` | First-row field-count check |

### GetNextBatch logic (both implementations)

```
rows = 0
loop until rows == batchSize:
    rec, err = csv.Read()
    if err == EOF:
        advance to next entry (file or blob stream)
        if no next entry: break
        continue
    if err != nil: return error

    ts = parse rec[0] as datetime
    if isFirstEntry and ts < start: continue   // leading skip
    if isLastEntry  and ts > end:  break       // trailing stop

    for each column: builders[col].Append(rec[col])
    rows++

if rows == 0: return nil, io.EOF
call FinalizeColumn(rows) on each builder → assemble Batch{Length: rows, ...}
call Reset() on each builder
```

No allocations inside the inner loop. Builders pre-allocate their `values` and
`nulls` slices once at construction (sized `batchSize` and `(batchSize+63)/64`
respectively) and reuse them across every batch.

### Logical concatenation and row-level filtering

The selected entries are logically concatenated into a single ordered row stream.
`timestamp` is always column 0 (Gobbler prepends it). Row-level timestamp checks
apply only to boundary entries:

| Position in selection | Row-level rule |
|---|---|
| First entry only | Skip rows where `timestamp < start` (leading skip) |
| Middle entries | All rows included — no per-row check |
| Last entry only | Stop when `timestamp > end` (trailing stop) |
| First == Last (one entry) | Both leading skip and trailing stop apply |
| Open bound (`start` or `end` zero) | The corresponding check is skipped |
| Full scan `(*)` | No row-level filtering at all |

### Column builders

`columnBuilder` is an unexported interface:

```go
type columnBuilder interface {
    Append(raw string)
    FinalizeColumn(n int) batch.ColumnVector
    Reset()
}
```

Concrete builders: `int32Builder`, `float64Builder`, `stringBuilder`, `boolBuilder`,
`datetimeBuilder`, `timespanBuilder`, `dynamicBuilder`.

`timespanBuilder` parses cells with `time.ParseDuration` and stores `time.Duration`.

`dynamicBuilder` is identical to `stringBuilder`. Gobbler writes dynamic fields as
CSV-quoted JSON, so Go's `csv.Reader` automatically unquotes the field before
`Append` is called — the builder stores a plain JSON string.

### Error handling

- **Empty cell** — treated as null for all types (null bit set). Gobbler writes
  `""` for any absent optional field.
- **Malformed non-empty value** — treated as null in Phase 1 rather than failing.
- **CSV parse errors** — propagated immediately.
- **Schema mismatch** — on opening each entry, the first data row's field count is
  compared against `{typeName}.json`. Returns an error before any row is appended
  (descriptive: entry name, expected count, actual count). This catches
  stale CSV files or blobs left over after a type definition change.

---

## 4. Entry Selection and Time-Range Pruning

### Naming convention

Gobbler names each CSV entry (file or blob) after the timestamp of its **first item**:

```
2024-01-15_13-22-07.123_logs.csv
2024-01-15_13-35-41.009_logs.csv
```

The `entry_timestamp` is the **lower bound** of item timestamps in that entry. Items are written in strictly increasing timestamp order within each entry, so ordering by name is reliable.

### Entry selection rule

For a query window `[T_start, T_end]`, sort all entries in the type's bucket by `entry_timestamp` ascending:

- **First entry (N)**: last entry where `entry_timestamp <= T_start` (the entry that was "active" at `T_start`, which may contain rows ≥ `T_start`); if all entries start after `T_start`, N is the first entry overall
- **Last entry (M)**: last entry where `entry_timestamp <= T_end`; if all entries start after `T_end`, the selection is empty
- **Read**: entries N through M inclusive

Row-level filtering within the boundary entries:

| Entry | Rule |
|---|---|
| Entry N | Skip rows where `timestamp < T_start` |
| Entries N+1 … M-1 | All rows pass — no per-row check needed |
| Entry M | Skip rows where `timestamp > T_end` |

Entry selection is pure I/O optimisation. The `FilterOp` predicate is still the source of truth for correctness.

**Owner**: `source/pruning.go`. Both `FileTableReader` and `BlobTableReader` apply this rule; the planner extracts `[T_start, T_end]` from the `TimeWindow` AST node and passes it into `SourceOp` at construction time.

### Time window forms

Every source requires an explicit time window — it is part of the `Source` syntax and a parse error to omit it. This prevents accidental full-table scans.

| Form | Example | Meaning |
|---|---|---|
| Relative lookback | `Logs(last 24h)` | All entries from `now() − 24h` onward |
| Absolute range | `Logs(datetime(2026-01-15 09:00:00) .. datetime(2026-01-15 18:00:00))` | Entries overlapping the given range |
| Full scan | `Logs(*)` | All entries — no time filter |

**`DatetimeLit` format** — Gobbler's native format: `YYYY-MM-DD HH:MM:SS.mmm` (space separator, no `T`, no timezone designator). Time part and milliseconds are optional:

```
datetime(2026-01-15)
datetime(2026-01-15 09:30:00)
datetime(2026-01-15 09:30:00.000)
```

**`last <duration>`** — the planner computes `T_start = now() − duration` at query start time. No upper-bound entry pruning is applied for this form.

**`*` (full scan)** — all entries in the type's bucket are read. Requiring the literal `*` rather than allowing a bare source name makes the cost intentionally visible in the query text.

In blob mode every matching blob must be opened and downloaded. Use narrow windows for large types.

**Phase 2**: once segment metadata (min/max statistics per segment) is available, the planner will use the time window to prune segments before opening any files or blobs, without changing query syntax.
