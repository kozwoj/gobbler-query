package physical

import (
	"io"
	"testing"

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

// strBatch builds a single-column string batch.
func strBatch(origin, col string, vals ...string) *batch.Batch {
	return &batch.Batch{
		Length:  len(vals),
		Schema:  []batch.ColumnMeta{{Name: col, Origin: origin}},
		Columns: []batch.ColumnVector{&batch.StringVector{Values: vals}},
	}
}

// twoCols builds a two-column batch: first column string key, second column int32 value.
func twoCols(origin, keyCol, valCol string, keys []string, vals []int32) *batch.Batch {
	return &batch.Batch{
		Length: len(keys),
		Schema: []batch.ColumnMeta{
			{Name: keyCol, Origin: origin},
			{Name: valCol, Origin: origin},
		},
		Columns: []batch.ColumnVector{
			&batch.StringVector{Values: keys},
			&batch.Int32Vector{Values: vals},
		},
	}
}

// drainAll collects every output row from op into ([][]any, [][]bool).
func drainAll(t *testing.T, op Operator) (rows [][]any, nulls [][]bool) {
	t.Helper()
	for {
		b, err := op.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next() error: %v", err)
		}
		for row := 0; row < b.Length; row++ {
			rVals := make([]any, len(b.Columns))
			rNulls := make([]bool, len(b.Columns))
			for col, cv := range b.Columns {
				if cv.IsNull(row) {
					rNulls[col] = true
				} else {
					v, err := extractCell(cv, row)
					if err != nil {
						t.Fatalf("extractCell: %v", err)
					}
					rVals[col] = v
				}
			}
			rows = append(rows, rVals)
			nulls = append(nulls, rNulls)
		}
	}
	return
}
