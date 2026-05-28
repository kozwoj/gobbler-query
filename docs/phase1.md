Input grammar is a *clean, well‑scoped, pipeline‑based KQL subset*. It’s perfect for a Phase‑1 execution‑engine prototype because:

- It’s **pipeline‑oriented** (Source → Stages)  
- All operators are **unary** except join  
- All operators operate on **batches**  
- There’s no recursion, no subqueries except inside join  
- There’s no dynamic typing, no JSON, no mv‑expand  
- Aggregations are simple and fixed‑arity  
- Joins are inner‑only and equality‑based  

This is exactly the kind of language that maps well onto a **CSV → Batch → Operator pipeline**.

---

# **PHASE 1 ARCHITECTURE OUTLINE**  
### *CSV → Batches → Operators → Results*  
### *No segments, no storage layer, no ingestion pipeline yet.*

This is the fastest path to a working engine.

---

## **1. High‑Level Architecture**
```
Query Parser → Logical Plan → Physical Plan → Execution Pipeline
```

Execution pipeline consumes **batches** produced directly from CSV files.

### Components you build in Phase 1:

1. **Lexer + Parser** (based on your grammar)
2. **Logical plan builder**
3. **Physical plan builder**
4. **CSVBatchReader** (your “pseudo‑segment reader”)
5. **Operators**:
   - Filter
   - Project
   - Summarize (hash aggregate)
   - Join (hash join)
   - Sort (local)
   - Take
   - Count (sugar for summarize count())
6. **Batch model** (4096 rows, typed column vectors)
7. **Execution engine** (pull‑based or push‑based)
8. **StorageRoot** (resolves type names to data paths before physical plan construction)

This gives you a fully working analytical engine.

---

## **2. Source: CSVBatchReader**
Each CSV file is treated as a **simple segment**.

### Responsibilities:
- Open CSV file
- Read schema from `type.json` in the same directory/container
- Read rows in chunks of 4096
- Convert each column to typed slices
- Produce a `Batch`

### Output:
```
Batch {
    Length: N
    Columns: []ColumnVector
    Sel: nil
}
```

### Why this works:
Your grammar always starts with:

```
Query ::= Source ( "|" Stage )*
```

So `Source` → CSVBatchReader, after the source name is resolved to a path by the **StorageRoot**.

---

## **2a. Storage Root**

Before the engine can execute any query, it must be given a **storage root** — the context that maps type names to data locations.

A query source is always a **type name with a required time window**, not a path:

```
Logs(last 24h)              ← type name + required time window
| where statusCode >= 400
```

The engine resolves `Logs` to an actual location using the storage root:

| Mode | Root | Type name resolves to |
|------|------|-----------------------|
| File | Root directory path | `<rootDir>/Logs/` — the subdirectory gobbler writes for that type |
| Blob | Storage account name + account key | Azure container named `Logs` in the given account |

### `StorageRoot` interface (`query/catalog/`):

```go
package catalog

// StorageRoot resolves type names to data source paths.
// The engine must be initialized with one before executing any query.
type StorageRoot interface {
    Resolve(typeName string) (path string, err error)
}
```

### Phase 1 implementation (file mode):

```go
// FileRoot points to a local directory that gobbler has written output into.
type FileRoot struct {
    Dir string // e.g. "C:\\temp\\gobbler-output"
}

func (r *FileRoot) Resolve(typeName string) (string, error) {
    return filepath.Join(r.Dir, typeName), nil
}
```

### Phase 1 implementation (blob mode):

```go
// BlobRoot points to an Azure Blob Storage account that gobbler has written output into.
// Both AccountName and AccountKey are required to authenticate against the storage account.
type BlobRoot struct {
    AccountName string // e.g. "mystorageaccount"
    AccountKey  string // base64-encoded storage account key
}

func (r *BlobRoot) Resolve(typeName string) (string, error) {
    // Returns a connection string or structured identifier used by the blob source reader.
    // The type name maps to a container of the same name within the account.
    return fmt.Sprintf("https://%s.blob.core.windows.net/%s", r.AccountName, typeName), nil
}
```

The account key is kept in `BlobRoot` rather than embedded in the resolved URL so that credentials are never serialised into the logical or physical plan. The physical plan builder receives the root and calls `Resolve` when constructing a `SourceOp`.

---

## **3. Logical Plan Nodes (one per grammar stage)**

### For example:

- `LogicalWhere{Expr}`
- `LogicalProject{Items}`
- `LogicalSummarize{Aggs, GroupBy}`
- `LogicalJoin{Left, Right, Keys}`
- `LogicalSort{SortItems}`
- `LogicalTake{N}`
- `LogicalCount{}`

