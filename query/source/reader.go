package source

import (
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/kozwoj/gobbler-query/query/batch"
	"github.com/kozwoj/gobbler-query/query/catalog"
)

// TableReader is the interface implemented by FileTableReader and BlobTableReader.
// GetNextBatch returns the next dense batch of rows, returning (nil, io.EOF)
// when the sequence is exhausted. Any other error is a hard failure.
type TableReader interface {
	GetNextBatch() (*batch.Batch, error)
	Close() error
}

// ReaderOptions carries optional predicate and column-projection hints that
// the reader applies inside GetNextBatch. Both fields are nil by default,
// which preserves the existing full-width, unfiltered behaviour.
type ReaderOptions struct {
	// Pred is evaluated on each candidate batch. Only passing rows are kept.
	// nil means accept all rows.
	Pred batch.RowPredicate
	// WantCols is the sorted list of column indices (into the source schema)
	// that must appear in the output batch. nil means all columns.
	WantCols []int
}

// NewTableReader constructs the appropriate TableReader based on entry.Mode.
// entry.TypeName must be set. opts may be nil.
func NewTableReader(entry *catalog.TableEntry, start, end time.Time, batchSize int, opts *ReaderOptions) (TableReader, error) {
	switch entry.Mode {
	case catalog.StorageModeFile:
		typeDir, err := entry.Resolve()
		if err != nil {
			return nil, err
		}
		return NewFileTableReader(typeDir, entry.TypeName, start, end, batchSize, opts)
	case catalog.StorageModeBlob:
		cred, err := azblob.NewSharedKeyCredential(entry.AccountName, entry.AccountKey)
		if err != nil {
			return nil, fmt.Errorf("NewTableReader: blob credential: %w", err)
		}
		containerURL, err := entry.Resolve()
		if err != nil {
			return nil, err
		}
		return NewBlobTableReader(containerURL, entry.TypeName, cred, start, end, batchSize, opts)
	default:
		return nil, fmt.Errorf("NewTableReader: unknown storage mode %d", entry.Mode)
	}
}
