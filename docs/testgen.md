# Test Data Generator — Design Note

## Purpose

A CLI tool (`cmd/testgen`) that produces committed static test data for all
source-layer and query-execution tests. Using static committed data (rather than
generating it on-the-fly in tests) means:

- every test run is deterministic without embedding seeds in test files
- test data can be inspected manually in the repo
- the generator itself is separately verifiable

## Location

```
gobbler-query/
  cmd/testgen/
    main.go
  testdata/          ← committed output
    requests/
      type.json
      2026-05-01_00-00-00.000_requests.csv
      ...
    users/
      type.json
      2026-05-01_00-00-00.000_users.csv
```

Run: `go run ./cmd/testgen -seed 42 -out ./testdata`

Flags: `-seed int`, `-out string`, `-rows int` (rows per file, default 500), `-files int` (total request files, default 14 = 7 days × 2 files/day)

## Gobbler File Conventions

- Filename: `2006-01-02_15-04-05.000_<typeName>.csv`
- `type.json`: `{"name":"...","orderedColumns":[{"name":"timestamp","type":"datetime"}, ...]}`
- `timestamp` is always column 0 and is always prepended by gobbler
- CSV rows: no header; values in `orderedColumns` order; empty string = null
- Datetime format: `2006-01-02 15:04:05.000`
- Timespan format: TBD (see open questions)
- Folder layout: `<out>/<typeName>/` — type name equals folder name for Phase 1 tests

## Proposed Types

### Domain: Authentication Service

The test data models the access log of an authentication service. The service
accepts a fixed set of named request kinds (`requestCode`). Users are human
end-users with a subscription tier and a country of origin.

### `requests` — (proposed)

Event stream. Two CSV files per simulated day (one per 12-hour window), 500 rows each.
1,000 rows/day → ~1 request per 1.4 minutes. 7 days → **14 files, 7,000 total rows**.

| Column | Type | Notes |
|---|---|---|
| `timestamp` | datetime | event time (col 0, always present) |
| `requestId` | string | unique per row |
| `userId` | string | **join key** → `users.userId` |
| `requestCode` | string | one of the fixed auth-service request kinds |
| `statusCode` | int | 200/201/400/404/500 |
| `durationMs` | real | response time |
| `region` | string | deployment region |
| `ttl` | timespan | exercises timespan reader |

Distribution (proposed):
- requestCode (auth-service endpoints):
  - `login` (30%) — most frequent; 401 probable on bad credentials
  - `tokenvalidation` (25%) — high-frequency background polling
  - `userinfo` (18%) — common after a fresh token
  - `tokenexchange` (12%) — service-to-service flows
  - `logout` (8%) — less frequent; almost always 200
  - `signup` (7%) — rarest; more 400s (validation failures)
- statusCode: 200 (55%), 201 (10%), 400 (15%), 401 (10%), 500 (10%)
  - `signup` skews toward 201 on success and 400 on failure
  - `login`/`tokenexchange` skews toward 401 on failure
  - `logout` is almost always 200
- region: eastus (40%), westus (30%), northeurope (20%), southeastasia (10%)
- durationMs: realistic spread, higher for 5xx; `tokenvalidation` is fastest

### `users` — (proposed)

Dimension table, low-volume (~50 rows), one CSV file.

| Column | Type | Notes |
|---|---|---|
| `timestamp` | datetime | ingest time (col 0, always present) |
| `userId` | string | unique, join key |
| `tier` | string | free / pro / enterprise |
| `active` | bool | |
| `countryCode` | string | ISO 3166-1 alpha-2 |
| `signupDate` | datetime | second datetime column |

> **Note**: because every gobbler type has `timestamp`, a join of `requests` and
> `users` produces two `timestamp` columns — one per origin. Queries after the
> join must qualify the reference (`requests.timestamp`) unless the sub-query
> projected it away. This ambiguity is the primary motivation for the
> `ColumnMeta.Origin` design.

## Queries the Test Data Must Support

The test suite for the source/physical/expr layers will exercise:

| Query pattern | Types used |
|---|---|
| `where statusCode >= 400` | requests |
| `summarize count() by region` | requests |
| `summarize count() by requestCode` | requests |
| `summarize avg(durationMs) by requestCode` | requests |
| `summarize avg(durationMs) by statusCode` | requests |
| `where requestCode == "login" \| summarize count() by region` | requests |
| `sort by durationMs desc \| take N` | requests |
| `join (users \| project userId, tier) on userId` | requests + users |
| `where active == true` (post-join) | users column post-join |
| `project requests.timestamp, ...` (qualified ref post-join) | join result |
| `count` | either |

## File Selection Expectations

The generator produces 14 files spanning 7 days (2 per day, one per 12-hour
window: `00:00–12:00` and `12:00–00:00`). This supports:
- A window covering days 3–5 reads exactly **6 files** (2 per day)
- A `last 1h` window straddling a noon or midnight boundary reads **1–2 files**
  and applies row-level filtering on the boundary file(s)
- `(*)` (FullScan) reads all 14 files

Simulated latency: **720 minutes** (12 hours) — matches `latencyMinutes` in
`definitions.json` so testgen output is consistent with what a live Gobbler
instance would produce under those definitions.

Summary of settled parameters:

| Parameter | Value | Notes |
|---|---|---|
| Days of data | 7 | |
| Files per day | 2 | noon + midnight boundaries |
| Rows per file | 500 | |
| Total files | 14 | requests only; users = 1 file |
| Total rows | 7,000 | ~1 req/1.4 min/day |
| Simulated latency | 720 min | matches `latencyMinutes` in `definitions.json` |
| Committed data size | ~700 KB | |
| Batches per file (batchSize=256) | 2 (256 + 244) | with cross-file batches at every boundary |

## Implementation Notes

- **Timespan wire format**: Go duration string (e.g. `"15m"`, `"1h"`, `"8h"`),
  confirmed from Gobbler source (`items/definition.go`). Parsed by
  `time.ParseDuration` on the reader side.
- **`signupDate` generation**: uniformly distributed between 2025-01-01 and
  2026-04-01 — months before the request window — so it is never confused with
  ingest `timestamp`.
- **Null representation**: empty string in CSV (`,,`). Optional fields (`userId`,
  `region`, `ttl`, `countryCode`) emit null at rates 5%, 3%, 10%, and 5%
  respectively.
- **Determinism**: seed 42 committed in test helpers. The generator uses
  `math/rand/v2` with a PCG source (`rand.NewPCG(seed, 0)`).