These nodes are created directly from the grammar.

---

## **4. Physical Plan Nodes (vectorized operators)**

Each logical node maps to a physical operator:

| Logical | Physical |
|---------|----------|
| Where | FilterOperator |
| Project | ProjectOperator |
| Summarize | HashAggregateOperator |
| Join | HashJoinOperator |
| Sort | SortOperator |
| Take | LimitOperator |
| Count | AggregateOperator(count) |

The physical plan is a **tree of operators**, each consuming batches.

---

## **5. Batch Model (core of Phase 1)**

### Batch:
```go
type Batch struct {
    Length  int
    Columns []ColumnVector
    Sel     []int      // optional selection vector
    Bitmap  []uint64   // optional bitmap
}
```

### ColumnVector:
Typed slices:

```go
type Int32Vector struct {
    Values []int32
    Nulls  []uint64
}
```

Strings use dictionary codes or raw strings (Phase 1 can use raw strings).

---

## **6. Operator Implementations**

### **6.1 FilterOperator**
Evaluates `BoolExpr` row‑by‑row, produces a selection vector.

### **6.2 ProjectOperator**
Evaluates scalar expressions, produces new columns.

### **6.3 HashAggregateOperator**
Maintains:

```
map[GroupKey]AggState
```

Consumes many batches, produces one final batch.

### **6.4 HashJoinOperator**
Build side:
```
map[JoinKey][]RowID
```

Probe side:
- For each row in probe batch, lookup matches
- Produce joined batch

### **6.5 SortOperator**
Phase 1: local sort only (sort entire input after collecting all batches).

### **6.6 TakeOperator**
Pass through batches until N rows emitted.

### **6.7 CountOperator**
Sugar for `summarize count()`.

---

## **7. Execution Engine**
Two options:

### **Pull‑based (Volcano style)**
Each operator implements:

```go
Next() *Batch
```

### **Push‑based (DataFusion style)**
Operators push batches downstream.

**Phase 1: pull‑based is simpler.**

---

## **8. Query Flow in Phase 1**
Example:

```
Logs
| where statusCode >= 400
| project timestamp, region
| summarize count() by region
```

Execution:

1. CSVBatchReader produces Batch(4096 rows)
2. FilterOperator applies predicate
3. ProjectOperator selects/renames columns
4. HashAggregateOperator updates state
5. Repeat until CSV exhausted
6. HashAggregateOperator emits final batch

---

## **9. What Phase 1 *does not* include**
- Segments  
- Columnar storage  
- Compression  
- Dictionary encoding  
- Min/max statistics  
- Pruning  
- Parallel scanning  
- Time‑partitioning  
- Retention policies  

All of that comes in **Phase 2**.

---

## **10. Why this Phase 1 architecture is perfect for your grammar**
Your grammar is:

- **pipeline‑based**  
- **batch‑friendly**  
- **operator‑driven**  
- **non‑recursive**  
- **no subqueries except join**  
- **dynamic type is opaque** (raw string storage; only `==`, `!=`, `isnull`, `isnotnull` permitted)  
- **no nested data**  

It maps *directly* onto a vectorized execution engine.

---

## Steps of Phase 1:

- **A. The full Phase 1 package layout (Go modules + directories)**  
- **B. The CSVBatchReader implementation sketch**  
- **C. The operator interface definitions**  
- **D. The logical → physical plan mapping**  
- **E. A minimal working example query execution**  

This layout is clean, idiomatic, and sets you up perfectly for Phase 2.

---

# **A. Phase 1 Directory Layout (Go)**  
### *Module: `gobbler-query`*  
### *Top-level directories: `query/` and later `segmenting/`*

```
gobbler-query/
│
├── cmd/
│   └── gobbler-cli/
│       └── main.go
│
├── query/
│   ├── lexer/
│   ├── parser/
│   ├── ast/
│   ├── logical/
│   ├── physical/
│   ├── exec/
│   ├── batch/
│   ├── source/
│   ├── expr/
│   ├── planner/
│   └── catalog/
│
└── api/
```

Now let’s break it down with your naming conventions in mind.

---

## **1. `cmd/gobbler-cli/`**
A tiny CLI that:

- reads a query string  
- parses it  
- builds a plan  
- executes it  
- prints results  

This is your development driver.

```
cmd/gobbler-cli/main.go
```

---

## **2. `query/lexer/`**
Implements the tokenizer for your grammar.

Files:

```
lexer.go
tokens.go
```

Outputs a stream of tokens for the parser.

---

## **3. `query/parser/`**
Implements the grammar you provided.

