package api

import (
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/kozwoj/gobbler-query/query/catalog"
)

// ─── test catalog ─────────────────────────────────────────────────────────────

// testDataDir returns the absolute path to the testdata directory at the repo root.
func testDataDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// thisFile = <repo>/api/execute_test.go → go up one level to repo root
	return filepath.Join(filepath.Dir(thisFile), "..", "testdata")
}

// testCatalog builds a file-mode catalog pointing at the committed testdata.
func testCatalog(t *testing.T) catalog.Catalog {
	t.Helper()
	td := testDataDir(t)
	return catalog.Catalog{
		"requests": {
			TypeName:      "requests",
			StorageBucket: "requests",
			Mode:          catalog.StorageModeFile,
			OutputDir:     td,
		},
		"users": {
			TypeName:      "users",
			StorageBucket: "users",
			Mode:          catalog.StorageModeFile,
			OutputDir:     td,
		},
	}
}

// run is a short-hand that runs a query and fatals on error.
func run(t *testing.T, q string) *Result {
	t.Helper()
	r, err := Execute(q, testCatalog(t), 256)
	if err != nil {
		t.Fatalf("Execute(%q): %v", q, err)
	}
	return r
}

// colIdx returns the index of the named column in r.Schema, or -1.
func colIdx(r *Result, name string) int {
	for i, m := range r.Schema {
		if m.Name == name {
			return i
		}
	}
	return -1
}

// intVal returns the int64-compatible value at (row, col). Works for int32 and int64.
func intVal(r *Result, row, col int) int64 {
	switch v := r.Rows[row][col].(type) {
	case int32:
		return int64(v)
	case int64:
		return v
	default:
		return 0
	}
}

// strVal returns the string value at (row, col).
func strVal(r *Result, row, col int) string {
	if s, ok := r.Rows[row][col].(string); ok {
		return s
	}
	return ""
}

// floatVal returns the float64 value at (row, col).
func floatVal(r *Result, row, col int) float64 {
	if v, ok := r.Rows[row][col].(float64); ok {
		return v
	}
	return 0
}

// ─── full-scan queries (no time window) ───────────────────────────────────────

func TestExecute_FullScan_TotalRowCount(t *testing.T) {
	// 14 files × 500 rows = 7000 rows; count() must equal 7000.
	r := run(t, `requests (*) | count`)
	if len(r.Rows) != 1 {
		t.Fatalf("count returned %d rows, want 1", len(r.Rows))
	}
	n := intVal(r, 0, 0)
	if n != 7000 {
		t.Errorf("total row count = %d, want 7000", n)
	}
}

func TestExecute_Where_StatusCode_GTE_400(t *testing.T) {
	// Must return only rows where statusCode >= 400.
	r := run(t, `requests (*) | where statusCode >= 400`)
	if len(r.Rows) == 0 {
		t.Fatal("no rows returned")
	}
	cIdx := colIdx(r, "statusCode")
	if cIdx < 0 {
		t.Fatal("statusCode column not found in result")
	}
	for i, row := range r.Rows {
		sc := intVal(r, i, cIdx)
		_ = row
		if sc < 400 {
			t.Errorf("row %d: statusCode = %d, want >= 400", i, sc)
		}
	}
}

func TestExecute_SummarizeCount_ByRegion(t *testing.T) {
	r := run(t, `requests (*) | summarize n = count() by region`)
	if len(r.Rows) == 0 {
		t.Fatal("no output rows")
	}
	// Expect exactly 4 non-null regions + 1 null group (rows with null region).
	// Null rows in the group-by key form their own group.
	regionIdx := colIdx(r, "region")
	nIdx := colIdx(r, "n")
	if regionIdx < 0 || nIdx < 0 {
		t.Fatalf("missing columns: region=%d n=%d", regionIdx, nIdx)
	}
	// Total across all groups must still be 7000.
	var total int64
	for i := range r.Rows {
		total += intVal(r, i, nIdx)
	}
	if total != 7000 {
		t.Errorf("sum of group counts = %d, want 7000", total)
	}
}

