Input grammar is a *clean, well-scoped, pipeline-based KQL subset*. It's perfect for a Phase-1 execution-engine prototype because:

- It's **pipeline-oriented** (Source -> Stages)
- All operators are **unary** except join
- All operators operate on **batches**
- There's no recursion, no subqueries except inside join
- There's no dynamic typing, no JSON, no mv-expand
- Aggregations are simple and fixed-arity
- Joins are inner-only and equality-based

This is exactly the kind of language that maps well onto a **CSV -> Batch -> Operator pipeline**.

---

# **PHASE 1 ARCHITECTURE OUTLINE**
### *CSV -> Batches -> Operators -> Results*
### *No segments, no storage layer, no ingestion pipeline yet.*

This is the fastest path to a working engine.

---

## **1. High-Level Architecture**

```
Query Parser -> Logical Plan -> Physical Plan -> Execution Pipeline
```

Execution pipeline consumes **batches** produced directly from CSV files.

### Components you build in Phase 1:

1. **Lexer + Parser** (based on your grammar)
2. **Logical plan builder**
3. **Physical plan builder**
4. **CSVBatchReader** (your "pseudo-segment reader")
5. **Operators**:
   - Filter
   - Project
   - Summarize (hash aggregate)
   - Join (hash join)
   - Sort (local)
   - Take
   - Count (sugar for summarize count())
6. **Batch model** (4096 rows, typed column vectors)
7. **Execution engine** (pull-based)
8. **StorageRoot** (resolves type names to data paths before physical plan construction)

This gives you a fully working analytical engine.

---

## **2. What Phase 1 does not include**

- Segments
- Columnar storage
- Compression
- Dictionary encoding
- Min/max statistics
- Pruning
- Parallel scanning
- Time-partitioning
- Retention policies

All of that comes in **Phase 2**.

---

## **3. Further reading**

| Document | Content |
|---|---|
| [execution-pipeline.md](execution-pipeline.md) | Batch model, operator catalogue, logical->physical plan, streaming vs blocking, end-to-end examples |
| [source-layer.md](source-layer.md) | StorageRoot, schema, CSVBatchReader, file selection and time-range pruning |

---

# **A. Phase 1 Directory Layout (Go)**
### *Module: `gobbler-query`*
### *Top-level directories: `query/` and later `segmenting/`*

```
gobbler-query/
|
+-- cmd/
|   +-- gobbler-cli/
|       +-- main.go
|
+-- query/
|   +-- lexer/
|   +-- parser/
|   +-- ast/
|   +-- logical/
|   +-- physical/
|   +-- exec/
|   +-- batch/
|   +-- source/
|   +-- expr/
|   +-- planner/
|   +-- catalog/
|
+-- api/
```

## Package reference

| Package | Purpose | Key files |
|---|---|---|
| `cmd/gobbler-cli/` | CLI development driver: parse, plan, execute, print | `main.go` |
| `query/lexer/` | Tokenizer | `lexer.go`, `tokens.go` |
| `query/parser/` | Grammar parser to AST | `parser.go`, `rules.go`, `errors.go` |
| `query/ast/` | AST node types (1:1 with grammar productions) | `query.go`, `stage.go`, `expr.go`, `join.go`, `agg.go` |
| `query/logical/` | AST to Logical Plan; grammar-aware, execution-agnostic; no optimization passes in Phase 1 | `logical_plan.go`, `logical_nodes.go` |
| `query/physical/` | Logical nodes to Physical operators; batch-aware | `physical_plan.go`, `operators.go` |
| `query/exec/` | Pull-based execution loop: calls Next() until io.EOF, collects result | `executor.go` |
| `query/batch/` | Core vector model: Batch, ColumnVector interface, typed vectors, selection vectors, bitmaps | `batch.go`, `column_vector.go`, `types.go`, `selection.go` |
| `query/source/` | Data source abstraction (file + blob mode); pruning.go owns file selection by time range; decode.go owns typed CSV decoding | `reader.go`, `file_source.go`, `blob_source.go`, `pruning.go`, `schema.go`, `decode.go` |
| `query/expr/` | Scalar and boolean expression evaluators and aggregation functions. String operators (=~, contains, startswith, endswith) are case-insensitive. dynamic columns accept only ==, !=, isnull, isnotnull -- any other operator is a compile-time plan error. | `scalar_eval.go`, `bool_eval.go`, `compare.go`, `agg_funcs.go` |
| `query/planner/` | Builds logical plan from AST, extracts time range, builds physical plan, selects FileSource vs BlobSource | `planner.go` |
| `query/catalog/` | StorageRoot interface; FileRoot (local directory); BlobRoot (Azure container + credentials) | `catalog.go` |
| `api/` | Public embedding API: Execute(query string) (Result, error) | `query.go`, `result.go`, `engine.go` |