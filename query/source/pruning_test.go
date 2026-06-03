package source

import (
	"testing"
	"time"
)

// Two timestamp formats are used in this file:
//
//   Entry filename format  (entryTimestampFormat): "2026-05-01_08-33-45.692"
//     Underscore between date and time; hyphens instead of colons.
//     Required by Windows/Unix filesystem conventions — colons are illegal
//     in file and blob names.
//
//   Query language format  (datetimeFormat): "2026-05-01 08:33:45.692"
//     Space between date and time; colons in the time part.
//     This is Gobbler's native datetime format and the format used in
//     CSV row timestamps and in datetime() literals in the query grammar.
//
// makeEntries() takes filename-format strings; dt() parses query-language strings.

// makeEntries constructs a slice of filenames from the given timestamps,
// using the requests type name. Timestamps must be in entryTimestampFormat
// (e.g. "2026-05-01_08-33-45.692").
func makeEntries(timestamps []string) []string {
	names := make([]string, len(timestamps))
	for i, ts := range timestamps {
		if _, err := time.Parse(entryTimestampFormat, ts); err != nil {
			panic("bad test timestamp: " + ts + ": " + err.Error())
		}
		names[i] = ts + "_requests.csv"
	}
	return names
}

// dt parses a query-language datetime string ("2026-05-02 00:00:00.000") into
// a time.Time, mirroring the datetime() literal format in the query grammar.
func dt(s string) time.Time {
	t, err := time.Parse(datetimeFormat, s)
	if err != nil {
		panic(err)
	}
	return t
}

// realRequestEntries are the actual filenames from testdata/requests/.
var realRequestEntries = []string{
	"2026-05-01_00-00-11.758_requests.csv",
	"2026-05-01_12-01-17.310_requests.csv",
	"2026-05-02_00-00-20.080_requests.csv",
	"2026-05-02_12-00-07.485_requests.csv",
	"2026-05-03_00-02-11.070_requests.csv",
	"2026-05-03_12-03-03.734_requests.csv",
	"2026-05-04_00-00-16.068_requests.csv",
	"2026-05-04_12-02-23.050_requests.csv",
	"2026-05-05_00-01-18.682_requests.csv",
	"2026-05-05_12-00-27.954_requests.csv",
	"2026-05-06_00-01-04.289_requests.csv",
	"2026-05-06_12-00-34.356_requests.csv",
	"2026-05-07_00-05-53.101_requests.csv",
	"2026-05-07_12-00-51.148_requests.csv",
}

func TestSelectEntries_FullScan(t *testing.T) {
	entries := makeEntries([]string{
		"2026-05-01_00-00-00.000",
		"2026-05-02_00-00-00.000",
		"2026-05-03_00-00-00.000",
	})
	got := selectEntries(entries, time.Time{}, time.Time{})
	if len(got) != 3 {
		t.Fatalf("full scan: got %d entries, want 3", len(got))
	}
}

func TestSelectEntries_ExactMatch(t *testing.T) {
	// start exactly equals an entry_timestamp → that entry is N
	entries := makeEntries([]string{
		"2026-05-01_00-00-00.000",
		"2026-05-02_00-00-00.000",
		"2026-05-03_00-00-00.000",
	})
	got := selectEntries(entries, dt("2026-05-02 00:00:00.000"), dt("2026-05-02 23:59:59.999"))
	if len(got) != 1 {
		t.Fatalf("exact match: got %d entries, want 1; entries: %v", len(got), got)
	}
}

func TestSelectEntries_MidEntryStart(t *testing.T) {
	// T_start falls in the middle of entry A — A must be included as N
	entries := makeEntries([]string{
		"2026-05-01_00-00-00.000", // A
		"2026-05-02_00-00-00.000", // B
		"2026-05-03_00-00-00.000", // C
	})
	// T_start = 2026-05-01 12:00:00 — falls inside A (A starts 00:00, B starts next day)
	got := selectEntries(entries, dt("2026-05-01 12:00:00.000"), dt("2026-05-02 12:00:00.000"))
	if len(got) != 2 {
		t.Fatalf("mid-entry start: got %d entries, want 2; entries: %v", len(got), got)
	}
	// First entry must be A, second must be B
	if got[0] != entries[0] {
		t.Errorf("first entry: got %q, want %q", got[0], entries[0])
	}
	if got[1] != entries[1] {
		t.Errorf("second entry: got %q, want %q", got[1], entries[1])
	}
}

func TestSelectEntries_StartBeforeAllEntries(t *testing.T) {
	// T_start is before the first entry — N = first entry overall
	entries := makeEntries([]string{
		"2026-05-02_00-00-00.000",
		"2026-05-03_00-00-00.000",
	})
	got := selectEntries(entries, dt("2026-05-01 00:00:00.000"), dt("2026-05-02 12:00:00.000"))
	if len(got) != 1 {
		t.Fatalf("start before all: got %d entries, want 1; entries: %v", len(got), got)
	}
	if got[0] != entries[0] {
		t.Errorf("got %q, want %q", got[0], entries[0])
	}
}