Files:

```
parser.go
rules.go
errors.go
```

Outputs an **AST**.

---

## **4. `query/ast/`**
Defines AST node types:

```
query.go
stage.go
expr.go
join.go
agg.go
```

These map 1:1 to your grammar productions.

---

## **5. `query/logical/`**
Transforms AST → Logical Plan.

Files:

```
logical_plan.go
logical_nodes.go
```

Logical nodes:

- `LogicalSource`
- `LogicalWhere`
- `LogicalProject`
- `LogicalSummarize`
- `LogicalJoin`
- `LogicalSort`
- `LogicalTake`
- `LogicalCount`

This layer is grammar‑aware but execution‑agnostic.

In Phase 1 the logical plan is a direct structural mirror of the AST — no optimization passes are applied. The split is kept intentionally: when Phase 2 introduces columnar segment storage, optimization passes (predicate pushdown, partition pruning, join reordering) will be inserted between the logical and physical layers without touching the parser, the AST, or the operator implementations.

---

## **6. `query/physical/`**
Maps logical nodes → physical operators.

Files:

```
physical_plan.go
operators.go
```

Physical operators:

- `FilterOp`
- `ProjectOp`
- `HashAggregateOp`
- `HashJoinOp`
- `SortOp`
- `LimitOp`
- `CountOp`

This layer is batch‑aware.

---

## **7. `query/exec/`**
Runs the operator pipeline and collects results.

Files:

```
executor.go
```

Responsibilities:

- receive a fully-wired root operator from the planner  
- call `Next()` in a loop until `io.EOF`  
- collect batches into a final result  
- return the result to the caller  

---

## **8. `query/batch/`**
Your core vector model.

Files:

```
batch.go
column_vector.go
types.go
selection.go
```

Defines:

- `Batch`
- `ColumnVector` interface
- typed vectors (`Int32Vector`, `StringVector`, etc.)
- selection vectors
- bitmaps

This is the heart of Phase 1.

---

## **9. `query/source/`**
Data source abstraction: reads stored data and produces batches.

Files:

```
reader.go        // BatchReader interface
file_source.go   // FileSource: reads local CSV files (file mode)
blob_source.go   // BlobSource: reads CSV blobs from Azure Blob Storage (blob mode)
pruning.go       // source selection by time range (shared by file and blob)
schema.go        // reads type.json from local directory or blob container
decode.go        // typed column decoding
```

Defines:

- `BatchReader` interface — the contract the planner and exec layer use; identical for file and blob mode  
- `FileSource` — file mode: lists CSV files in a local directory by time window, reads schema from `type.json`, produces batches  
- `BlobSource` — blob mode: lists CSV blobs in an Azure container by time window, streams each blob, reads schema from `type.json` in the container, produces batches  

The CSV parsing layer (`CSVBatchReader`) is shared — `FileSource` opens local files and `BlobSource` streams blobs, but both hand the resulting `io.Reader` to the same decoder.

---

## **10. `query/expr/`**
Expression evaluation engine.

Files:

```
scalar_eval.go
bool_eval.go
compare.go
agg_funcs.go
```

Evaluates:

- scalar expressions  
- boolean expressions  
- comparison operators  
- aggregation functions  

Used by Filter, Project, Aggregate.

**String operators** (all case-insensitive, Unicode-aware via `strings.EqualFold` / `unicode.ToLower`):

| Operator | Semantics |
|---|---|
| `=~` | case-insensitive equality |
| `contains` | substring match |
| `startswith` | prefix match |
| `endswith` | suffix match |

All four apply only to `TypeString` (and `TypeDynamic` is excluded — only `==`/`!=`/`isnull`/`isnotnull` apply there). Using a string operator against a numeric or datetime column is a compile-time plan error.

**Dynamic column restrictions**: columns with `TypeDynamic` are stored as opaque strings. The evaluator enforces that only `==`, `!=`, `isnull`, and `isnotnull` are used against them; any other operator (`<`, `>`, arithmetic, string functions, etc.) returns a compile-time plan error. A future phase may add type-specific operators (e.g. JSON path extraction) that parse the raw string at evaluation time.

---

## **11. `query/planner/`**
The glue between parser and execution.

Files:

```
planner.go
```

Responsibilities:

- build logical plan from AST  
- extract time range from the source time window and pass it to the source  
- build physical plan (lower logical nodes → operators)  
- choose operator implementations (FileSource vs BlobSource based on the StorageRoot type)  

Source selection and timestamp filtering live in `query/source/`, not here.

---

## **12. `query/catalog/`**
Resolves type names to data paths.

Files:

