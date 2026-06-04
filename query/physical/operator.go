package physical

import "github.com/kozwoj/gobbler-query/query/batch"

// Operator is the pull-based interface implemented by every physical operator.
type Operator interface {
	Next() (*batch.Batch, error)
	Close() error
}
