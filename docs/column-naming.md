# Column Naming and Batch Metadata

*Join output schema · Column metadata · Qualified field references*

---

## Problem

After a join, the output batch contains columns from both sides. If both tables
have a column with the same name (e.g. `timestamp`), downstream stages need a
way to reference each one unambiguously.

> **Note**: Every Gobbler source table includes a `timestamp` column by design.
> This means `timestamp` is **always** ambiguous after any join. Qualification
> of at least `timestamp` is therefore mandatory in every post-join projection
> or filter that references it.

Example:

```
requests(last 1h)
| join (users(*)) on userId
| project requests.timestamp, users.timestamp, userId, tier
```

---

## Decision: Option B — columns keep original names; explicit qualification required

- The join operator does **not** rename columns. Both sides' columns appear in
  the output batch under their original names.
- The join key column is deduplicated (appears once, from the left side).
- Ambiguity is determined by the **actual combined output schema of the two
  join sides**, not by the underlying source schemas. If the right sub-query
  projects away a conflicting column before the join, no ambiguity exists.
- Any stage following a join that references a column name present in **both**
  sides' actual outputs **must** qualify it: `requests.timestamp`.
- An unqualified reference to an ambiguous column is an error raised by the
  expression compiler at physical plan build time (counting matches in the real
  input schema — error when count ≥ 2).
- Unambiguous columns (name appears exactly once in the combined schema) may
  always be referenced unqualified.

The parser already supports `identifier.identifier` as `FieldRef{Table, Name}`,
so no parser changes are required.

---

## Consequence: batches must carry column metadata

Because the output batch after a join has two columns named `timestamp`, a flat
`[]ColumnVector` keyed only by position is insufficient. Each `ColumnVector`
must carry enough metadata to resolve a `FieldRef{Table, Name}` lookup.

### Column metadata (decided)

```go
// ColumnMeta describes a single column in a Batch.
type ColumnMeta struct {
    Name   string // column name, e.g. "timestamp"
    Origin string // source type name the column came from, e.g. "requests"
                  // empty for computed columns (project expressions, agg outputs)
}
```

A `FieldRef{Table: "requests", Name: "timestamp"}` resolves by matching
`Name == "timestamp" && Origin == "requests"`.

A `FieldRef{Table: "", Name: "userId"}` (unqualified) resolves by finding
exactly one column where `Name == "userId"` — error if more than one match.

### Batch shape (decided)

```go
type Batch struct {
    Length  int          // actual row count; always dense (no selection vector in Phase 1)
    Schema  []ColumnMeta // parallel to Columns
    Columns []ColumnVector
}
```

---

## Resolved decisions

1. **Computed columns** (`project` expression outputs): `Origin` is empty string.
   `Name` is the alias if given, otherwise the bare field name for a field-ref item.

2. **Aggregate outputs** (`summarize`): `Origin` is empty string.
   `Name` is the alias if given, otherwise the default function name (e.g. `count_`).

3. **Qualified refs outside a join** (e.g. `requests.timestamp` in a plain query):
   resolved normally — if exactly one column matches `Name` and `Origin`, it
   succeeds. No special error for "unnecessary" qualification.

4. **Ambiguity check timing**: expression compiler at physical plan build time.
   The compiler receives the input schema (from the operator being built), counts
   matching columns for each `FieldRef`, and errors on count ≥ 2. No separate
   schema-inference pass in Phase 1.
