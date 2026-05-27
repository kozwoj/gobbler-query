# Gobbler Query — Segment Storage Model

## Concept

A **segment** is the on-disk unit of the columnar store. It holds a fixed number of rows (the segment size **S**, default 4096) for all columns of one item type. Segment boundaries are independent of source CSV file boundaries: a single segment may span rows from the tail of one CSV file and the head of the next.

A **source** (item type collection) is an ordered sequence of segments numbered from zero. The last segment is the only one that may be partial (fewer than S rows).

```
Source (item type "alpha")
  alpha-0000000000.seg   [4096 rows]
  alpha-0000000001.seg   [4096 rows]
  alpha-0000000002.seg   [  317 rows]  ← partial, currently open
  alpha.catalog
```

---

## Timestamp field

Gobbler prepends a `timestamp` field (type `datetime`) as the first field of every item. The name `timestamp` is reserved. It is the **ingestion key** used for:

- selecting which source CSV/blob files fall within a query time span
- recording `MinTimestamp` / `MaxTimestamp` per segment in the catalog (enables segment skipping)

---

## Catalog file

Each item type has one catalog file: `<typeName>.catalog`

It contains one fixed-size entry per segment:

```
SegmentEntry {
    SegmentIndex    uint32   // 0-based, matches filename index
    RowCount        uint32   // ≤ S; equals S for all but the last segment
    MinTimestamp    int64    // Unix nanoseconds — min(timestamp) in segment
    MaxTimestamp    int64    // Unix nanoseconds — max(timestamp) in segment
    SourceRowEnd    uint64   // absolute row offset in the source CSV stream
                             // at the end of this segment; used as ingestion cursor
}
```

`SourceRowEnd` is the resume cursor for incremental ingestion. On the next ingestion run the engine opens new CSVs in order, skips to `SourceRowEnd`, tops up any partial last segment, then writes new full segments.

---

## Segment file layout

```
┌──────────────────────────────────────────────────────────┐
│  Magic + Version          8 bytes                        │
│  Row count                uint32  (≤ S)                  │
│  Column count             uint32                         │
│  Schema section           variable                       │
│    per column:                                           │
│      name length  uint16 + name bytes                    │
│      type tag     uint8                                  │
│  Column directory         16 bytes × column count        │
│    per column:                                           │
│      data offset  uint64  (byte offset from file start)  │
│      data length  uint64  (bytes)                        │
│  Per-column data blocks   variable                       │
│    per column:                                           │
│      null bitmap  ceil(rowCount/8) bytes, LSB-first,     │
│                   padded to 8-byte boundary              │
│      values       see encoding table below               │
└──────────────────────────────────────────────────────────┘
```

The column directory enables **seekable column access**: a reader fetches only the columns referenced by the query (projection pushdown), which matters for Block Blob range reads.

The schema section makes each segment self-describing, independent of the item definition JSON.

---

## Column encoding

| Gobbler type | Type tag | Value encoding |
|---|---|---|
| `bool` | 1 | `uint8`, 0 or 1, 1 byte/row |
| `datetime` | 2 | Unix nanoseconds, `int64`, little-endian, 8 bytes/row |
| `dynamic` | 3 | offset array + byte blob (raw JSON bytes) |
| `int` | 4 | `int64`, little-endian, 8 bytes/row |
| `real` | 5 | `float64`, IEEE 754, 8 bytes/row |
| `string` | 6 | offset array + byte blob (see below) |
| `timespan` | 7 | Ticks = 100ns, `int64`, little-endian, 8 bytes/row |




- bool
- datetime
- dynamic
- int
- real
- string, and 
- timespan

### Offset array + byte blob (string and dynamic)

```
uint32[rowCount]   // byte offsets into the blob, one per row
                   // offset[i+1] - offset[i] = length of row i's value
                   // sentinel: append one extra offset = total blob length
[]byte             // contiguous UTF-8 (string) or JSON (dynamic) bytes
```

This layout allows O(1) random access to any row's value and supports byte-range reads for Block Blobs. A null row has length zero in the blob; its null bit is set in the null bitmap.

---

## Null bitmap

One bit per row, packed LSB-first into `uint8` bytes. A set bit (1) means the value at that row index is null. Padded to the next 8-byte boundary. Stored immediately before the column's value data within its data block.

---

## Segment filename convention

```
<typeName>-<10-digit-zero-padded-index>.seg
```

Examples:

```
alpha-0000000000.seg
alpha-0000000001.seg
alpha.catalog
```

Ten digits support up to ~10 billion segments per item type.

---

## Incremental ingestion flow

```
1. Load catalog  →  read SourceRowEnd of the last entry
2. Open source CSV/blob files in ascending timestamp order
3. Skip the first SourceRowEnd rows across those files
4. If the last segment is partial:
       read up to (S − lastSegment.RowCount) rows
       rewrite the last segment file
       update its catalog entry
5. While rows remain:
       accumulate S rows into a new segment file
       append a new catalog entry
6. Flush catalog
```

Only the last segment is ever rewritten. All prior segments are immutable once written.

---

## Segment skipping during query execution

Before opening any segment file the query engine checks the catalog:

```
if segment.MaxTimestamp < queryStart || segment.MinTimestamp > queryEnd {
    skip segment   // no rows can match the time predicate
}
```

This is the primary optimization for time-bounded queries over large collections.

---

## Block Blob layout (Azure)

Segments and the catalog are stored in the same container as the source CSVs, alongside them:

```
<container>/<folder>/alpha-0000000000.seg
<container>/<folder>/alpha-0000000001.seg
<container>/<folder>/alpha.catalog
```

The column directory byte offsets enable HTTP `Range` reads to fetch only the needed columns, avoiding full blob downloads.

---

## Open questions

1. **Catalog format**: binary fixed-size entries (easy to append, zero parsing overhead) vs. newline-delimited JSON (human-readable, easier to inspect). Binary recommended for v1.
2. **Compression**: no compression for v1. Per-column zstd can be added by extending the column directory entry with a `compressed length` field alongside `data length`.
3. **Segment size S**: 4096 rows is the default. Should it be configurable per item type in the item definition, or a global constant?
