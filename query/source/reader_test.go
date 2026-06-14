package source

import (
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/kozwoj/gobbler-query/query/batch"
	"github.com/kozwoj/gobbler-query/query/catalog"
)

// requestsDir is the testdata directory for the requests type.
const requestsDir = "../../testdata/requests"

// testBatchSize is small enough to produce multiple batches per file (500 rows)
// while exercising cross-file batch boundaries.
const testBatchSize = 256

// countAllRows drains a TableReader and returns the total number of rows read.
func countAllRows(t *testing.T, r TableReader) int {
	t.Helper()
	total := 0
	for {
		b, err := r.GetNextBatch()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("GetNextBatch: %v", err)
		}
		total += b.Length
	}
	return total
}

// --- Construction ---

func TestFileTableReader_MissingTypeJSON(t *testing.T) {
	_, err := NewFileTableReader(t.TempDir(), "requests", time.Time{}, time.Time{}, testBatchSize)
	if err == nil {
		t.Fatal("expected error for missing {typeName}.json, got nil")
	}
}

func TestFileTableReader_EmptyWindow(t *testing.T) {
	// Window entirely before all testdata → zero files selected → immediate EOF.
	start := time.Time{}
	end := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	r, err := NewFileTableReader(requestsDir, "requests", start, end, testBatchSize)
	if err != nil {
		t.Fatalf("NewFileTableReader: %v", err)
	}
	defer r.Close()

	b, err := r.GetNextBatch()
	if err != io.EOF {
		t.Fatalf("expected io.EOF, got batch=%v err=%v", b, err)
	}
}

// --- Full scan ---

func TestFileTableReader_FullScan_RowCount(t *testing.T) {
	// All 14 files × 500 rows = 7000 rows total.
	r, err := NewFileTableReader(requestsDir, "requests", time.Time{}, time.Time{}, testBatchSize)
	if err != nil {
		t.Fatalf("NewFileTableReader: %v", err)
	}
	defer r.Close()

	got := countAllRows(t, r)
	if got != 7000 {
		t.Errorf("total rows: got %d, want 7000", got)
	}
}

func TestFileTableReader_BatchSizes(t *testing.T) {
	// With batch=256 and 7000 total rows:
	//   27 full batches of 256  +  1 final batch of 88  = 28 batches.
	r, err := NewFileTableReader(requestsDir, "requests", time.Time{}, time.Time{}, testBatchSize)
	if err != nil {
		t.Fatalf("NewFileTableReader: %v", err)
	}
	defer r.Close()

	const wantFull = 27
	const wantLast = 88 // 7000 - 27*256
	const wantTotal = 28

	var batchCount, rowCount int
	for {
		b, err := r.GetNextBatch()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("GetNextBatch: %v", err)
		}
		batchCount++
		rowCount += b.Length
		if batchCount < wantTotal {
			if b.Length != testBatchSize {
				t.Errorf("batch %d: got %d rows, want %d", batchCount, b.Length, testBatchSize)
			}
		}
	}
	if batchCount != wantTotal {
		t.Errorf("batch count: got %d, want %d", batchCount, wantTotal)
	}
	if rowCount != 7000 {
		t.Errorf("total rows: got %d, want 7000", rowCount)
	}
}

// --- Schema and column values ---

func TestFileTableReader_ColumnMeta(t *testing.T) {
	r, err := NewFileTableReader(requestsDir, "requests", time.Time{}, time.Time{}, testBatchSize)
	if err != nil {
		t.Fatalf("NewFileTableReader: %v", err)
	}
	defer r.Close()

	b, err := r.GetNextBatch()
	if err != nil {
		t.Fatalf("GetNextBatch: %v", err)
	}

	wantMeta := []batch.ColumnMeta{
		{Name: "timestamp", Origin: "requests"},
		{Name: "requestId", Origin: "requests"},
		{Name: "userId", Origin: "requests"},
		{Name: "requestCode", Origin: "requests"},
		{Name: "statusCode", Origin: "requests"},
		{Name: "durationMs", Origin: "requests"},
		{Name: "region", Origin: "requests"},
		{Name: "ttl", Origin: "requests"},
	}
	if len(b.Schema) != len(wantMeta) {
		t.Fatalf("schema length: got %d, want %d", len(b.Schema), len(wantMeta))
	}
	for i, w := range wantMeta {
		if b.Schema[i] != w {
			t.Errorf("schema[%d]: got %+v, want %+v", i, b.Schema[i], w)
		}
	}
}

