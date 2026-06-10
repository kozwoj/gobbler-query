# CLI Design — `gq`

## Command Structure

`gq` uses a **noun-verb** structure (like PowerShell, Azure CLI, Docker).  
Every command is `gq <noun> <verb> [args] [flags]`.

```
gq [--catalog <path>] <noun> <verb> [args] [flags]
```

Global flags accepted by every command:
```
--catalog <path>   path to catalog file (default: ~/.gobbler/catalog.json)
```

---

## Help Behaviour

Help is printed automatically at three levels. All help paths exit **0**.

| Invocation | Output |
|---|---|
| `gq` or `gq --help` | Top-level overview (see below) |
| `gq <noun>` or `gq <noun> --help` | Noun-level: list of verbs with one-line descriptions |
| `gq <noun> <verb> --help` | Verb-level: full usage, flags, and examples for that command |

**Top-level output** (`gq` / `gq --help`):
```
gq — query Gobbler data sources using GQL

Usage:
  gq [--catalog <path>] <noun> <verb> [args] [flags]

Global Flags:
  --catalog <path>   catalog file to use (default: ~/.gobbler/catalog.json)
                     a .gobbler.json in the current directory takes precedence

Nouns:
  catalog   manage table registrations
  query     execute GQL queries

Run 'gq <noun> --help' for the list of commands in each noun.
```

**Noun-level output** (`gq catalog` / `gq catalog --help`):
```
gq catalog — manage table registrations

Commands:
  add <table>     register a table (file mode or blob mode)
  remove <table>  deregister a table
  list            print all registered tables
  show <table>    print a single entry's details
  load <file>     merge entries from a snapshot file into the catalog
  export          write the current catalog to a snapshot file

Run 'gq catalog <verb> --help' for full usage of each command.
```

---

## Noun: `catalog`

Manages the set of known tables (logically: "connecting to a data source").

The catalog is a JSON file that maps table names to storage entries.  
Account keys are **never** written to the catalog file — see the [Account Keys](#account-keys) section.

| Command | Description |
|---|---|
| `gq catalog add <table> --dir <path>` | Register a file-mode table |
| `gq catalog add <table> --account <name> --container <name>` | Register a blob-mode table |
| `gq catalog remove <table>` | Deregister a table |
| `gq catalog list` | Print all registered tables |
| `gq catalog show <table>` | Print a single entry's details |
| `gq catalog load <file>` | Merge entries from a snapshot file into the catalog |
| `gq catalog export [--out <file>]` | Write the current catalog to a snapshot file |

**Default catalog location:** `~/.gobbler/catalog.json`  
A project-local `.gobbler.json` in the current directory takes precedence (like `.editorconfig`).

### Catalog Snapshot File

A snapshot file is a portable JSON array of table entries. It can be checked into a
project repository alongside `.gql` query files and shared across machines.

```json
[
  { "table": "requests", "mode": "file", "dir": "/data/requests" },
  { "table": "users",    "mode": "file", "dir": "/data/users" },
  { "table": "events",   "mode": "blob", "account": "gobblerstorage", "container": "events" }
]
```

Field reference:

| Field | Required | Description |
|---|---|---|
| `table` | always | table name (must be a valid Gobbler item-type name) |
| `mode` | always | `"file"` or `"blob"` |
| `dir` | file mode | path to the directory containing CSV files |
| `account` | blob mode | Azure storage account name |
| `container` | blob mode | Azure blob container name |

**`gq catalog load <file>`** merges all entries into the catalog file. If any entry's
`table` name already exists in the catalog, the entire load fails with an error and
the catalog file is left unchanged. Use `gq catalog remove <table>` first to
explicitly clear a conflicting entry before loading.

**`gq catalog export [--out <file>]`** writes all current catalog entries as a snapshot
array. Without `--out`, output goes to stdout. The exported file has the same schema
as the snapshot file above and can be passed directly to `gq catalog load`.

---

## Noun: `query`

Executes a GQL query against the active catalog.

| Command | Description |
|---|---|
| `gq query run "<gql>"` | Run an inline query string |
| `gq query run --file <path.gql>` | Run a query from a file |

Output flags (on `gq query run`):

| Flag | Description |
|---|---|
| `--format table\|csv\|jsonl\|json` | Output format (see below) |
| `--out <file>` | Write output to file instead of stdout |

### Output Formats

| Value | Description | Auto-selected when |
|---|---|---|
| `table` | Aligned columns with header | stdout is a TTY |
| `csv` | Comma-separated with header row | stdout is piped |
| `jsonl` | One JSON object per line | explicit only |
| `json` | JSON array of objects | explicit only |

Auto-detection: TTY → `table`, piped → `csv`. Always overridable with `--format`.

---

## Examples

```sh
# Register a local data directory
gq catalog add requests --dir /data/telemetry

# Register a blob-mode table
gq catalog add events --account gobblerstorage --container events

# Load multiple tables from a project snapshot file
gq catalog load ./project-catalog.json

# Export the current catalog for sharing or backup
gq catalog export --out ./project-catalog.json

# List all tables
gq catalog list

# Run a query against blob tables (key supplied via env var)
# $env:GOBBLER_KEY_GOBBLERSTORAGE = "sv=2023-..."
gq query run "events (*) | where statusCode >= 400 | take 20"

# Run a query joining two blob tables from different accounts
# $env:GOBBLER_KEY_GOBBLERSTORAGE = "sv=2023-..."
# $env:GOBBLER_KEY_ARCHIVEACCOUNT = "sv=2023-..."
gq query run "requests (*) | join users (*) on userId | take 10"

# Aggregate and write CSV to file
gq query run "requests (*) | summarize n = count() by region" --out results.csv

# Use a non-default catalog
gq --catalog ./prod.json query run "requests (*) | count"

# Run from file, explicit JSON output
gq query run --file daily_report.gql --format json --out report.json
```

---

## Account Keys

Azure storage account names consist only of lowercase letters (`a–z`) and digits (`0–9`),
which are also legal in environment variable names on both Windows and Linux.

For each blob-mode table, `gq` reads the account key from an environment variable named:

```
GOBBLER_KEY_<ACCOUNT>
```

where `<ACCOUNT>` is the storage account name uppercased. Examples:

| Account name | Environment variable |
|---|---|
| `gobblerstorage` | `GOBBLER_KEY_GOBBLERSTORAGE` |
| `archiveaccount` | `GOBBLER_KEY_ARCHIVEACCOUNT` |

If a query touches a blob table whose account key env var is not set, `gq` exits with
an error naming the missing variable before any data is read.

**Setting keys for a session (PowerShell):**
```powershell
$env:GOBBLER_KEY_GOBBLERSTORAGE = "sv=2023-..."
$env:GOBBLER_KEY_ARCHIVEACCOUNT = "sv=2023-..."
```

**Setting keys for a session (bash):**
```sh
export GOBBLER_KEY_GOBBLERSTORAGE="sv=2023-..."
export GOBBLER_KEY_ARCHIVEACCOUNT="sv=2023-..."
```

Only the account keys for tables that are actually referenced by the query need to be
set — unused accounts are not checked.

---

## Open Questions

- Interactive REPL mode (multi-line query editing) — phase 2.
- Paging for large `table` output (`less`-style) — phase 2.
- `gq query explain "<gql>"` — print the logical/physical plan instead of results — phase 2.
