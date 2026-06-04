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

func TestLimitOp_Close_Delegates(t *testing.T) {
	input := &fakeOperator{closeErr: io.ErrClosedPipe}
	op := &LimitOp{Input: input}

	if err := op.Close(); err != io.ErrClosedPipe {
		t.Fatalf("Close() error = %v, want %v", err, io.ErrClosedPipe)
	}
}
