package physical

import (
	"io"
	"testing"

	"github.com/kozwoj/gobbler-query/query/batch"
)

func TestLimitOp_PassesThroughUntilLimit(t *testing.T) {
	input := &fakeOperator{batches: []*batch.Batch{
		{Length: 2},
		{Length: 2},
		{Length: 2},
	}}
	op := &LimitOp{Input: input, Remaining: 3}

	var total int
	for {
		b, err := op.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next() error = %v", err)
		}
		total += b.Length
	}

	if total != 3 {
		t.Fatalf("total rows = %d, want 3", total)
	}
	if input.idx != 2 {
		t.Fatalf("input Next calls = %d, want 2", input.idx)
	}
}

func TestLimitOp_Truncation_CopiesSliceAndPreservesNulls(t *testing.T) {
	// batch has 4 rows; limit is 2. Row 1 is null.
	nulls := []uint64{0b0010} // bit 1 set
	b := &batch.Batch{
		Length: 4,
		Schema: []batch.ColumnMeta{{Name: "v", Origin: "t"}},
		Columns: []batch.ColumnVector{
			&batch.Int32Vector{Values: []int32{10, 20, 30, 40}, Nulls: nulls},
		},
	}
	op := &LimitOp{Input: &fakeOperator{batches: []*batch.Batch{b}}, Remaining: 2}

	got, err := op.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if got.Length != 2 {
		t.Fatalf("Length = %d, want 2", got.Length)
	}
	vec := got.Columns[0].(*batch.Int32Vector)
	if len(vec.Values) != 2 {
		t.Fatalf("Values len = %d, want 2 (fresh copy, not aliased slice)", len(vec.Values))
	}
	if vec.Values[0] != 10 {
		t.Errorf("row 0 = %d, want 10", vec.Values[0])
	}
	// row 1 must still be null
	if !vec.IsNull(1) {
		t.Errorf("row 1 IsNull = false, want true (null bit lost in truncation)")
	}
	// Verify backing array is not aliased: mutating the output must not affect the input.
	vec.Values[0] = 99
	orig := b.Columns[0].(*batch.Int32Vector)
	if orig.Values[0] == 99 {
		t.Errorf("output aliases input backing array — copy was not made")
	}
}

func TestLimitOp_Close_Delegates(t *testing.T) {
	input := &fakeOperator{closeErr: io.ErrClosedPipe}
	op := &LimitOp{Input: input}

	if err := op.Close(); err != io.ErrClosedPipe {
		t.Fatalf("Close() error = %v, want %v", err, io.ErrClosedPipe)
	}
}
