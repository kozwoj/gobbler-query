package source

import (
	"io"
	"os"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"

	"github.com/kozwoj/gobbler-query/query/batch"
)

// blobIntegrationCreds returns (accountName, accountKey) from environment
// variables, or skips the test if they are not set.
//
// Set before running:
//
//	$env:GOBBLER_TEST_ACCOUNT = "gobblerstorage"
//	$env:GOBBLER_TEST_KEY     = "<key>"
func blobIntegrationCreds(t *testing.T) (account, key string) {
	t.Helper()
	account = os.Getenv("GOBBLER_TEST_ACCOUNT")
	key = os.Getenv("GOBBLER_TEST_KEY")
	if account == "" || key == "" {
		t.Skip("GOBBLER_TEST_ACCOUNT / GOBBLER_TEST_KEY not set; skipping blob integration test")
	}
	return
}

func newBlobReader(t *testing.T, container, typeName string, start, end time.Time) *BlobTableReader {
	t.Helper()
	account, key := blobIntegrationCreds(t)
	cred, err := azblob.NewSharedKeyCredential(account, key)
	if err != nil {
		t.Fatalf("credential: %v", err)
	}
	containerURL := "https://" + account + ".blob.core.windows.net/" + container
	r, err := NewBlobTableReader(containerURL, typeName, cred, start, end, testBatchSize, nil)
	if err != nil {
		t.Fatalf("NewBlobTableReader: %v", err)
	}
	return r
}

// TestBlobTableReader_FullScan_RowCount mirrors TestFileTableReader_FullScan_RowCount.
// Requires testdata uploaded via tester/blobupload.
func TestBlobTableReader_FullScan_RowCount(t *testing.T) {
	r := newBlobReader(t, "requests", "requests", time.Time{}, time.Time{})
	defer r.Close()

	got := countAllRows(t, r)
	if got != 7000 {
		t.Errorf("total rows: got %d, want 7000", got)
	}
}

// TestBlobTableReader_BatchSizes mirrors TestFileTableReader_BatchSizes.
func TestBlobTableReader_BatchSizes(t *testing.T) {
	r := newBlobReader(t, "requests", "requests", time.Time{}, time.Time{})
	defer r.Close()

	const wantFull = 27
	const wantLast = 88
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
	}
	if batchCount != wantTotal {
		t.Errorf("batch count: got %d, want %d", batchCount, wantTotal)
	}
	if rowCount != wantFull*testBatchSize+wantLast {
		t.Errorf("row count: got %d, want %d", rowCount, wantFull*testBatchSize+wantLast)
	}
}

// TestBlobTableReader_TimeWindow mirrors TestFileTableReader_TimeWindow_MidRange.
func TestBlobTableReader_TimeWindow(t *testing.T) {
	// Same window as the file-based test: 2026-05-02 to 2026-05-04
	start := time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 4, 23, 59, 59, 0, time.UTC)
	r := newBlobReader(t, "requests", "requests", start, end)
	defer r.Close()

	got := countAllRows(t, r)
	// Verify non-zero; exact count verified by FileTableReader test.
	if got == 0 {
		t.Error("expected rows in window, got 0")
	}
	if got > 7000 {
		t.Errorf("row count %d exceeds total dataset size", got)
	}
}

// TestBlobTableReader_MatchesFileReader checks that BlobTableReader and
// FileTableReader return the same row counts for the full scan and a time window.
func TestBlobTableReader_MatchesFileReader(t *testing.T) {
	cases := []struct {
		name  string
		start time.Time
		end   time.Time
	}{
		{"full", time.Time{}, time.Time{}},
		{"window", time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC), time.Date(2026, 5, 4, 23, 59, 59, 0, time.UTC)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fr, err := NewFileTableReader(requestsDir, "requests", tc.start, tc.end, testBatchSize, nil)
			if err != nil {
				t.Fatalf("NewFileTableReader: %v", err)
			}
			defer fr.Close()
			wantRows := countAllRows(t, fr)

			br := newBlobReader(t, "requests", "requests", tc.start, tc.end)
			defer br.Close()
			gotRows := countAllRows(t, br)

			if gotRows != wantRows {
				t.Errorf("blob rows = %d, file rows = %d", gotRows, wantRows)
			}
		})
	}
}

// TestBlobTableReader_EmptyWindow verifies EOF is returned immediately when
// the time window selects no blobs.
func TestBlobTableReader_EmptyWindow(t *testing.T) {
	start := time.Time{}
	end := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	r := newBlobReader(t, "requests", "requests", start, end)
	defer r.Close()

	b, err := r.GetNextBatch()
	if err != io.EOF {
		t.Fatalf("expected io.EOF, got batch=%v err=%v", b, err)
	}
}

func newBlobReaderWithOpts(t *testing.T, container, typeName string, start, end time.Time, opts *ReaderOptions) *BlobTableReader {
	t.Helper()
	account, key := blobIntegrationCreds(t)
	cred, err := azblob.NewSharedKeyCredential(account, key)
	if err != nil {
		t.Fatalf("credential: %v", err)
	}
	containerURL := "https://" + account + ".blob.core.windows.net/" + container
	r, err := NewBlobTableReader(containerURL, typeName, cred, start, end, testBatchSize, opts)
	if err != nil {
		t.Fatalf("NewBlobTableReader: %v", err)
	}
	return r
}

// TestBlobTableReader_Pred_AllPass mirrors TestFileTableReader_Pred_AllPass.
func TestBlobTableReader_Pred_AllPass(t *testing.T) {
	opts := &ReaderOptions{
		Pred: batch.RowPredicate(func(_ *batch.Batch, _ int) (bool, error) { return true, nil }),
	}
	r := newBlobReaderWithOpts(t, "requests", "requests", time.Time{}, time.Time{}, opts)
	defer r.Close()

	if got := countAllRows(t, r); got != 7000 {
		t.Errorf("total rows: got %d, want 7000", got)
	}
}

// TestBlobTableReader_Pred_WithWantCols mirrors TestFileTableReader_Pred_WithWantCols.
func TestBlobTableReader_Pred_WithWantCols(t *testing.T) {
	pred := batch.RowPredicate(func(b *batch.Batch, row int) (bool, error) {
		sc := b.Columns[4].(*batch.Int32Vector)
		if sc.IsNull(row) {
			return false, nil
		}
		return sc.Values[row] >= 400, nil
	})
	opts := &ReaderOptions{Pred: pred, WantCols: []int{1, 4}}
	r := newBlobReaderWithOpts(t, "requests", "requests", time.Time{}, time.Time{}, opts)
	defer r.Close()

	total := 0
	for {
		b, err := r.GetNextBatch()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("GetNextBatch: %v", err)
		}
		if len(b.Schema) != 2 {
			t.Fatalf("schema length: got %d, want 2", len(b.Schema))
		}
		sc := b.Columns[1].(*batch.Int32Vector)
		for i := 0; i < b.Length; i++ {
			if !sc.IsNull(i) && sc.Values[i] < 400 {
				t.Errorf("row %d: statusCode %d < 400 passed predicate", total+i, sc.Values[i])
			}
		}
		total += b.Length
	}
	if total == 0 {
		t.Error("expected at least one passing row")
	}
}
