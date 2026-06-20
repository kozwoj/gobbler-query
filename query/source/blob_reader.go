package source

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"

	"github.com/kozwoj/gobbler-query/query/batch"
)

// BlobTableReader reads CSV blobs from an Azure Blob Storage container as a
// single ordered row stream. It mirrors FileTableReader: blobs are selected by
// time-window pruning (same filename convention as local files), read in
// ascending entry-timestamp order, and batches cross blob boundaries
// transparently.
//
// The container must contain:
//   - exactly one schema blob named "<typeName>.json"
//   - zero or more data blobs named "<timestamp>_<typeName>.csv"
type BlobTableReader struct {
	containerClient *container.Client
	blobs           []string    // selected blob names in ascending timestamp order
	blobIdx         int         // index of the currently open blob in blobs
	blobReader      io.Closer   // active download body; nil between blobs
	csv             *csv.Reader // wraps blobReader
	schema          *Schema
	typeName        string
	batchSize       int
	start           time.Time
	end             time.Time
	colBuilders     []columnBuilder
	pendingRow      []string
	done            bool
}

// NewBlobTableReader constructs a BlobTableReader.
//   - containerURL: full URL of the Azure Blob Storage container.
//   - typeName: stored as Origin in every ColumnMeta this reader emits.
//   - cred: shared-key credential for the storage account.
//   - start, end: time-window bounds (zero = open bound).
//   - batchSize: maximum rows per batch.
//
// Downloads "<typeName>.json" from the container to obtain the schema, then
// lists and prunes blobs to the time window. Opens the first selected blob
// during construction so schema field-count errors are caught early.
func NewBlobTableReader(
	containerURL, typeName string,
	cred *azblob.SharedKeyCredential,
	start, end time.Time,
	batchSize int,
) (*BlobTableReader, error) {
	cc, err := container.NewClientWithSharedKeyCredential(containerURL, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("NewBlobTableReader: container client: %w", err)
	}

	ctx := context.Background()

	// Download and parse schema blob.
	schemaBlob := typeName + ".json"
	dlResp, err := cc.NewBlobClient(schemaBlob).DownloadStream(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("NewBlobTableReader: download %s: %w", schemaBlob, err)
	}
	schemaData, err := io.ReadAll(dlResp.Body)
	dlResp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("NewBlobTableReader: read %s: %w", schemaBlob, err)
	}
	schema, err := parseSchema(schemaData)
	if err != nil {
		return nil, fmt.Errorf("NewBlobTableReader: %w", err)
	}

	// List all CSV blobs for this type name.
	suffix := "_" + typeName + ".csv"
	var names []string
	pager := cc.NewListBlobsFlatPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("NewBlobTableReader: list blobs: %w", err)
		}
		for _, item := range page.Segment.BlobItems {
			if strings.HasSuffix(*item.Name, suffix) {
				names = append(names, *item.Name)
			}
		}
	}

	// Prune to the time window using the same logic as FileTableReader.
	selected := selectEntries(names, start, end)

	r := &BlobTableReader{
		containerClient: cc,
		blobs:           selected,
		schema:          schema,
		typeName:        typeName,
		batchSize:       batchSize,
		start:           start,
		end:             end,
		colBuilders:     newColumnBuilders(schema, batchSize),
	}

	if len(selected) == 0 {
		r.done = true
		return r, nil
	}

	if err := r.openCurrentBlob(); err != nil {
		return nil, err
	}
	return r, nil
}

// openCurrentBlob downloads r.blobs[r.blobIdx] and reads its first row for
// field-count validation. Empty blobs are skipped automatically.
func (r *BlobTableReader) openCurrentBlob() error {
	blobName := r.blobs[r.blobIdx]
	ctx := context.Background()
	dlResp, err := r.containerClient.NewBlobClient(blobName).DownloadStream(ctx, nil)
	if err != nil {
		return fmt.Errorf("%s: download: %w", blobName, err)
	}

	csvr := csv.NewReader(dlResp.Body)
	csvr.FieldsPerRecord = -1
	rec, err := csvr.Read()
	if err == io.EOF {
		// Empty blob — skip it.
		dlResp.Body.Close()
		return r.advanceBlob()
	}
	if err != nil {
		dlResp.Body.Close()
		return fmt.Errorf("%s: %w", blobName, err)
	}
	if err := validateFieldCount(r.schema, rec); err != nil {
		dlResp.Body.Close()
		return fmt.Errorf("%s: %w", blobName, err)
	}

	r.blobReader = dlResp.Body
	r.csv = csvr
	r.pendingRow = rec
	return nil
}

// advanceBlob closes the current blob stream and opens the next one.
// Sets r.done = true when all blobs have been read.
func (r *BlobTableReader) advanceBlob() error {
	if r.blobReader != nil {
		r.blobReader.Close()
		r.blobReader = nil
		r.csv = nil
	}
	r.blobIdx++
	if r.blobIdx >= len(r.blobs) {
		r.done = true
		return nil
	}
	return r.openCurrentBlob()
}

// nextRow returns the next CSV record, draining pendingRow first.
func (r *BlobTableReader) nextRow() ([]string, error) {
	if r.pendingRow != nil {
		row := r.pendingRow
		r.pendingRow = nil
		return row, nil
	}
	return r.csv.Read()
}

// GetNextBatch returns the next dense batch of up to batchSize rows.
// Returns (nil, io.EOF) when the reader is exhausted.
func (r *BlobTableReader) GetNextBatch() (*batch.Batch, error) {
	if r.done {
		return nil, io.EOF
	}

	rows := 0
	for rows < r.batchSize {
		rec, err := r.nextRow()
		if err == io.EOF {
			if advErr := r.advanceBlob(); advErr != nil {
				return nil, advErr
			}
			if r.done {
				break
			}
			continue
		}
		if err != nil {
			return nil, err
		}

		// Leading skip: first selected blob only, skip rows before start.
		if !r.start.IsZero() && r.blobIdx == 0 {
			ts, _ := time.Parse(datetimeFormat, rec[0])
			if ts.Before(r.start) {
				continue
			}
		}

		// Trailing stop: last selected blob only, stop when past end.
		if !r.end.IsZero() && r.blobIdx == len(r.blobs)-1 {
			ts, _ := time.Parse(datetimeFormat, rec[0])
			if ts.After(r.end) {
				r.done = true
				break
			}
		}

		for i, b := range r.colBuilders {
			b.Append(rec[i])
		}
		rows++
	}

	if rows == 0 {
		return nil, io.EOF
	}

	cols := make([]batch.ColumnVector, len(r.schema.Columns))
	meta := make([]batch.ColumnMeta, len(r.schema.Columns))
	for i, cb := range r.colBuilders {
		cols[i] = cb.FinalizeColumn(rows)
		meta[i] = batch.ColumnMeta{Name: r.schema.Columns[i].Name, Origin: r.typeName, Type: r.schema.Columns[i].Type}
		cb.Reset()
	}
	return &batch.Batch{Length: rows, Schema: meta, Columns: cols}, nil
}

// Close releases the active blob download stream.
func (r *BlobTableReader) Close() error {
	if r.blobReader != nil {
		err := r.blobReader.Close()
		r.blobReader = nil
		r.csv = nil
		return err
	}
	return nil
}
