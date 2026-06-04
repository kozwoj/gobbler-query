package physical

import (
	"io"
	"testing"

	"github.com/kozwoj/gobbler-query/query/batch"
)

func TestCountOp_EmitsFinalCount(t *testing.T) {
	input := &fakeOperator{batches: []*batch.Batch{
		{Length: 2},
		{Length: 3},
	}}
	op := &CountOp{Input: input}

	b, err := op.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if b.Length != 1 {
		t.Fatalf("batch length = %d, want 1", b.Length)
	}

	vec, ok := b.Columns[0].(*batch.Int64Vector)
	if !ok {
		t.Fatalf("column type = %T, want *batch.Int64Vector", b.Columns[0])
	}
	if got := vec.Values[0]; got != 5 {
		t.Fatalf("count value = %d, want 5", got)
	}

	b, err = op.Next()
	if err != io.EOF {
		t.Fatalf("second Next() error = %v, want %v", err, io.EOF)
	}
	if b != nil {
		t.Fatalf("second Next() batch = %v, want nil", b)
	}
}

func TestCountOp_Close_Delegates(t *testing.T) {
	input := &fakeOperator{closeErr: io.ErrClosedPipe}
	op := &CountOp{Input: input}

	if err := op.Close(); err != io.ErrClosedPipe {
		t.Fatalf("Close() error = %v, want %v", err, io.ErrClosedPipe)
	}
}
