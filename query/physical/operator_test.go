package physical

import (
	"errors"
	"io"
	"testing"

	"github.com/kozwoj/gobbler-query/query/batch"
)

type fakeReader struct {
	batch      *batch.Batch
	err        error
	closeErr   error
	closeCalls int
}

func (f *fakeReader) GetNextBatch() (*batch.Batch, error) {
	return f.batch, f.err
}

func (f *fakeReader) Close() error {
	f.closeCalls++
	return f.closeErr
}

func TestSourceOp_Next_DelegatesToReader(t *testing.T) {
	want := &batch.Batch{Length: 1}
	r := &fakeReader{batch: want}
	op := &SourceOp{Reader: r}

	got, err := op.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if got != want {
		t.Fatalf("Next() = %p, want %p", got, want)
	}
}

func TestSourceOp_Next_PropagatesEOF(t *testing.T) {
	r := &fakeReader{err: io.EOF}
	op := &SourceOp{Reader: r}

	got, err := op.Next()
	if err != io.EOF {
		t.Fatalf("Next() error = %v, want %v", err, io.EOF)
	}
	if got != nil {
		t.Fatalf("Next() batch = %v, want nil", got)
	}
}

func TestSourceOp_Close_DelegatesToReader(t *testing.T) {
	wantErr := errors.New("close failed")
	r := &fakeReader{closeErr: wantErr}
	op := &SourceOp{Reader: r}

	if err := op.Close(); !errors.Is(err, wantErr) {
		t.Fatalf("Close() error = %v, want %v", err, wantErr)
	}
	if r.closeCalls != 1 {
		t.Fatalf("Close() calls = %d, want 1", r.closeCalls)
	}
}