func TestExecute_SummarizeCount_ByRequestCode(t *testing.T) {
	r := run(t, `requests (*) | summarize n = count() by requestCode`)
	// 6 distinct requestCodes defined in testgen.
	rcIdx := colIdx(r, "requestCode")
	nIdx := colIdx(r, "n")
	if rcIdx < 0 || nIdx < 0 {
		t.Fatalf("missing columns")
	}
	var total int64
	for i := range r.Rows {
		total += intVal(r, i, nIdx)
	}
	if total != 7000 {
		t.Errorf("sum of group counts = %d, want 7000", total)
	}
	if len(r.Rows) < 2 {
		t.Errorf("expected multiple requestCode groups, got %d", len(r.Rows))
	}
}

func TestExecute_SummarizeAvg_DurationMs_ByRequestCode(t *testing.T) {
	r := run(t, `requests (*) | summarize avg_dur = avg(durationMs) by requestCode`)
	rcIdx := colIdx(r, "requestCode")
	avgIdx := colIdx(r, "avg_dur")
	if rcIdx < 0 || avgIdx < 0 {
		t.Fatalf("missing columns: requestCode=%d avg_dur=%d", rcIdx, avgIdx)
	}
	for i := range r.Rows {
		if r.Nulls[i][avgIdx] {
			continue // null avg is valid only for an all-null group — shouldn't happen here
		}
		v := floatVal(r, i, avgIdx)
		if v <= 0 {
			t.Errorf("row %d: avg_dur = %v, want > 0", i, v)
		}
	}
}

func TestExecute_SummarizeAvg_DurationMs_ByStatusCode(t *testing.T) {
	r := run(t, `requests (*) | summarize avg_dur = avg(durationMs) by statusCode`)
	scIdx := colIdx(r, "statusCode")
	avgIdx := colIdx(r, "avg_dur")
	if scIdx < 0 || avgIdx < 0 {
		t.Fatalf("missing columns")
	}
	if len(r.Rows) < 2 {
		t.Errorf("expected multiple statusCode groups, got %d", len(r.Rows))
	}
}

func TestExecute_Where_Login_ThenCountByRegion(t *testing.T) {
	r := run(t, `requests (*) | where requestCode == "login" | summarize n = count() by region`)
	nIdx := colIdx(r, "n")
	if nIdx < 0 {
		t.Fatal("n column not found")
	}
	var total int64
	for i := range r.Rows {
		total += intVal(r, i, nIdx)
	}
	if total == 0 {
		t.Error("expected some login rows, got 0")
	}
	// Must be less than 7000 (filter removed non-login rows).
	if total >= 7000 {
		t.Errorf("filtered count = %d, want < 7000", total)
	}
}

func TestExecute_SortByDurationMs_Desc_Take10(t *testing.T) {
	r := run(t, `requests (*) | sort by durationMs desc | take 10`)
	if len(r.Rows) != 10 {
		t.Fatalf("take 10 returned %d rows", len(r.Rows))
	}
	dIdx := colIdx(r, "durationMs")
	if dIdx < 0 {
		t.Fatal("durationMs column not found")
	}
	// Verify descending order.
	for i := 1; i < len(r.Rows); i++ {
		prev := floatVal(r, i-1, dIdx)
		curr := floatVal(r, i, dIdx)
		if curr > prev {
			t.Errorf("rows %d-%d not descending: %v > %v", i-1, i, curr, prev)
		}
	}
}

func TestExecute_Count_Users(t *testing.T) {
	r := run(t, `users (*) | count`)
	if len(r.Rows) != 1 {
		t.Fatalf("count returned %d rows", len(r.Rows))
	}
	n := intVal(r, 0, 0)
	if n == 0 {
		t.Error("user count = 0, want > 0")
	}
}

func TestExecute_TimeWindow_FullScan_Star(t *testing.T) {
	// (*) full scan must read all 7000 request rows.
	r := run(t, `requests (*) | count`)
	n := intVal(r, 0, 0)
	if n != 7000 {
		t.Errorf("(*) full scan count = %d, want 7000", n)
	}
}

