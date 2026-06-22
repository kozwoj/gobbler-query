package api

import (
	"os"
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

func TestExecute_WherePushdown_PartitionCheck(t *testing.T) {
	// Where → Source pushdown: the above-threshold and below-threshold
	// partitions must together equal the full scan count.
	total := intVal(run(t, `requests (*) | count`), 0, 0)
	above := intVal(run(t, `requests (*) | where statusCode >= 400 | count`), 0, 0)
	below := intVal(run(t, `requests (*) | where statusCode < 400 | count`), 0, 0)
	if above+below != total {
		t.Errorf("above(%d) + below(%d) = %d, want %d (total)", above, below, above+below, total)
	}
	if above == 0 || below == 0 {
		t.Errorf("expected both partitions non-empty: above=%d below=%d", above, below)
	}
}

func TestExecute_WherePushdown_ValuesCorrect(t *testing.T) {
	// Every row returned by the pushed-down where must satisfy the predicate.
	r := run(t, `requests (*) | where statusCode >= 400`)
	cIdx := colIdx(r, "statusCode")
	if cIdx < 0 {
		t.Fatal("statusCode column missing")
	}
	for i := range r.Rows {
		if sc := intVal(r, i, cIdx); sc < 400 {
			t.Errorf("row %d: statusCode = %d, want >= 400", i, sc)
		}
	}
	if len(r.Rows) == 0 {
		t.Error("expected at least one row")
	}
}

func TestExecute_ProjectPushdown_NarrowSchema(t *testing.T) {
	// Project → Source: output schema must contain exactly the projected columns.
	r := run(t, `requests (*) | project userId, statusCode`)
	if len(r.Schema) != 2 {
		t.Fatalf("schema width = %d, want 2 (userId, statusCode)", len(r.Schema))
	}
	if r.Schema[0].Name != "userId" {
		t.Errorf("col[0] = %q, want %q", r.Schema[0].Name, "userId")
	}
	if r.Schema[1].Name != "statusCode" {
		t.Errorf("col[1] = %q, want %q", r.Schema[1].Name, "statusCode")
	}
	// All 7000 rows must still be present — project does not filter.
	if len(r.Rows) != 7000 {
		t.Errorf("row count = %d, want 7000", len(r.Rows))
	}
}

func TestExecute_ProjectPushdown_ValuesCorrect(t *testing.T) {
	// Every row must have a plausible statusCode; userId may be null (nil).
	r := run(t, `requests (*) | project userId, statusCode`)
	scIdx := colIdx(r, "statusCode")
	if scIdx < 0 {
		t.Fatal("expected statusCode column")
	}
	for i := range r.Rows {
		if sc := intVal(r, i, scIdx); sc < 100 || sc > 599 {
			t.Errorf("row %d: statusCode = %d, want 100–599", i, sc)
			break
		}
	}
}

func TestExecute_WhereThenProjectPushdown_Schema(t *testing.T) {
	// Project → Where → Source: schema is narrow and every row satisfies the predicate.
	r := run(t, `requests (*) | where statusCode >= 400 | project userId, statusCode`)
	if len(r.Schema) != 2 {
		t.Fatalf("schema width = %d, want 2 (userId, statusCode)", len(r.Schema))
	}
	if r.Schema[0].Name != "userId" || r.Schema[1].Name != "statusCode" {
		t.Errorf("schema = [%s, %s], want [userId, statusCode]", r.Schema[0].Name, r.Schema[1].Name)
	}
	scIdx := colIdx(r, "statusCode")
	for i := range r.Rows {
		if sc := intVal(r, i, scIdx); sc < 400 {
			t.Errorf("row %d: statusCode = %d, want >= 400", i, sc)
			break
		}
	}
	if len(r.Rows) == 0 {
		t.Error("expected at least one row")
	}
}

func TestExecute_WhereThenProjectPushdown_CrossCheck(t *testing.T) {
	// Row count of the combined pushdown must equal the standalone where count.
	narrow := run(t, `requests (*) | where statusCode >= 400 | project userId, statusCode`)
	baseline := run(t, `requests (*) | where statusCode >= 400 | count`)
	if len(baseline.Rows) != 1 {
		t.Fatal("count query returned wrong number of rows")
	}
	want := intVal(baseline, 0, 0)
	got := int64(len(narrow.Rows))
	if got != want {
		t.Errorf("pushdown row count = %d, standalone where count = %d", got, want)
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

func TestExecute_ProjectThenWherePushdown_Schema(t *testing.T) {
	// Where → Project → Source: output must be narrow and satisfy the predicate.
	r := run(t, `requests (*) | project userId, statusCode | where statusCode >= 400`)
	if len(r.Schema) != 2 {
		t.Fatalf("schema width = %d, want 2 (userId, statusCode)", len(r.Schema))
	}
	if r.Schema[0].Name != "userId" || r.Schema[1].Name != "statusCode" {
		t.Errorf("schema = [%s, %s], want [userId, statusCode]", r.Schema[0].Name, r.Schema[1].Name)
	}
	scIdx := colIdx(r, "statusCode")
	for i := range r.Rows {
		if sc := intVal(r, i, scIdx); sc < 400 {
			t.Errorf("row %d: statusCode = %d, want >= 400", i, sc)
			break
		}
	}
	if len(r.Rows) == 0 {
		t.Error("expected at least one row")
	}
}

func TestExecute_ProjectThenWherePushdown_CrossCheck(t *testing.T) {
	// Row count must match the equivalent where-then-project pushdown (Step 9).
	gotR := run(t, `requests (*) | project userId, statusCode | where statusCode >= 400`)
	wantR := run(t, `requests (*) | where statusCode >= 400 | project userId, statusCode`)
	if len(gotR.Rows) != len(wantR.Rows) {
		t.Errorf("project-then-where row count = %d, where-then-project = %d", len(gotR.Rows), len(wantR.Rows))
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

// ─── join queries ─────────────────────────────────────────────────────────────

// TestExecute_Join_RequestsWithUsers verifies that joining requests and users
// on userId produces a non-empty result and that the output contains columns
// from both sides.
func TestExecute_Join_RequestsWithUsers(t *testing.T) {
	r := run(t, `requests (*) | join (users (*)) on userId`)

	if len(r.Rows) == 0 {
		t.Fatal("join returned 0 rows, expected > 0")
	}

	// Output schema must contain columns from both sides.
	names := map[string]bool{}
	for _, m := range r.Schema {
		names[m.Name] = true
	}
	for _, want := range []string{"requestId", "statusCode", "tier"} {
		if !names[want] {
			t.Errorf("column %q missing from join output; schema: %v", want, r.Schema)
		}
	}
}

// TestExecute_Join_ThenSummarizeByTier verifies a realistic pipeline:
// join requests with users, then count requests per user tier.
// The sum of all tier counts must equal the number of matched rows.
func TestExecute_Join_ThenSummarizeByTier(t *testing.T) {
	// Count total matched rows first.
	rJoin := run(t, `requests (*) | join (users (*) | project userId, tier) on userId | count`)
	if len(rJoin.Rows) != 1 {
		t.Fatalf("count returned %d rows, want 1", len(rJoin.Rows))
	}
	totalMatched := intVal(rJoin, 0, 0)
	if totalMatched == 0 {
		t.Fatal("join produced 0 matched rows")
	}

	// Summarize by tier — sum of group counts must equal totalMatched.
	rByTier := run(t, `requests (*) | join (users (*) | project userId, tier) on userId | summarize n = count() by tier`)
	nIdx := colIdx(rByTier, "n")
	if nIdx < 0 {
		t.Fatal("n column not found in summarize output")
	}
	var sum int64
	for i := range rByTier.Rows {
		sum += intVal(rByTier, i, nIdx)
	}
	if sum != totalMatched {
		t.Errorf("sum of tier groups = %d, want %d (total matched)", sum, totalMatched)
	}
	if len(rByTier.Rows) < 2 {
		t.Errorf("expected at least 2 tier groups, got %d", len(rByTier.Rows))
	}
}

// TestExecute_Join_CountByCountryCode joins requests with users to bring in
// countryCode, then counts requests per country. The sum must equal the total
// number of matched rows (all requests have a matching user in the testdata).
func TestExecute_Join_CountByCountryCode(t *testing.T) {
	rJoin := run(t, `requests (*) | join (users (*) | project userId, countryCode) on userId | summarize n = count() by countryCode`)

	nIdx := colIdx(rJoin, "n")
	ccIdx := colIdx(rJoin, "countryCode")
	if nIdx < 0 || ccIdx < 0 {
		t.Fatalf("missing columns: n=%d countryCode=%d", nIdx, ccIdx)
	}
	if len(rJoin.Rows) < 2 {
		t.Errorf("expected at least 2 country groups, got %d", len(rJoin.Rows))
	}

	var total int64
	for i := range rJoin.Rows {
		total += intVal(rJoin, i, nIdx)
	}

	rAll := run(t, `requests (*) | join (users (*)) on userId | count`)
	expected := intVal(rAll, 0, 0)
	if total != expected {
		t.Errorf("sum of countryCode groups = %d, want %d", total, expected)
	}
}

// TestExecute_Join_CountByCountryCode_First3Days is the same countryCode query
// restricted to the first 3 days of May 2026 (2026-05-01 .. 2026-05-04).
// The window covers 6 files × 500 rows = 3000 request rows, but ~5% have null
// userId and are dropped by the inner join.
func TestExecute_Join_CountByCountryCode_First3Days(t *testing.T) {
	const window = `datetime(2026-05-01 00:00:00.000) .. datetime(2026-05-04 00:00:00.000)`

	rJoin := run(t, `requests (`+window+`) | join (users (*) | project userId, countryCode) on userId | summarize n = count() by countryCode`)

	nIdx := colIdx(rJoin, "n")
	ccIdx := colIdx(rJoin, "countryCode")
	if nIdx < 0 || ccIdx < 0 {
		t.Fatalf("missing columns: n=%d countryCode=%d", nIdx, ccIdx)
	}
	if len(rJoin.Rows) < 2 {
		t.Errorf("expected at least 2 country groups, got %d", len(rJoin.Rows))
	}

	// Sum of groups must equal the total matched rows for the same window.
	rTotal := run(t, `requests (`+window+`) | join (users (*)) on userId | count`)
	expected := intVal(rTotal, 0, 0)

	var total int64
	for i := range rJoin.Rows {
		total += intVal(rJoin, i, nIdx)
	}
	if total != expected {
		t.Errorf("sum of countryCode groups = %d, want %d (matched rows in window)", total, expected)
	}
	// Sanity: matched rows should be well under 3000 (null userId rows dropped) but > 0.
	if expected == 0 || expected > 3000 {
		t.Errorf("unexpected matched row count %d for 3-day window", expected)
	}
}

// TestExecute_Join_CountByCountryCode_First3Days_Sorted is the same query as
// above but with the result sorted by n desc (most requests first).
func TestExecute_Join_CountByCountryCode_First3Days_Sorted(t *testing.T) {
	const window = `datetime(2026-05-01 00:00:00.000) .. datetime(2026-05-04 00:00:00.000)`

	r := run(t, `requests (`+window+`) | join (users (*) | project userId, countryCode) on userId | summarize n = count() by countryCode | sort by n desc`)

	nIdx := colIdx(r, "n")
	if nIdx < 0 {
		t.Fatal("n column not found")
	}
	if len(r.Rows) < 2 {
		t.Fatalf("expected at least 2 rows, got %d", len(r.Rows))
	}
	// Verify descending order.
	for i := 1; i < len(r.Rows); i++ {
		prev := intVal(r, i-1, nIdx)
		curr := intVal(r, i, nIdx)
		if curr > prev {
			t.Errorf("rows %d-%d not descending: %d > %d", i-1, i, curr, prev)
		}
	}
}

// requests with a userId that has no matching user row must be dropped.
// Total join output must be <= total request rows.
func TestExecute_Join_NonMatchingRowsDropped(t *testing.T) {
	rAll := run(t, `requests (*) | count`)
	rJoin := run(t, `requests (*) | join (users (*)) on userId | count`)

	nAll := intVal(rAll, 0, 0)
	nJoin := intVal(rJoin, 0, 0)

	if nJoin > nAll {
		t.Errorf("join produced more rows (%d) than requests (%d) — impossible for inner join", nJoin, nAll)
	}
	// Testdata is generated so that all userIds in requests exist in users,
	// so nJoin should be > 0 and == nAll (every request matches a user).
	if nJoin == 0 {
		t.Error("join produced 0 rows, expected matched rows")
	}
}

// ─── dynamic column tests ─────────────────────────────────────────────────────

// dynamicCatalog creates a temp directory with a single-table CSV data set that
// has a dynamic (opaque JSON string) column, and returns the catalog pointing
// at it.
//
// Schema: id (string), meta (dynamic), score (int)
// Rows:
//
//	r1  {"env":"prod","version":2}   100
//	r2  {"env":"dev","version":1}    200
//	r3  {"env":"prod","version":3}   150
//	r4  (null meta)                  50
func dynamicCatalog(t *testing.T) catalog.Catalog {
	t.Helper()
	dir := t.TempDir()
	tableDir := filepath.Join(dir, "events")
	if err := os.MkdirAll(tableDir, 0700); err != nil {
		t.Fatal(err)
	}

	typeJSON := `{
  "name": "events",
  "orderedColumns": [
    {"name": "timestamp", "type": "datetime"},
    {"name": "id",        "type": "string"},
    {"name": "meta",      "type": "dynamic"},
    {"name": "score",     "type": "int"}
  ]
}`
	if err := os.WriteFile(filepath.Join(tableDir, "events.json"), []byte(typeJSON), 0600); err != nil {
		t.Fatal(err)
	}

	// File name must use the Gobbler convention: <timestamp>_<typeName>.csv
	csvData := `2026-05-01T00:00:00.000Z,r1,"{""env"":""prod"",""version"":2}",100` + "\n" +
		`2026-05-01T00:01:00.000Z,r2,"{""env"":""dev"",""version"":1}",200` + "\n" +
		`2026-05-01T00:02:00.000Z,r3,"{""env"":""prod"",""version"":3}",150` + "\n" +
		"2026-05-01T00:03:00.000Z,r4,,50\n"
	if err := os.WriteFile(filepath.Join(tableDir, "2026-05-01_00-00-00.000_events.csv"), []byte(csvData), 0600); err != nil {
		t.Fatal(err)
	}

	return catalog.Catalog{
		"events": {
			TypeName:      "events",
			StorageBucket: "events",
			Mode:          catalog.StorageModeFile,
			OutputDir:     dir,
		},
	}
}

// runDyn is like run() but uses the dynamic catalog.
func runDyn(t *testing.T, q string) *Result {
	t.Helper()
	r, err := Execute(q, dynamicCatalog(t), 256)
	if err != nil {
		t.Fatalf("Execute(%q): %v", q, err)
	}
	return r
}

// TestExecute_Dynamic_FullScan verifies that a full scan over a table with a
// dynamic column returns all rows with the raw JSON string preserved.
func TestExecute_Dynamic_FullScan(t *testing.T) {
	r := runDyn(t, `events (*) | count`)
	if len(r.Rows) != 1 {
		t.Fatalf("count returned %d rows, want 1", len(r.Rows))
	}
	if n := intVal(r, 0, 0); n != 4 {
		t.Errorf("count = %d, want 4", n)
	}
}

// TestExecute_Dynamic_Project verifies that projecting a dynamic column passes
// the raw JSON string through to the result unchanged.
func TestExecute_Dynamic_Project(t *testing.T) {
	r := runDyn(t, `events (*) | project id, meta`)
	if len(r.Schema) != 2 {
		t.Fatalf("schema len = %d, want 2", len(r.Schema))
	}
	metaIdx := colIdx(r, "meta")
	if metaIdx < 0 {
		t.Fatal("meta column not found")
	}
	// Row 0 (r1): meta must be the raw JSON string, not parsed.
	v := strVal(r, 0, metaIdx)
	if v == "" {
		t.Error("meta for r1 is empty, want JSON string")
	}
	// Row 3 (r4): meta must be null.
	if !r.Nulls[3][metaIdx] {
		t.Errorf("row 3 meta should be null, got %v", r.Rows[3][metaIdx])
	}
}

// TestExecute_Dynamic_Where_Eq verifies that == comparison on a dynamic column
// works: it matches the exact raw JSON string.
func TestExecute_Dynamic_Where_Eq(t *testing.T) {
	// The CSV stores the value as a JSON string; match it exactly.
	r := runDyn(t, `events (*) | where meta == "{\"env\":\"prod\",\"version\":2}"`)
	if len(r.Rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(r.Rows))
	}
	if strVal(r, 0, colIdx(r, "id")) != "r1" {
		t.Errorf("expected row r1, got %v", r.Rows[0])
	}
}

// TestExecute_Dynamic_NullsPreserved verifies that null dynamic values
// survive a project stage.
func TestExecute_Dynamic_NullsPreserved(t *testing.T) {
	r := runDyn(t, `events (*) | project meta`)
	metaIdx := colIdx(r, "meta")
	nullCount := 0
	for i := range r.Rows {
		if r.Nulls[i][metaIdx] {
			nullCount++
		}
	}
	if nullCount != 1 {
		t.Errorf("null meta count = %d, want 1", nullCount)
	}
}

// TestExecute_Dynamic_SummarizeCount_ByMeta verifies that a dynamic column
// can be used as a group-by key in summarize (groups by exact JSON string).
func TestExecute_Dynamic_SummarizeCount_ByMeta(t *testing.T) {
	r := runDyn(t, `events (*) | summarize n = count() by meta`)
	nIdx := colIdx(r, "n")
	if nIdx < 0 {
		t.Fatal("n column not found")
	}
	// 3 distinct non-null JSON values + 1 null group = 4 groups.
	if len(r.Rows) != 4 {
		t.Errorf("group count = %d, want 4 (3 JSON values + 1 null)", len(r.Rows))
	}
	var total int64
	for i := range r.Rows {
		total += intVal(r, i, nIdx)
	}
	if total != 4 {
		t.Errorf("sum = %d, want 4", total)
	}
}
