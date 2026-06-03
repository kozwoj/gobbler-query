package source

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// entryTimestampFormat is the datetime portion of a Gobbler entry filename.
// Filename format: "2006-01-02_15-04-05.000_<typeName>.csv"
const entryTimestampFormat = "2006-01-02_15-04-05.000"

// parseEntryTimestamp extracts the entry_timestamp from a Gobbler CSV filename.
// It accepts both bare names ("2026-05-01_00-00-11.758_requests.csv") and full
// paths (only the base name is examined).
func parseEntryTimestamp(name string) (time.Time, error) {
	base := filepath.Base(name)
	// The timestamp occupies the first 23 characters: "YYYY-MM-DD_HH-MM-SS.mmm"
	if len(base) < len(entryTimestampFormat) {
		return time.Time{}, fmt.Errorf("entry name too short: %q", base)
	}
	return time.Parse(entryTimestampFormat, base[:len(entryTimestampFormat)])
}

// selectEntries prunes the list of entry names to those that fall within the
// time window [start, end].
//
// Selection rule (see docs/source-layer.md §4):
//   - Sort entries by entry_timestamp ascending.
//   - First entry (N): last entry where entry_timestamp <= start (the entry
//     "active" at start, which may contain rows >= start). If all entries start
//     after start, N is the first entry overall.
//   - Last entry (M): last entry where entry_timestamp <= end. If all entries
//     start after end, the result is empty.
//   - Return entries N…M inclusive, in ascending order.
//
// Open bounds: a zero start means no lower-bound pruning (N = first entry); a
// zero end means no upper-bound pruning (M = last entry). Both zero = full scan.
func selectEntries(names []string, start, end time.Time) []string {
	if len(names) == 0 {
		return nil
	}

	type entry struct {
		name string
		ts   time.Time
	}

	parsed := make([]entry, 0, len(names))
	for _, n := range names {
		ts, err := parseEntryTimestamp(n)
		if err != nil {
			continue // skip unparseable names
		}
		parsed = append(parsed, entry{name: n, ts: ts})
	}
	if len(parsed) == 0 {
		return nil
	}

	sort.Slice(parsed, func(i, j int) bool {
		return parsed[i].ts.Before(parsed[j].ts)
	})

	// Determine first index (N).
	first := 0
	if !start.IsZero() {
		// Last entry where entry_timestamp <= start.
		// If none, first stays 0 (include everything from the beginning).
		for i, e := range parsed {
			if !e.ts.After(start) { // e.ts <= start
				first = i
			}
		}
		// If the very first entry already starts after end, return empty (handled below).
	}

	// Determine last index (M).
	last := len(parsed) - 1
	if !end.IsZero() {
		// Last entry where entry_timestamp <= end.
		last = -1
		for i, e := range parsed {
			if !e.ts.After(end) { // e.ts <= end
				last = i
			}
		}
		if last == -1 {
			return nil // all entries start after end
		}
	}

	if first > last {
		return nil
	}

	result := make([]string, last-first+1)
	for i, e := range parsed[first : last+1] {
		result[i] = e.name
	}
	return result
}

// entryBaseName returns just the filename portion of an entry path, for use in
// error messages and the selectEntries input (full paths are also accepted).
func entryBaseName(name string) string {
	return strings.TrimSuffix(filepath.Base(name), ".csv")
}
