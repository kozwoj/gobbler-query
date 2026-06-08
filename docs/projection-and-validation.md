# Projection Semantics and Query Validation

*Design decisions · Validation pass scope · InferAndValidate*

---

## 1. GQL projection is not relational projection

In relational algebra, `project` only selects a subset of columns. In GQL
(following KQL), `project` does more:

- Selects which columns survive into the next stage
- Renames columns
- Reorders columns
- Computes new columns via per-row scalar arithmetic
- Drops everything not listed

### Valid `ProjectItem` right-hand sides

Three forms make semantic sense:

| Form | Example | Meaning |
|---|---|---|
| Bare `FieldRef` | `project timestamp` | Keep column as-is |
| `alias = FieldRef` | `project ts = timestamp` | Rename column |
| `alias = BinaryExpr` over fields | `project dur = endTime - startTime` | Compute new column per row |

Pure-literal right sides (`project n = 42`) are grammatically legal — the
grammar is intentionally permissive — but produce a constant column (same value
for every row) with no analytical value. The validation pass rejects them.

---

## 2. Grammar stays permissive; semantics enforced by a validation pass

**Decision**: Do not tighten the grammar. A separate semantic validation pass
over the logical plan enforces all column-reference and type-compatibility
constraints before physical plan construction.

Reasons:
- Grammar complexity stays low and matches what KQL allows.
- Validation is independent of parsing and produces precise error messages.
- Runtime type errors (discovered on first batch) give poor UX for a query tool.

---

## 3. Arithmetic type rules for `BinaryExpr`

Applicable in `project` expressions and in scalar operands of `where` comparisons.

| Left type | Op | Right type | Result type |
|---|---|---|---|
| `int32` / `int64` | `+ - * /` | `int32` / `int64` | `int64` |
| `float64` | `+ - * /` | `float64` | `float64` |
| `int64` | `+ - * /` | `float64` | `float64` (promoted) |
| `time.Time` | `-` | `time.Time` | `time.Duration` |
| `time.Time` | `+ -` | `time.Duration` | `time.Time` |
| `time.Duration` | `+ -` | `time.Duration` | `time.Duration` |
| `time.Duration` | `* /` | `int64` / `float64` | `time.Duration` |

All other combinations (e.g. `string + int`, `bool - float`) are type errors
reported by the validation pass.

`UnaryMinusExpr` is valid only on `int64`, `float64`, and `time.Duration`.

---

## 4. Full validation scope — by stage

The validation pass threads the current schema from stage to stage. Each stage
is checked against the schema its input produces.

### `where`

| Check | Example error |
|---|---|
| All `FieldRef`s in `BoolExpr` exist in current schema | `where foo >= 1` — `foo` not found |
| Comparison operand types are compatible | `where region >= 400` — string vs int |
| `isnull` / `isnotnull` / `isempty` field exists | `where isempty(foo)` — `foo` not found |
| `in` list element types match the field type | `where code in ("a", "b")` — `code` is int |
| `between` bound types match the field type | `where ms between ("a" .. "b")` — `ms` is int |

### `project`

| Check | Example error |
|---|---|
| Bare `FieldRef` exists in current schema | `project foo` — `foo` not found |
| `alias = FieldRef` — source field exists | `project x = foo` — `foo` not found |
| `alias = BinaryExpr` — operand types compatible | `project x = region - startTime` — string vs datetime |
| Right side must not be a pure literal | `project x = 42` — no field reference in expression |

### `summarize`

| Check | Example error |
|---|---|
| `sum` / `avg` / `min` / `max` argument is numeric or datetime/duration | `sum(region)` — `region` is string |
| `dcount` argument field exists | `dcount(foo)` — `foo` not found |
| Group-by `FieldRef`s exist in current schema | `by foo` — `foo` not found |

### `join`

| Check | Example error |
|---|---|
| Same-name shorthand key exists on **both** sides | `on userId` — `userId` absent from right sub-query output |
| Explicit `$left.col` exists in left schema | `$left.id` — `id` not in left schema |
| Explicit `$right.col` exists in right schema | `$right.orderId` — `orderId` not in right schema |
| Join key types are compatible | `$left.id == $right.name` — int vs string |

### `sort by`

| Check | Example error |
|---|---|
| All `FieldRef`s exist in current schema | `sort by foo` — `foo` not found |

### `take` / `count`

No column references — nothing to validate.

---

## 5. InferAndValidate — design

A single combined pass: schema inference + validation in one traversal.

```go
// InferAndValidate walks the logical plan, threads the schema stage by stage,
// validates all column references and type constraints, and returns the
// final output schema of the plan root.
//
// Returns an error with a precise message on the first violation found.
func InferAndValidate(node LogicalNode, cat catalog.Catalog) ([]batch.ColumnMeta, error)
```

**Lives in `query/logical`** — it operates over `LogicalNode` values and the
catalog, both of which are already imported by that package.

**Schema threading rules:**

| Node | Input schema | Output schema |
|---|---|---|
| `LogicalSource` | — | Catalog schema for the type |
| `LogicalWhere` | Input schema | Same as input (filter changes no columns) |
| `LogicalProject` | Input schema | One `ColumnMeta` per `ProjectItem`; `Origin = ""` for computed columns |
| `LogicalSummarize` | Input schema | One entry per `AggItem` output + one per group-by field |
| `LogicalJoin` | Left schema + right schema | Left ∪ right, join key deduplicated (left copy kept) |
| `LogicalSort` | Input schema | Same as input |
| `LogicalTake` | Input schema | Same as input |
| `LogicalCount` | — | `[{Name: "count_", Origin: ""}]` |
