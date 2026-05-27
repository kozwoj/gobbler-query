# Gobbler Query — Design Overview

## Purpose

Gobbler Query is a the third element in the Gobbler ecosystem chain
1. Gobbler Client - is a SDK for instrumenting applications to send telemetry to Gobbler telemetry ingestion server
2. Gobbler - is a telemetry ingestion server pipeline accepting typed telemetry items, and storing them in time-stamped CSV files or Azure blobs
3. Gobbler Query - the third elements, is a query engines to analyze telemetry data stored by the Gobbler server

## Phased development

In the first phase of Gobbler Query module development the queries will execute directly against the CSV files. This phase will focus on implementing a KQL-inspired pipeline query language described in `docs\ql_grammar.ebnf` and `KQL-Cheat-Sheet.md`. 

In the second phase of Gobbler Query development the CSV files/blobs will be converted into Gobbler columnar segments described in `docs\segment-model.md`. This phase will focus on implementing columnar store representation of telemetry data. 

## Telemetry ingestion 

Gobbler Server produces a **sequence of time-stamped files** per item type, rotated either by age (Latency field in item definition) or by an explicit `POST /gobbler/pipeline/rotate` call.

### Local file mode

Files are written under `<outputDir>/<folder>/` with the naming convention:

```
YYYY-MM-DD_HH-MM-SS.mmm_<typeName>.csv
```

Example:
```
C:\gobbler-logs\alpha-folder\2026-05-13_09-30-00.000_alpha.csv
C:\gobbler-logs\alpha-folder\2026-05-13_10-30-00.000_alpha.csv
```

Each CSV file contains raw data rows (no header); the schema comes from the item definition.

### Azure Blob mode

One append blob per item type per rotation, following the same timestamp-based naming convention.
Blobs for the same item type are placed in the same Azure container following the folder naming convention. 

### Time-stamped ingestion

Gobbler automatically prepends a `timestamp` field (type `datetime`) as the **first field of every item**. The name `timestamp` is reserved and cannot be used by user-defined fields. This field is the designated ingestion key used by gobbler-query for:
- time-span file selection (which source CSV/blob files to ingest)
- segment catalog min/max values (for segment skipping during query execution). 

The CSV file/blob timestamp is the same as the timestamp of the first item stored in it. This means that a file/blob contains only items that are "older" than the file/blob.

## Storage Model

In the Phase 1 of development the storage either 
- CSV files stored in directories corresponding to telemetry items types, or
- Azure append blobs with CSV strings, stored in containers corresponding to item types. 

In Phase 2 of development the SCV files/blobs are converted to columnar segments
- Both local CSV and Azure Blob sources must be supported 
- See `docs/segment-model.md` for the full segment file format, catalog structure, encoding table, and incremental ingestion flow.

## Query Language

Inspired by the pipeline query language KQL if the Kusto Analytics Engine.

### Pipeline syntax

```
<ItemTypeName> | <stage> | <stage> | ...
```

Supported stages (see `docs/grammar.ebnf` for the formal grammar):

| Stage | Description |
|---|---|
| `where <expr>` | Filter rows; boolean expression with comparisons, `in`, `between`, null tests |
| `project <field>, ...` | Select, rename, or compute output columns |
| `summarize <agg> [by <field>, ...]` | Group-by with aggregation; no `by` = whole table as one group |
| `join (<query>) on <key>` | Inner join with a sub-query on matching key columns |
| `sort by <field> [asc\|desc], ...` | Order results |
| `take <n>` | Limit result count |
| `count` | Shorthand for `summarize count()` |

### Aggregation functions

All aggregation functions are used inside a `summarize` stage or the `count` shorthand stage.

| Function | Argument | Description |
|---|---|---|
| `count()` | none | Number of rows |
| `min(field)` | numeric or datetime | Minimum value |
| `max(field)` | numeric or datetime | Maximum value |
| `avg(field)` | numeric | Average value |
| `sum(field)` | numeric | Sum of values |
| `dcount(field)` | any | Distinct count of non-null values |

Examples:

```
// Whole-table count via shorthand stage
alpha-folder | where alpha.statusCode >= 400 | count

// Aggregation per group
alpha-folder | summarize count(), avg(alpha.durationMs) by alpha.statusCode

// Multiple aggregations with aliases
alpha-folder
| summarize total = count(), maxDur = max(alpha.durationMs) by alpha.region
```

Type constraints (enforced by semantic validator):
- `min`, `max` require a numeric or datetime-typed field
- `avg`, `sum` require a numeric field (not datetime)
- `dcount` accepts any field type
- `count` takes no argument

### Typed literals

| Literal form | Example |
|---|---|
| String | `"eastus"` (double-quoted) |
| Integer | `42` |
| Float | `3.14` |
| Boolean | `true` / `false` |
| Datetime | `datetime(2026-01-15)` or `datetime(2026-01-15 09:30:00)` |
| Timespan | `1h`, `7d`, `30m` — used only inside `ago(...)` |
| Dynamic | no literal form — column value is opaque at query level; only equality and null tests are valid |

String comparison operators: `==`, `!=`, `=~` (case-insensitive equals), `contains`, `startswith`, `endswith`.

### Boolean logic

Operator precedence (lowest to highest): `or` < `and` < `not` < primary comparison.
Primary comparisons: `==`, `!=`, `<`, `<=`, `>`, `>=`, `=~`, `contains`, `startswith`, `endswith`, `in`, `!in`, `between`, `isnull`, `isnotnull`, `isempty`.
Parenthesized grouping is supported at all levels.

## Phased development

In Pase 1 the batches will be created directly from the SCV files bases on the files and items timestamps. 

```
CSV/Blob reader
    └── →   Batches  (per item type, per column)
                          └── Query engine
                                  ├── Lexer
                                  ├── Parser  →  AST
                                  ├── Semantic validator  (schema from item definition)
                                  └── Executor  →  result set
```

In Phase 2 the Ingester will create columnar segments first, considerably reducing the date volume. Then, the batches for queries will be created from the segments, not CSV files. The query logic will remain the same. The front-end will change.  

## Open Questions (to resolve tomorrow)

1. ~~Exact set of aggregation stages and functions.~~ **Decided**: `count()`, `min(field)`, `max(field)`, `avg(field)`, `sum(field)` — first priority after basic filtering.
2. How `summarize` interacts with `where` (pre- vs post-aggregation filter — KQL uses `where` before and after).
3. ~~Whether `project` should support computed columns.~~ **Decided**: `project` supports `alias = ScalarExpr`, including arithmetic (`endTime - startTime`).
4. Columnar format: custom binary, Parquet-like, or something simpler (e.g. per-column gob-encoded slices)?
5. Incremental ingestion: how to append new CSV rows without re-ingesting everything?
6. Repo name confirmed as `gobbler-query`.
7. Time-span boundary semantics: is a file included if its timestamp is within `[start, end)`, or should it scan the last file before `start` too (in case it contains rows timestamped after `start`)? Row-level filtering by a timestamp field vs. file-selection by filename timestamp.