func TestFileTableReader_ColumnValues_FirstRow(t *testing.T) {
	// First row of requests:
	// 2026-05-01 00:00:11.758,req-0000001,user-042,login,401,18.318,eastus,1h
	r, err := NewFileTableReader(requestsDir, "requests", time.Time{}, time.Time{}, testBatchSize)
	if err != nil {
		t.Fatalf("NewFileTableReader: %v", err)
	}
	defer r.Close()

	b, err := r.GetNextBatch()
	if err != nil {
		t.Fatalf("GetNextBatch: %v", err)
	}

	// timestamp col[0]
	tsVec := b.Columns[0].(*batch.DatetimeVector)
	wantTS := time.Date(2026, 5, 1, 0, 0, 11, 758_000_000, time.UTC)
	if !tsVec.Values[0].Equal(wantTS) {
		t.Errorf("timestamp row 0: got %v, want %v", tsVec.Values[0], wantTS)
	}
	if tsVec.IsNull(0) {
		t.Error("timestamp row 0: should not be null")
	}

	// requestId col[1]
	ridVec := b.Columns[1].(*batch.StringVector)
	if ridVec.Values[0] != "req-0000001" {
		t.Errorf("requestId row 0: got %q, want %q", ridVec.Values[0], "req-0000001")
	}

	// statusCode col[4]
	scVec := b.Columns[4].(*batch.Int32Vector)
	if scVec.Values[0] != 401 {
		t.Errorf("statusCode row 0: got %d, want 401", scVec.Values[0])
	}

	// durationMs col[5]
	durVec := b.Columns[5].(*batch.Float64Vector)
	if durVec.Values[0] != 18.318 {
		t.Errorf("durationMs row 0: got %v, want 18.318", durVec.Values[0])
	}

	// ttl col[7]
	ttlVec := b.Columns[7].(*batch.TimespanVector)
	if ttlVec.Values[0] != time.Hour {
		t.Errorf("ttl row 0: got %v, want 1h", ttlVec.Values[0])
	}
}

func TestFileTableReader_NullValues(t *testing.T) {
	// Row 3 (0-indexed) has empty userId and region:
	// 2026-05-01 00:04:25.160,req-0000004,,login,500,307.935,,8h
	r, err := NewFileTableReader(requestsDir, "requests", time.Time{}, time.Time{}, testBatchSize)
	if err != nil {
		t.Fatalf("NewFileTableReader: %v", err)
	}
	defer r.Close()

	b, err := r.GetNextBatch()
	if err != nil {
		t.Fatalf("GetNextBatch: %v", err)
	}

	// userId col[2] row 3
	uid := b.Columns[2].(*batch.StringVector)
	if !uid.IsNull(3) {
		t.Errorf("userId row 3: expected null, got %q", uid.Values[3])
	}

	// region col[6] row 3
	reg := b.Columns[6].(*batch.StringVector)
	if !reg.IsNull(3) {
		t.Errorf("region row 3: expected null, got %q", reg.Values[3])
	}
}

// --- Time window tests ---

func TestFileTableReader_OneFile(t *testing.T) {
	// end is safely after file[0]'s last row (2026-05-01 11:59:35.188) but
	// before file[1]'s entry timestamp (2026-05-01 12:01:17.310),
	// so only the first file is selected and all 500 rows pass the trailing stop.
	end := time.Date(2026, 5, 1, 11, 59, 59, 999_000_000, time.UTC)
	r, err := NewFileTableReader(requestsDir, "requests", time.Time{}, end, testBatchSize)
	if err != nil {
		t.Fatalf("NewFileTableReader: %v", err)
	}
	defer r.Close()

	got := countAllRows(t, r)
	if got != 500 {
		t.Errorf("one-file window: got %d rows, want 500", got)
	}
}

