package physical

import (
	"errors"
	"io"
	"testing"

	"github.com/kozwoj/gobbler-query/query/batch"
)

func TestFilterOp_FiltersRows(t *testing.T) {
	b := &batch.Batch{
		Length:  3,
		Schema:  []batch.ColumnMeta{{Name: "code", Origin: "t"}},
		Columns: []batch.ColumnVector{&batch.Int32Vector{Values: []int32{200, 400, 500}}},
	}
	input := &fakeOperator{batches: []*batch.Batch{b}}

	op := &FilterOp{
		Input: input,
		Pred: func(b *batch.Batch, row int) (bool, error) {
			return b.Columns[0].(*batch.Int32Vector).Values[row] >= 400, nil
		},
	}

	got, err := op.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if got.Length != 2 {
		t.Fatalf("batch Length = %d, want 2", got.Length)
	}
	vec := got.Columns[0].(*batch.Int32Vector)
	if len(vec.Values) != 2 || vec.Values[0] != 400 || vec.Values[1] != 500 {
		t.Errorf("values = %v, want [400 500]", vec.Values)
	}

	_, err = op.Next()
	if err != io.EOF {
		t.Fatalf("second Next() error = %v, want io.EOF", err)
	}
}

func TestFilterOp_SkipsFullyFilteredBatch(t *testing.T) {
	// First batch: all rows filtered. Second batch: one row passes.
	b1 := &batch.Batch{
		Length:  2,
		Schema:  []batch.ColumnMeta{{Name: "code", Origin: "t"}},
		Columns: []batch.ColumnVector{&batch.Int32Vector{Values: []int32{100, 200}}},
	}
	b2 := &batch.Batch{
		Length:  2,
		Schema:  []batch.ColumnMeta{{Name: "code", Origin: "t"}},
		Columns: []batch.ColumnVector{&batch.Int32Vector{Values: []int32{300, 500}}},
	}
	input := &fakeOperator{batches: []*batch.Batch{b1, b2}}

	op := &FilterOp{
		Input: input,
		Pred: func(b *batch.Batch, row int) (bool, error) {
			return b.Columns[0].(*batch.Int32Vector).Values[row] >= 400, nil
		},
	}

	got, err := op.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if got.Length != 1 {
		t.Fatalf("batch Length = %d, want 1", got.Length)
	}
	vec := got.Columns[0].(*batch.Int32Vector)
	if vec.Values[0] != 500 {
		t.Errorf("value = %d, want 500", vec.Values[0])
	}
	// Both input batches must have been consumed.
	if input.idx != 2 {
		t.Errorf("input Next() calls = %d, want 2", input.idx)
	}
}

func TestFilterOp_AllFilteredToEOF(t *testing.T) {
	b := &batch.Batch{
		Length:  2,
		Schema:  []batch.ColumnMeta{{Name: "code", Origin: "t"}},
		Columns: []batch.ColumnVector{&batch.Int32Vector{Values: []int32{100, 200}}},
	}
	input := &fakeOperator{batches: []*batch.Batch{b}}

	op := &FilterOp{
		Input: input,
		Pred:  func(b *batch.Batch, row int) (bool, error) { return false, nil },
	}

	_, err := op.Next()
	if err != io.EOF {
		t.Fatalf("Next() error = %v, want io.EOF", err)
	}
}

func TestFilterOp_PropagatesPredError(t *testing.T) {
	b := &batch.Batch{
		Length:  1,
		Schema:  []batch.ColumnMeta{{Name: "code", Origin: "t"}},
		Columns: []batch.ColumnVector{&batch.Int32Vector{Values: []int32{200}}},
	}
	input := &fakeOperator{batches: []*batch.Batch{b}}
	wantErr := errors.New("eval failed")

	op := &FilterOp{
		Input: input,
		Pred:  func(b *batch.Batch, row int) (bool, error) { return false, wantErr },
	}

	_, err := op.Next()
	if !errors.Is(err, wantErr) {
		t.Fatalf("Next() error = %v, want %v", err, wantErr)
	}
}

func TestFilterOp_Close_Delegates(t *testing.T) {
	input := &fakeOperator{closeErr: io.ErrClosedPipe}
	op := &FilterOp{Input: input}

	if err := op.Close(); err != io.ErrClosedPipe {
		t.Fatalf("Close() error = %v, want %v", err, io.ErrClosedPipe)
	}
}
