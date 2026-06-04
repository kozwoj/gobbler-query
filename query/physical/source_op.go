package physical

import (
	"github.com/kozwoj/gobbler-query/query/batch"
	"github.com/kozwoj/gobbler-query/query/source"
)

// SourceOp wraps a source.TableReader and exposes it as a physical operator.
type SourceOp struct {
	Reader source.TableReader
}

func (op *SourceOp) Next() (*batch.Batch, error) {
	return op.Reader.GetNextBatch()
}

func (op *SourceOp) Close() error {
	return op.Reader.Close()
}