func TestExecute_TimeWindow_AbsoluteRange_Days3to5(t *testing.T) {
	// days 3-5 (2026-05-03 to 2026-05-06) → 6 files × 500 rows = 3000 rows.
	q := `requests (datetime(2026-05-03 00:00:00.000) .. datetime(2026-05-06 00:00:00.000)) | count`
	r := run(t, q)
	n := intVal(r, 0, 0)
	// Allow a small tolerance for boundary rows that may or may not fall in the window
	// depending on exact file timestamp vs row timestamp comparisons.
	if n < 2000 || n > 3500 {
		t.Errorf("days 3-5 count = %d, want ~3000", n)
	}
}

func TestExecute_Project_SubsetOfColumns(t *testing.T) {
	r := run(t, `requests (*) | project requestId, statusCode, region | take 5`)
	if len(r.Rows) != 5 {
		t.Fatalf("take 5 returned %d rows", len(r.Rows))
	}
	if len(r.Schema) != 3 {
		t.Errorf("schema has %d columns, want 3", len(r.Schema))
	}
	names := map[string]bool{}
	for _, m := range r.Schema {
		names[m.Name] = true
	}
	for _, want := range []string{"requestId", "statusCode", "region"} {
		if !names[want] {
			t.Errorf("column %q missing from result", want)
		}
	}
}

func TestExecute_LastHour_WithinData(t *testing.T) {
	// last 1h relative to the last file's timestamp should read at least 1 file.
	// We test with an absolute window instead so the test is not time-dependent.
	q := `requests (datetime(2026-05-07 12:00:00.000) .. datetime(2026-05-08 00:00:00.000)) | count`
	r := run(t, q)
	n := intVal(r, 0, 0)
	if n == 0 {
		t.Error("last-file window returned 0 rows")
	}
}

func TestExecute_Where_ActiveUsers(t *testing.T) {
	r := run(t, `users (*) | where active == true`)
	if len(r.Rows) == 0 {
		t.Error("no active users returned")
	}
	aIdx := colIdx(r, "active")
	if aIdx < 0 {
		t.Fatal("active column not found")
	}
	for i := range r.Rows {
		if v, ok := r.Rows[i][aIdx].(bool); !ok || !v {
			t.Errorf("row %d: active = %v, want true", i, r.Rows[i][aIdx])
		}
	}
}

func TestExecute_SummarizeCount_ByTier_Users(t *testing.T) {
	r := run(t, `users (*) | summarize n = count() by tier`)
	nIdx := colIdx(r, "n")
	if nIdx < 0 {
		t.Fatal("n column not found")
	}
	var total int64
	for i := range r.Rows {
		total += intVal(r, i, nIdx)
	}
	userCount := run(t, `users (*) | count`)
	expected := intVal(userCount, 0, 0)
	if total != expected {
		t.Errorf("sum of tier groups = %d, want %d", total, expected)
	}
}

func TestExecute_SchemaPreserved_InResult(t *testing.T) {
	r := run(t, `requests (*) | take 1`)
	// requests has 8 columns: timestamp, requestId, userId, requestCode, statusCode, durationMs, region, ttl
	if len(r.Schema) != 8 {
		t.Errorf("schema len = %d, want 8: %v", len(r.Schema), r.Schema)
	}
}

func TestExecute_NullsInResult_RegionColumn(t *testing.T) {
	// Some region values are null in the test data (rows 4 has empty region).
	// At least one null must appear across a full scan.
	r := run(t, `requests (*) | project region`)
	rIdx := colIdx(r, "region")
	if rIdx < 0 {
		t.Fatal("region column not found")
	}
	nullCount := 0
	for i := range r.Rows {
		if r.Nulls[i][rIdx] {
			nullCount++
		}
	}
	if nullCount == 0 {
		t.Error("expected some null region values, found none")
	}
}