```
catalog.go
```

Defines:

- `StorageRoot` interface  
- `FileRoot` — file mode: resolves a type name to a local subdirectory  
- `BlobRoot` — blob mode: resolves a type name to an Azure container; holds account name and account key

---

## **13. `api/`**
Public API for embedding Gobbler Query in other apps.

Files:

```
query.go
result.go
engine.go
```

Provides:

- `Execute(query string) (Result, error)`
- stable types for external callers

Everything else stays in `query/`.

---

## **Why this layout is ideal for your goals**
Because it:

- isolates the query engine  
- isolates the parser  
- isolates the data source reader (file or blob)  
- keeps storage out of the picture  
- allows Phase 2 to drop in a SegmentReader without touching operators  
- keeps the public API clean  
- mirrors the architecture of DuckDB, DataFusion, and Velox  

You can build this incrementally and test each layer independently.

---

# **B. CSVBatchReader — Phase 1 Implementation Sketch**

## **1. Purpose**
The CSVBatchReader is your Phase‑1 “pseudo‑segment reader.”

It:

- opens a CSV file  
- reads schema from `type.json` in the same directory  
- reads rows in chunks (e.g., 4096)  
- converts each column into typed vectors  
- produces `Batch` objects for the execution pipeline  

It is the *only* data source in Phase 1.

---

## **2. Public API**

```go
package csvreader

type Reader struct {
    r        *csv.Reader
    schema   *Schema
    batchSize int
}

func NewReader(path string, batchSize int) (*Reader, error)

func (r *Reader) Schema() *Schema

func (r *Reader) NextBatch() (*batch.Batch, error)
```

### Notes:
- `Schema()` returns the schema read from `type.json` (column names + types).
- `NextBatch()` returns `nil, io.EOF` when done.
- `batch.Batch` is from `query/batch`.

---

## **3. Schema Representation**

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

### Phase‑1 schema source:
The schema is read from `type.json` in the same directory as the CSV files. Gobbler writes this file when the directory/container is created, so it is always present and authoritative. Column names, order, and types are taken directly from it — no inference needed.

---

## **4. Reader Internal Structure**

```go
type Reader struct {
    file      *os.File
    r         *csv.Reader
    schema    *Schema
    batchSize int

    // scratch buffers reused across batches
    colBuilders []columnBuilder
}
```

### `columnBuilder` is a small helper:

```go
type columnBuilder interface {
    Append(raw string)
    Build(n int) batch.ColumnVector
    Reset()
}
```

Concrete builders:

- `int32Builder`
- `float64Builder`
- `stringBuilder`
- `boolBuilder`
- `datetimeBuilder`
- `timespanBuilder` — parses the cell with `time.ParseDuration`; stores `time.Duration`
- `dynamicBuilder` — identical to `stringBuilder`; Gobbler writes dynamic fields as CSV-quoted JSON (`"{"k":"v"}"` with internal quotes doubled), so Go's `csv.Reader` automatically unquotes the field before `Append` is called — the builder stores a plain JSON string

Each builder accumulates values for one batch.

---

## **5. NextBatch() Implementation Sketch**

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

    // Build column vectors
    cols := make([]batch.ColumnVector, len(r.schema.Columns))
    for i := range cols {
        cols[i] = r.colBuilders[i].Build(rows)
        r.colBuilders[i].Reset()
    }

    return &batch.Batch{
        Length:  rows,
        Columns: cols,
        Sel:     nil,
        Bitmap:  nil,
    }, nil
}
```

### Key points:
- No allocations inside the loop  
- Builders reuse memory  
- Batch is created only once per chunk  
- No selection vector yet (operators add it later)  

---

## **6. Column Builders (typed decoding)**

### Example: `int32Builder`

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
    return &batch.Int32Vector{
        Values: b.values[:n],
        Nulls:  b.nulls,
    }
}

func (b *int32Builder) Reset() {
    b.idx = 0
}
```

### String builder (Phase 1: raw strings)

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

### Phase 2 will replace this with dictionary encoding.

---

## **7. Memory Reuse Strategy**
Each builder allocates:

- `values` slice of size `batchSize`
- `nulls` bitmap of size `batchSize/64`

These are reused for every batch.

This keeps GC pressure extremely low.

---

## **8. Error Handling**
- **Empty cell → null for all types.** Gobbler writes `""` (empty CSV field) for any optional column whose value was absent at write time, regardless of column type. The reader must treat an empty cell as null (set the null bit in the column vector) rather than attempting to parse it.
- Malformed numbers (non-empty, non-parseable) → treat as null (Phase 1 simplicity)
- CSV parse errors → propagate

