package physical

import (
	"io"

	"github.com/kozwoj/gobbler-query/query/batch"
)

// fakeOperator is a test helper that replays a fixed slice of batches.
type fakeOperator struct {
	batches  []*batch.Batch
	idx      int
	closeErr error
}

func (f *fakeOperator) Next() (*batch.Batch, error) {
	if f.idx >= len(f.batches) {
		return nil, io.EOF
	}
	b := f.batches[f.idx]
	f.idx++
	return b, nil
}

func (f *fakeOperator) Close() error {
	return f.closeErr
}