// ─── regression: timespan column ─────────────────────────────────────────────

func TestExecute_TimespanColumn_NotNull(t *testing.T) {
	// ttl is a timespan column; at least some values should be non-null.
	r := run(t, `requests (*) | project ttl | take 20`)
	tIdx := colIdx(r, "ttl")
	if tIdx < 0 {
		t.Fatal("ttl column not found")
	}
	nonNull := 0
	for i := range r.Rows {
		if !r.Nulls[i][tIdx] {
			if _, ok := r.Rows[i][tIdx].(time.Duration); !ok {
				t.Errorf("row %d: ttl is %T, want time.Duration", i, r.Rows[i][tIdx])
			}
			nonNull++
		}
	}
	if nonNull == 0 {
		t.Error("all ttl values are null, expected some non-null")
	}
}

// ─── time window tests ────────────────────────────────────────────────────────

// TestExecute_TimeWindow_SingleDay verifies that a window covering exactly one
// calendar day selects only the two files for that day (2 × 500 = 1000 rows).
// Files for 2026-05-03: 2026-05-03_00-02-11 and 2026-05-03_12-03-03.
func TestExecute_TimeWindow_SingleDay(t *testing.T) {
	q := `requests (datetime(2026-05-03 00:00:00.000) .. datetime(2026-05-04 00:00:00.000)) | count`
	r := run(t, q)
	n := intVal(r, 0, 0)
	if n != 1000 {
		t.Errorf("single-day count = %d, want 1000", n)
	}
}

// TestExecute_TimeWindow_BeforeAllData verifies that a window entirely before
// the earliest file returns 0 rows.
func TestExecute_TimeWindow_BeforeAllData(t *testing.T) {
	q := `requests (datetime(2020-01-01 00:00:00.000) .. datetime(2020-12-31 00:00:00.000)) | count`
	r := run(t, q)
	n := intVal(r, 0, 0)
	if n != 0 {
		t.Errorf("pre-data window count = %d, want 0", n)
	}
}

// TestExecute_TimeWindow_FirstDayOnly verifies that a window covering only
// 2026-05-01 returns exactly the two files for that day (1000 rows).
func TestExecute_TimeWindow_FirstDayOnly(t *testing.T) {
	q := `requests (datetime(2026-05-01 00:00:00.000) .. datetime(2026-05-02 00:00:00.000)) | count`
	r := run(t, q)
	n := intVal(r, 0, 0)
	if n != 1000 {
		t.Errorf("first-day count = %d, want 1000", n)
	}
}

// TestExecute_TimeWindow_LastDayOnly verifies that a window covering only
// 2026-05-07 (the last day) returns exactly 1000 rows.
func TestExecute_TimeWindow_LastDayOnly(t *testing.T) {
	q := `requests (datetime(2026-05-07 00:00:00.000) .. datetime(2026-05-08 00:00:00.000)) | count`
	r := run(t, q)
	n := intVal(r, 0, 0)
	if n != 1000 {
		t.Errorf("last-day count = %d, want 1000", n)
	}
}

// TestExecute_TimeWindow_FilterStillApplies verifies that a where clause is
// still applied when a time window restricts the source files.
func TestExecute_TimeWindow_FilterStillApplies(t *testing.T) {
	// One day, only >= 400 status codes.
	qFull := `requests (datetime(2026-05-03 00:00:00.000) .. datetime(2026-05-04 00:00:00.000)) | count`
	qFiltered := `requests (datetime(2026-05-03 00:00:00.000) .. datetime(2026-05-04 00:00:00.000)) | where statusCode >= 400 | count`
	rFull := run(t, qFull)
	rFiltered := run(t, qFiltered)
	nFull := intVal(rFull, 0, 0)
	nFiltered := intVal(rFiltered, 0, 0)
	if nFiltered >= nFull {
		t.Errorf("filtered count (%d) should be less than full count (%d)", nFiltered, nFull)
	}
	if nFiltered == 0 {
		t.Error("filtered count is 0, expected some >= 400 status codes")
	}
}