**Schema mismatch detection (fail early):**  
Gobbler CSV files have no header row — column order is defined solely by `type.json`. `NewReader` reads the first data row immediately on open and counts its fields. If the field count does not match the number of columns declared in `type.json`, it returns an error before any row is processed. This catches the case where a DevOps change altered the type definition but old CSV files in the same directory still carry the previous column layout. The query fails at plan execution start with a descriptive error (file path, expected column count, actual field count) rather than silently producing wrong data.

A stricter future option: treat type-cast failures (e.g. a column declared `int32` in `type.json` but containing a non-numeric value) as a hard error rather than null. This would surface schema drift that a field-count check alone cannot catch.

---

## **9. File Selection / Time-Range Pruning (Phase 1)**

Gobbler names each CSV file after the timestamp of its **first item**:

```
2024-01-15_13-22-07.123_logs.csv
2024-01-15_13-35-41.009_logs.csv
...
```

The `file_timestamp` is the **lower bound** of item timestamps in that file. Items are written in increasing order of timestamp, so file order is reliable.

**Selection rule for a query window `[T_start, T_end]`:**

Sort all files in the type's directory by `file_timestamp` ascending. Then:

- **First file (N)**: first file where `file_timestamp >= T_start`
- **Last file (M)**: last file where `file_timestamp <= T_end` — the file just before the first one with `file_timestamp > T_end`; any items with `timestamp == T_end` live in this file
- **Read**: files N through M inclusive; skip everything before N and after M

Within the selected range, the row-level `where` filter still applies:

- Beginning of file N: skip rows where `timestamp < T_start`
- End of file M: skip rows where `timestamp > T_end`
- Files N+1 through M-1: all rows pass the time bounds (no skipping needed)

**Correctness guarantee**: file selection is I/O optimisation only. The `where` operator is always the source of truth.

**Owner**: `source/pruning.go`. `FileSource` applies this rule; the planner extracts `[T_start, T_end]` from the source time window and passes it in.

---

### Time window (required source modifier)

Every source requires an explicit time window — it is part of the `Source` syntax and a parse error to omit it. This prevents accidental full-table scans.

Three forms are supported:

| Form | Example | Meaning |
|------|---------|---------|
| Relative lookback | `Logs(last 24h)` | All files from `now() − 24h` onward |
| Absolute range | `Logs(datetime(2026-01-15 09:00:00) .. datetime(2026-01-15 18:00:00))` | Files overlapping the given range |
| Full scan | `Logs(*)` | All files — no time filter applied |

**`DatetimeLit` format**: Gobbler's native datetime format — `YYYY-MM-DD HH:MM:SS.mmm` (space separator, no `T`, no timezone designator). The time part and milliseconds are optional:
- `datetime(2026-01-15)` — day precision
- `datetime(2026-01-15 09:30:00)` — second precision
- `datetime(2026-01-15 09:30:00.000)` — millisecond precision

**`last <duration>`**: the planner computes `T_start = now() − duration` at query start time and selects files from the first one where `file_timestamp >= T_start` onward. No upper-bound file pruning is applied for this form.

**`*` (full scan)**: all files in the type's directory/container are read. Requiring the literal `*` rather than allowing a bare `Logs` makes the cost intentionally visible in the query text.

Cost reminder — in blob mode every matching blob must be opened and downloaded. Use narrow windows for large types.

**Phase 2 note**: once segment metadata (min/max statistics per segment) is available, the planner will use the time window to prune segments before opening any files or blobs — eliminating I/O for segments outside the window without changing query syntax.

---

## **10. Future‑Proofing for Phase 2**
When you add real segments:

- `SegmentReader` will have the same API as `CSVBatchReader`  
- Operators will not change  
- Execution engine will not change  
- Planner will choose between CSV or SegmentReader  

This is why the design is correct.

---

## **Summary**
The CSVBatchReader:

- is simple  
- is fast  
- produces typed batches  
- integrates cleanly with your operators  
- is fully compatible with Phase 2  
- lets you build the execution engine first  

This is exactly how DuckDB and DataFusion bootstrap their engines.

---

# **C. Operator Interface Definitions**

## **1. The Core Operator Interface**
Every operator implements a simple pull‑based interface:

```go
package physical

import "gobbler-query/query/batch"

type Operator interface {
    Next() (*batch.Batch, error)
    Close() error
}
```

### Why this is perfect:
- **Pull‑based**: downstream operator asks upstream for the next batch  
- **Composable**: operators chain naturally  
- **Simple**: only two methods  
- **Future‑proof**: works with CSVBatchReader and SegmentReader  