func TestFileTableReader_LeadingSkip(t *testing.T) {
	// start is 1 ms after the first row (2026-05-01 00:00:11.758), so that row
	// is skipped by the leading filter. The next row is at 2026-05-01 00:02:19.763.
	// Open end → all 14 files selected; only row 0 is pruned → 6999 total.
	start := time.Date(2026, 5, 1, 0, 0, 11, 759_000_000, time.UTC)
	r, err := NewFileTableReader(requestsDir, "requests", start, time.Time{}, testBatchSize)
	if err != nil {
		t.Fatalf("NewFileTableReader: %v", err)
	}
	defer r.Close()

	b, err := r.GetNextBatch()
	if err != nil {
		t.Fatalf("GetNextBatch: %v", err)
	}

	// First row of first batch must be the row at 00:02:19.763 (row 1 of the file).
	wantFirst := time.Date(2026, 5, 1, 0, 2, 19, 763_000_000, time.UTC)
	tsVec := b.Columns[0].(*batch.DatetimeVector)
	if !tsVec.Values[0].Equal(wantFirst) {
		t.Errorf("first row timestamp: got %v, want %v", tsVec.Values[0], wantFirst)
	}

	// Drain the rest and verify total.
	total := b.Length
	for {
		b2, err := r.GetNextBatch()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("GetNextBatch: %v", err)
		}
		total += b2.Length
	}
	if total != 6999 {
		t.Errorf("total rows: got %d, want 6999", total)
	}
}

func TestFileTableReader_BothBounds(t *testing.T) {
	// start is 1 ms after row 0, end safely before file[1]'s entry timestamp.
	// Only file[0] is selected; leading skip drops row 0 → 499 rows.
	start := time.Date(2026, 5, 1, 0, 0, 11, 759_000_000, time.UTC)
	end := time.Date(2026, 5, 1, 11, 59, 59, 999_000_000, time.UTC)
	r, err := NewFileTableReader(requestsDir, "requests", start, end, testBatchSize)
	if err != nil {
		t.Fatalf("NewFileTableReader: %v", err)
	}
	defer r.Close()

	got := countAllRows(t, r)
	if got != 499 {
		t.Errorf("both-bounds window: got %d rows, want 499", got)
	}
}

// --- NewTableReader factory ---

func TestNewTableReader_FileMode(t *testing.T) {
	abs, err := filepath.Abs(requestsDir)
	if err != nil {
		t.Fatal(err)
	}
	entry := &catalog.TableEntry{
		TypeName:      "requests",
		StorageBucket: "requests",
		Mode:          catalog.StorageModeFile,
		OutputDir:     filepath.Dir(abs),
	}
	r, err := NewTableReader(entry, time.Time{}, time.Time{}, testBatchSize)
	if err != nil {
		t.Fatalf("NewTableReader: %v", err)
	}
	defer r.Close()

	got := countAllRows(t, r)
	if got != 7000 {
		t.Errorf("total rows via factory: got %d, want 7000", got)
	}
}

func TestNewTableReader_BlobMode_NotImplemented(t *testing.T) {
	entry := &catalog.TableEntry{
		TypeName: "x",
		Mode:     catalog.StorageModeBlob,
	}
	_, err := NewTableReader(entry, time.Time{}, time.Time{}, 256)
	if err == nil {
		t.Fatal("expected error for blob mode, got nil")
	}
}

// --- Close ---

func TestFileTableReader_Close(t *testing.T) {
	r, err := NewFileTableReader(requestsDir, "requests", time.Time{}, time.Time{}, testBatchSize)
	if err != nil {
		t.Fatalf("NewFileTableReader: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	// Second close should also be safe
	if err := r.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}