func TestSelectEntries_EndBeforeAllEntries(t *testing.T) {
	// T_end is before the first entry — empty result
	entries := makeEntries([]string{
		"2026-05-02_00-00-00.000",
		"2026-05-03_00-00-00.000",
	})
	got := selectEntries(entries, dt("2026-05-01 00:00:00.000"), dt("2026-05-01 23:59:59.999"))
	if len(got) != 0 {
		t.Fatalf("end before all: got %d entries, want 0; entries: %v", len(got), got)
	}
}

func TestSelectEntries_StartAfterAllEntries(t *testing.T) {
	// T_start is after the last entry — still include last entry (it may have rows after T_start)
	// Wait: if the last entry's timestamp <= T_start, it IS included as N.
	// T_end must also be >= last entry for it to survive the M pruning.
	entries := makeEntries([]string{
		"2026-05-01_00-00-00.000",
		"2026-05-02_00-00-00.000",
	})
	// T_start after all entries, T_end also after all — last entry is N and M
	got := selectEntries(entries, dt("2026-05-03 00:00:00.000"), dt("2026-05-04 00:00:00.000"))
	// last entry where ts <= T_end = entries[1]; last entry where ts <= T_start = entries[1]
	// N = entries[1], M = entries[1]
	if len(got) != 1 {
		t.Fatalf("start after all: got %d entries, want 1; entries: %v", len(got), got)
	}
}

func TestSelectEntries_OpenStartBound(t *testing.T) {
	// Zero start → N = first entry always
	entries := makeEntries([]string{
		"2026-05-01_00-00-00.000",
		"2026-05-02_00-00-00.000",
		"2026-05-03_00-00-00.000",
	})
	got := selectEntries(entries, time.Time{}, dt("2026-05-02 00:00:00.000"))
	if len(got) != 2 {
		t.Fatalf("open start: got %d entries, want 2; entries: %v", len(got), got)
	}
}

func TestSelectEntries_OpenEndBound(t *testing.T) {
	// Zero end → M = last entry always
	entries := makeEntries([]string{
		"2026-05-01_00-00-00.000",
		"2026-05-02_00-00-00.000",
		"2026-05-03_00-00-00.000",
	})
	got := selectEntries(entries, dt("2026-05-02 00:00:00.000"), time.Time{})
	if len(got) != 2 {
		t.Fatalf("open end: got %d entries, want 2; entries: %v", len(got), got)
	}
	if got[0] != entries[1] {
		t.Errorf("first entry: got %q, want %q", got[0], entries[1])
	}
}

func TestSelectEntries_SingleEntry(t *testing.T) {
	entries := makeEntries([]string{"2026-05-01_00-00-00.000"})
	got := selectEntries(entries, dt("2026-05-01 06:00:00.000"), dt("2026-05-01 18:00:00.000"))
	if len(got) != 1 {
		t.Fatalf("single entry: got %d entries, want 1", len(got))
	}
}

func TestSelectEntries_Empty(t *testing.T) {
	got := selectEntries(nil, dt("2026-05-01 00:00:00.000"), dt("2026-05-02 00:00:00.000"))
	if len(got) != 0 {
		t.Fatalf("empty input: got %d entries", len(got))
	}
}

func TestSelectEntries_RealRequestFiles(t *testing.T) {
	// Query window: 2026-05-02 through 2026-05-03 (inclusive)
	start := dt("2026-05-02 00:00:00.000")
	end := dt("2026-05-03 23:59:59.999")
	got := selectEntries(realRequestEntries, start, end)

	// N = last entry where ts <= start = "2026-05-01_12-01-17.310" (May 1 12:01 <= May 2 00:00)
	// M = last entry where ts <= end  = "2026-05-03_12-03-03.734" (May 3 12:03 <= May 3 23:59)
	// Entries in range:
	//   2026-05-01_12-01-17.310  ← N (active at midnight May 2)
	//   2026-05-02_00-00-20.080
	//   2026-05-02_12-00-07.485
	//   2026-05-03_00-02-11.070
	//   2026-05-03_12-03-03.734  ← M
	wantFirst := "2026-05-01_12-01-17.310_requests.csv"
	wantLast := "2026-05-03_12-03-03.734_requests.csv"
	wantCount := 5

	if len(got) != wantCount {
		t.Fatalf("got %d entries, want %d: %v", len(got), wantCount, got)
	}
	if got[0] != wantFirst {
		t.Errorf("first entry: got %q, want %q", got[0], wantFirst)
	}
	if got[len(got)-1] != wantLast {
		t.Errorf("last entry: got %q, want %q", got[len(got)-1], wantLast)
	}
}