This is the same model used by:
- DuckDB  
- DataFusion  
- Velox  
- Kusto (internally)  

---

## **2. The Source Operator (CSVBatchReader wrapper)**

```go
type SourceOp struct {
    reader *csvreader.Reader
}

func (op *SourceOp) Next() (*batch.Batch, error) {
    return op.reader.NextBatch()
}

func (op *SourceOp) Close() error {
    return nil
}
```

This is the root of every plan.

---

## **3. Filter Operator**

```go
type FilterOp struct {
    input Operator
    expr  expr.BoolExprEvaluator
}
```

`Next()`:

- pull batch from input  
- evaluate boolean expression  
- produce selection vector  
- return filtered batch  

---

## **4. Project Operator**

```go
type ProjectOp struct {
    input Operator
    items []expr.ScalarExprEvaluator
    names []string
}
```

`Next()`:

- pull batch  
- evaluate each scalar expression  
- build new column vectors  

---

## **5. Hash Aggregate Operator**

```go
type HashAggregateOp struct {
    input    Operator
    aggs     []expr.AggFunc
    groupBy  []expr.FieldRef
    hash     map[GroupKey]*AggState
    emitted  bool
}
```

`Next()`:

- if not finished: consume all input batches, update hash table  
- then emit one final batch  
- then return `nil, io.EOF`  

---

## **6. Hash Join Operator**

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

`Next()`:

- build hash table from left side  
- probe with right side batches  
- emit joined batches  

---

## **7. Sort Operator**

Phase‑1: simple in‑memory sort.

```go
type SortOp struct {
    input Operator
    items []SortItem
    done  bool
    rows  *materializedRows
}
```

`Next()`:

- materialize all rows  
- sort  
- emit sorted batches  

---

## **8. Limit (Take) Operator**

```go
type LimitOp struct {
    input Operator
    remaining int
}
```

`Next()`:

- pass through batches until `remaining == 0`  

---

## **9. Count Operator**

Sugar for:

```
summarize count()
```

Implemented as:

```go
type CountOp struct {
    input Operator
    done  bool
    count int64
}
```

---

## **10. Operator Construction (Physical Plan Builder)**

Each logical node maps to a physical operator:

```go
func buildPhysicalPlan(logical LogicalNode, root catalog.StorageRoot) (Operator, error) {
    switch n := logical.(type) {
    case *LogicalWhere:
        child := buildPhysicalPlan(n.Input)
        return &FilterOp{input: child, expr: compileBoolExpr(n.Expr)}, nil

    case *LogicalProject:
        child := buildPhysicalPlan(n.Input)
        return &ProjectOp{input: child, items: compileProject(n.Items)}, nil

    case *LogicalSummarize:
        child := buildPhysicalPlan(n.Input)
        return newHashAggregateOp(child, n.Aggs, n.GroupBy), nil

    case *LogicalJoin:
        left := buildPhysicalPlan(n.Left)
        right := buildPhysicalPlan(n.Right)
        return newHashJoinOp(left, right, n.Keys), nil

    case *LogicalSort:
        child := buildPhysicalPlan(n.Input)
        return newSortOp(child, n.SortItems), nil

    case *LogicalTake:
        child := buildPhysicalPlan(n.Input)
        return &LimitOp{input: child, remaining: n.N}, nil

    case *LogicalCount:
        child := buildPhysicalPlan(n.Input, root)
        return &CountOp{input: child}, nil

    case *LogicalSource:
        path, err := root.Resolve(n.TypeName)
        if err != nil {
            return nil, err
        }
        return newSourceOp(path), nil
    }
}
```

This is the heart of your execution engine.

---

## **11. Why this operator interface is perfect for Phase 1**
Because it:

- is minimal  
- is composable  
- is easy to test  
- supports all your grammar stages  
- works with CSVBatchReader  
- will work with SegmentReader in Phase 2  
- matches the architecture of real analytical engines  

You can implement each operator independently and test them with synthetic batches.

---

# **D. Logical → Physical Plan Mapping** - *AST → Logical Plan → Physical Plan (Operators)*

> **Why keep the logical/physical split in Phase 1?**  
> In Phase 1 the logical plan is a direct mirror of the AST with no optimization applied. The two-layer design is kept because Phase 2 columnar segment storage will require optimization passes (predicate pushdown, partition pruning, join reordering) that slot cleanly between logical and physical. Keeping the split now means Phase 2 adds passes without restructuring the parser, the AST, or any operator.

Your pipeline grammar:

```
Query ::= Source ( "|" Stage )*
```

maps beautifully to a left‑deep operator tree.

