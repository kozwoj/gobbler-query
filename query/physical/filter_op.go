package physical

import (
	"time"

	"github.com/kozwoj/gobbler-query/query/batch"
	"github.com/kozwoj/gobbler-query/query/expr"
)

// FilterOp evaluates Pred for every row of each input batch and compacts
// the passing rows into a new dense batch. Batches where no row passes are
// skipped transparently; the operator keeps pulling until it finds passing
// rows or the input is exhausted.
type FilterOp struct {
	Input Operator
	Pred  expr.RowPredicate
}

func (op *FilterOp) Next() (*batch.Batch, error) {
	for {
		b, err := op.Input.Next()
		if err != nil {
			return nil, err // includes io.EOF
		}

		passing := make([]int, 0, b.Length)
		for row := 0; row < b.Length; row++ {
			ok, err := op.Pred(b, row)
			if err != nil {
				return nil, err
			}
			if ok {
				passing = append(passing, row)
			}
		}

		if len(passing) == 0 {
			continue // all rows filtered; pull next batch
		}

		return compact(b, passing), nil
	}
}

func (op *FilterOp) Close() error {
	if op.Input != nil {
		return op.Input.Close()
	}
	return nil
}

// compact builds a new batch containing only the rows at indices passing.
func compact(src *batch.Batch, passing []int) *batch.Batch {
	cols := make([]batch.ColumnVector, len(src.Columns))
	for i, col := range src.Columns {
		cols[i] = compactColumn(col, passing)
	}
	return &batch.Batch{
		Length:  len(passing),
		Schema:  src.Schema,
		Columns: cols,
	}
}

func compactColumn(col batch.ColumnVector, passing []int) batch.ColumnVector {
	n := len(passing)
	switch v := col.(type) {
	case *batch.Int32Vector:
		vals := make([]int32, n)
		for j, i := range passing {
			vals[j] = v.Values[i]
		}
		return &batch.Int32Vector{Values: vals, Nulls: compactNulls(v.Nulls, passing)}
	case *batch.Int64Vector:
		vals := make([]int64, n)
		for j, i := range passing {
			vals[j] = v.Values[i]
		}
		return &batch.Int64Vector{Values: vals, Nulls: compactNulls(v.Nulls, passing)}
	case *batch.Float64Vector:
		vals := make([]float64, n)
		for j, i := range passing {
			vals[j] = v.Values[i]
		}
		return &batch.Float64Vector{Values: vals, Nulls: compactNulls(v.Nulls, passing)}
	case *batch.StringVector:
		vals := make([]string, n)
		for j, i := range passing {
			vals[j] = v.Values[i]
		}
		return &batch.StringVector{Values: vals, Nulls: compactNulls(v.Nulls, passing)}
	case *batch.BoolVector:
		vals := make([]bool, n)
		for j, i := range passing {
			vals[j] = v.Values[i]
		}
		return &batch.BoolVector{Values: vals, Nulls: compactNulls(v.Nulls, passing)}
	case *batch.DatetimeVector:
		vals := make([]time.Time, n)
		for j, i := range passing {
			vals[j] = v.Values[i]
		}
		return &batch.DatetimeVector{Values: vals, Nulls: compactNulls(v.Nulls, passing)}
	case *batch.TimespanVector:
		vals := make([]time.Duration, n)
		for j, i := range passing {
			vals[j] = v.Values[i]
		}
		return &batch.TimespanVector{Values: vals, Nulls: compactNulls(v.Nulls, passing)}
	case *batch.DynamicVector:
		vals := make([]string, n)
		for j, i := range passing {
			vals[j] = v.Values[i]
		}
		return &batch.DynamicVector{Values: vals, Nulls: compactNulls(v.Nulls, passing)}
	default:
		return col
	}
}

// compactNulls builds a new null bitmap containing only the bits at passing indices.
func compactNulls(nulls []uint64, passing []int) []uint64 {
	if len(nulls) == 0 {
		return nil
	}
	n := len(passing)
	result := make([]uint64, (n+63)/64)
	for j, i := range passing {
		if nulls[i/64]>>(uint(i)%64)&1 == 1 {
			result[j/64] |= 1 << (uint(j) % 64)
		}
	}
	return result
}
