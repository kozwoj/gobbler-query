package physical

import (
	"io"

	"github.com/kozwoj/gobbler-query/query/batch"
)

// CountOp accumulates the total number of input rows and emits one final row.
type CountOp struct {
	Input   Operator
	count   int64
	emitted bool
}

func (op *CountOp) Next() (*batch.Batch, error) {
	if op.emitted {
		return nil, io.EOF
	}

	for {
		b, err := op.Input.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if b == nil {
			continue
		}
		op.count += int64(b.Length)
	}

	op.emitted = true
	return &batch.Batch{
		Length: 1,
		Schema: []batch.ColumnMeta{{Name: "count", Origin: ""}},
		Columns: []batch.ColumnVector{
			&batch.Int64Vector{Values: []int64{op.count}},
		},
	}, nil
}

func (op *CountOp) Close() error {
	if op.Input != nil {
		return op.Input.Close()
	}
	return nil
}