Below is the full mapping, step by step.

---

## **1. AST → Logical Plan**

The AST mirrors the grammar.  
The logical plan mirrors the *semantics*.

### Example AST (simplified):

```
Query
  Source: "Logs"
  Stages:
    WhereStage(statusCode >= 400)
    ProjectStage(timestamp, region)
    SummarizeStage(count() by region)
```

### Logical plan becomes:

```
LogicalSummarize
  Input:
    LogicalProject
      Input:
        LogicalWhere
          Input:
            LogicalSource("Logs")
```

### Logical nodes (Phase 1):

- `LogicalSource`
- `LogicalWhere`
- `LogicalProject`
- `LogicalSummarize`
- `LogicalJoin`
- `LogicalSort`
- `LogicalTake`
- `LogicalCount`

Each stage wraps the previous one.

---

## **2. Logical → Physical Mapping Table**

This is the core of D.

| Logical Node | Physical Operator | Notes |
|--------------|------------------|-------|
| `LogicalSource` | `SourceOp` | Wraps CSVBatchReader |
| `LogicalWhere` | `FilterOp` | Compiled BoolExpr |
| `LogicalProject` | `ProjectOp` | Compiled ScalarExprs |
| `LogicalSummarize` | `HashAggregateOp` | Hash table + AggFuncs |
| `LogicalJoin` | `HashJoinOp` | Build left, probe right |
| `LogicalSort` | `SortOp` | Phase‑1: full materialize |
| `LogicalTake` | `LimitOp` | Row‑count limiting |
| `LogicalCount` | `CountOp` | Sugar for summarize count() |

This mapping is deterministic and simple.

---

## **3. Physical Plan Construction (Pseudo‑Code)**

This is the heart of the planner:

```go
func buildPhysical(node LogicalNode, root catalog.StorageRoot) (physical.Operator, error) {
    switch n := node.(type) {

    case *LogicalSource:
        path, err := root.Resolve(n.TypeName)
        if err != nil {
            return nil, err
        }
        return newSourceOp(path), nil

    case *LogicalWhere:
        child, _ := buildPhysical(n.Input)
        pred := expr.CompileBoolExpr(n.Expr)
        return &physical.FilterOp{Input: child, Expr: pred}, nil

    case *LogicalProject:
        child, _ := buildPhysical(n.Input)
        items := expr.CompileProjectItems(n.Items)
        return &physical.ProjectOp{Input: child, Items: items}, nil

    case *LogicalSummarize:
        child, _ := buildPhysical(n.Input)
        aggs := expr.CompileAggFuncs(n.Aggs)
        group := expr.CompileGroupBy(n.GroupBy)
        return physical.NewHashAggregateOp(child, aggs, group), nil

    case *LogicalJoin:
        left, _ := buildPhysical(n.Left)
        right, _ := buildPhysical(n.Right)
        keys := expr.CompileJoinKeys(n.Keys)
        return physical.NewHashJoinOp(left, right, keys), nil

    case *LogicalSort:
        child, _ := buildPhysical(n.Input)
        items := expr.CompileSortItems(n.Items)
        return physical.NewSortOp(child, items), nil

    case *LogicalTake:
        child, _ := buildPhysical(n.Input)
        return &physical.LimitOp{Input: child, Remaining: n.N}, nil

    case *LogicalCount:
        child, _ := buildPhysical(n.Input)
        return &physical.CountOp{Input: child}, nil
    }

    return nil, fmt.Errorf("unknown logical node")
}
```

This is the entire lowering pipeline.

---

## **4. Example: Full End‑to‑End Mapping**

### Query:

```
Logs
| where statusCode >= 400
| project timestamp, region
| summarize count() by region
```

### AST → Logical:

```
LogicalSummarize
  Input:
    LogicalProject
      Input:
        LogicalWhere
          Input:
            LogicalSource("Logs")
```

### Logical → Physical:

```
HashAggregateOp
  Input:
    ProjectOp
      Input:
        FilterOp
          Input:
            SourceOp (CSVBatchReader)
```

### Execution pipeline:

```
CSV → Batch → Filter → Project → HashAggregate → Final Batch
```

Perfect.

---

## **5. Example: Join Mapping**

### Query:

```
Requests
| join (Users | project userId, tier) on userId
| summarize count() by tier
```

### Logical:

```
LogicalSummarize
  Input:
    LogicalJoin
      Left:  LogicalSource("Requests")
      Right: LogicalProject(LogicalSource("Users"))
      Keys:  userId
```

### Physical:

```
HashAggregateOp
  Input:
    HashJoinOp
      Left:  SourceOp(Requests)
      Right: ProjectOp(SourceOp(Users))
      Keys:  userId
```

