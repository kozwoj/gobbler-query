package physical

import (
	"io"

	"github.com/kozwoj/gobbler-query/query/batch"
)

// LimitOp passes through at most Remaining rows from its input.
type LimitOp struct {
	Input     Operator
	Remaining int
	closed    bool
}

func (op *LimitOp) Next() (*batch.Batch, error) {
	if op.closed || op.Remaining <= 0 {
		return nil, io.EOF
	}
	if op.Input == nil {
		return nil, io.EOF
	}

	b, err := op.Input.Next()
	if err == io.EOF {
		return nil, io.EOF
	}
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, io.EOF
	}

	keep := op.Remaining
	if keep > b.Length {
		keep = b.Length
	}
	if keep <= 0 {
		return nil, io.EOF
	}

	if keep < b.Length {
		// Build passing index slice [0, 1, ..., keep-1] and reuse compact.
		passing := make([]int, keep)
		for i := range passing {
			passing[i] = i
		}
		truncated, err := compact(b, passing)
		if err != nil {
			return nil, err
		}
		b = truncated
	}

	op.Remaining -= keep
	return b, nil
}

func (op *LimitOp) Close() error {
	op.closed = true
	if op.Input != nil {
		return op.Input.Close()
	}
	return nil
}
