package physical

import (
	"io"

	"github.com/kozwoj/gobbler-query/query/batch"
)

// LimitOp passes through at most Remaining rows from its input.
type LimitOp struct {
	Input     Operator
	Remaining int
	emitted   int
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
		b2 := *b
		b2.Length = keep
		b2.Columns = make([]batch.ColumnVector, len(b.Columns))
		for i, col := range b.Columns {
			switch v := col.(type) {
			case *batch.Int32Vector:
				b2.Columns[i] = &batch.Int32Vector{Values: v.Values[:keep]}
			case *batch.Int64Vector:
				b2.Columns[i] = &batch.Int64Vector{Values: v.Values[:keep]}
			case *batch.Float64Vector:
				b2.Columns[i] = &batch.Float64Vector{Values: v.Values[:keep]}
			case *batch.StringVector:
				b2.Columns[i] = &batch.StringVector{Values: v.Values[:keep]}
			case *batch.BoolVector:
				b2.Columns[i] = &batch.BoolVector{Values: v.Values[:keep]}
			case *batch.DatetimeVector:
				b2.Columns[i] = &batch.DatetimeVector{Values: v.Values[:keep]}
			case *batch.TimespanVector:
				b2.Columns[i] = &batch.TimespanVector{Values: v.Values[:keep]}
			case *batch.DynamicVector:
				b2.Columns[i] = &batch.DynamicVector{Values: v.Values[:keep]}
			default:
				b2.Columns[i] = col
			}
		}
		b = &b2
	}

	op.Remaining -= keep
	op.emitted += keep
	return b, nil
}

func (op *LimitOp) Close() error {
	op.closed = true
	if op.Input != nil {
		return op.Input.Close()
	}
	return nil
}