---

## **6. Why this mapping is correct for your grammar**

Your grammar is:

- **pipeline‑based**  
- **stage‑oriented**  
- **non‑recursive**  
- **no subqueries except join**  
- **no window functions**  
- **dynamic type is opaque** (raw string storage; only `==`, `!=`, `isnull`, `isnotnull` permitted)  

This means:

- Logical plan is always a **left‑deep tree**  
- Physical plan is always a **left‑deep operator chain**  
- Join is the only binary operator  
- Everything else is unary  

This is the simplest possible analytical engine architecture — and it’s exactly what you want for Phase 1.

---

## **7. What this gives you**

- A clean compiler pipeline  
- A deterministic mapping from grammar → execution  
- A structure that matches DuckDB/DataFusion/Velox  
- A foundation that will not change in Phase 2  
- A clear separation of concerns  

This is the “spine” of your engine.

---

# **E: A Minimal Working Example of End‑to‑End Query Execution** 

*query string → parse → logical plan → physical plan → operator pipeline → execution → output*

Query: 
```
Logs
| where statusCode >= 400
| project timestamp, region
| take 3
```

This is exactly what your engine will do in Phase 1.

---

## **1. Query String**

```go
query := `
    Logs
    | where statusCode >= 400
    | project timestamp, region
    | take 3
`
```

---

## **2. Parse → AST**

```go
ast, err := parser.Parse(query)
```

AST (conceptually):

```
Query
  Source: "Logs"
  Stages:
    WhereStage(statusCode >= 400)
    ProjectStage(timestamp, region)
    TakeStage(3)
```

---

## **3. AST → Logical Plan**

```go
logicalPlan, err := planner.BuildLogical(ast)
```

Logical plan (tree):

```
LogicalTake(3)
  Input:
    LogicalProject(timestamp, region)
      Input:
        LogicalWhere(statusCode >= 400)
          Input:
            LogicalSource("Logs")
```

Left‑deep, exactly matching your grammar.

---

## **4. Logical → Physical Plan**

```go
physicalPlan, err := planner.BuildPhysical(logicalPlan)
```

Physical operator tree:

```
LimitOp(3)
  Input:
    ProjectOp(timestamp, region)
      Input:
        FilterOp(statusCode >= 400)
          Input:
            SourceOp(CSVBatchReader(root.Resolve("Logs")))
```

This is your execution pipeline.

---

## **5. Operator Chain (actual Go objects)**

```go
root := &catalog.FileRoot{Dir: "C:\\data\\gobbler-output"}
path, _ := root.Resolve("Logs")
source := &SourceOp{reader: csvreader.NewReader(path, 4096)}
filter := &FilterOp{Input: source, Expr: compiledPredicate}
project := &ProjectOp{Input: filter, Items: compiledProjectItems}
limit := &LimitOp{Input: project, Remaining: 3}
```

`limit` is the root operator.

---

## **6. Execution Loop**

```go
op := limit

for {
    batch, err := op.Next()
    if err == io.EOF {
        break
    }
    if err != nil {
        log.Fatal(err)
    }

    printBatch(batch)
}
```

Each call to `Next()` pulls a batch from upstream:

```
LimitOp → ProjectOp → FilterOp → SourceOp → CSVBatchReader
```

The CSV reader produces batches of 4096 rows.  
Filter applies predicate.  
Project selects columns.  
Limit stops after 3 rows.

---

## **7. Output (example)**

```
timestamp              region
--------------------------------
2024-01-01T12:00:01Z   eastus
2024-01-01T12:00:02Z   westus
2024-01-01T12:00:05Z   eastus
```

Execution stops after 3 rows.

---

## **What This Demonstrates**

This example shows that your engine already supports:

- CSV ingestion (Phase 1)
- Batch production
- Boolean expression evaluation
- Projection
- Limit
- Operator chaining
- Pull‑based execution
- Left‑deep pipeline
- Grammar → AST → Logical → Physical → Execution

This is a *real* analytical engine.

---

## **What You Can Build Next**

Now that you have:

- A: Directory layout  
- B: CSVBatchReader  
- C: Operator interface  
- D: Logical → Physical mapping  
- E: End‑to‑end execution  

You’re ready to implement:

### **F: Expression Compiler (scalar + boolean)**  
This is the next major piece.

Or, if you prefer:

### **G: Implement FilterOp in full detail**  
### **H: Implement ProjectOp**  
### **I: Implement HashAggregateOp**  
### **J: Implement HashJoinOp**

Just tell me which one you want to tackle next.