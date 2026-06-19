package physical

import (
	"io"

	"github.com/kozwoj/gobbler-query/query/batch"
	"github.com/kozwoj/gobbler-query/query/expr"
)

// HashJoinOp is a semi-blocking operator that implements the join stage. The stage joins
// two input subtrees (Left and Right) on equality of the specified key columns. The join is
// performed in two phases:
//
// Right-side build phase (first Next() call): drains the Right subtree entirely
// into a hash table keyed by the join columns.
//
// Join phase: streams the Left subtree batch-by-batch; for each left row,
// looks up matching right rows and emits the combined output rows.
//
// Output schema: left columns followed by right columns (all columns kept).
// Inner join semantics: left rows with no matching right row are dropped.
type HashJoinOp struct {
	Left  Operator
	Right Operator

	// LeftKeyIdxs are column indices into the left schema.
	// RightKeyIdxs are the corresponding column indices into the right (build) schema.
	// Both slices are parallel and have the same length.
	LeftKeyIdxs  []int
	RightKeyIdxs []int

	// OutSchema is the output schema: left columns || right columns.
	// OutKinds is the parallel VecKind slice used by buildBatchFromRows.
	OutSchema []batch.ColumnMeta
	OutKinds  []VecKind

	BatchSize int // rows per output batch; 0 → defaultBatchSize

	// ── right-side build phase state (populated on first Next()) ────────────

	built      bool
	buildMat   rowStore         // all right-side rows in encoded form
	buildIndex map[string][]int // join key → row indices into buildMat

	// ── join phase state ─────────────────────────────────────────────

	leftBatch     *batch.Batch // current left batch being joined
	leftRow       int          // next row index to join in leftBatch
	pending       rowStore     // output rows accumulated from matches, not yet emitted
	pendingOffset int          // next row to emit from pending
}

// buildHashTable drains the Right subtree and loads all rows into buildMat
// and buildIndex. Called once, on the first Next().
func (op *HashJoinOp) buildHashTable() error {
	op.buildIndex = make(map[string][]int)
	for {
		b, err := op.Right.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		startIdx := op.buildMat.rowCount()
		if err := op.buildMat.appendBatch(b); err != nil {
			return err
		}
		for i := startIdx; i < op.buildMat.rowCount(); i++ {
			k := groupKeyFromValues(op.buildMat.keyValues(i, op.RightKeyIdxs))
			op.buildIndex[k] = append(op.buildIndex[k], i)
		}
	}
	return nil
}

func (op *HashJoinOp) Close() error {
	op.buildMat = rowStore{}
	op.buildIndex = nil
	op.pending = rowStore{}
	leftErr := op.Left.Close()
	rightErr := op.Right.Close()
	if leftErr != nil {
		return leftErr
	}
	return rightErr
}

// Next implements the join phase. On the first call it triggers the right-side
// build phase, then streams the left side row-by-row through the hash table.
func (op *HashJoinOp) Next() (*batch.Batch, error) {
	// Right-side build phase: runs exactly once.
	if !op.built {
		if err := op.buildHashTable(); err != nil {
			return nil, err
		}
		op.built = true
		// Seed pending with the output schema/kinds so buildBatchFromRows
		// produces correctly-typed vectors even for all-null columns.
		op.pending.schema = op.OutSchema
		op.pending.kinds = op.OutKinds
	}

	bs := op.BatchSize
	if bs <= 0 {
		bs = defaultSortBatchSize
	}

	for {
		// Emit accumulated output rows if available.
		if op.pendingOffset < op.pending.rowCount() {
			end := op.pendingOffset + bs
			if end > op.pending.rowCount() {
				end = op.pending.rowCount()
			}
			b := op.pending.buildBatchFromRows(op.pendingOffset, end)
			op.pendingOffset = end
			return b, nil
		}
		// All pending rows emitted — reset for next round.
		op.pending.rows = op.pending.rows[:0]
		op.pendingOffset = 0

		// Fetch the next left batch when the current one is exhausted.
		if op.leftBatch == nil || op.leftRow >= op.leftBatch.Length {
			b, err := op.Left.Next()
			if err == io.EOF {
				return nil, io.EOF
			}
			if err != nil {
				return nil, err
			}
			op.leftBatch = b
			op.leftRow = 0
		}

		// Process left rows until we have enough pending output or the batch is done.
		for op.leftRow < op.leftBatch.Length {
			k := groupKeyFromValues(batchKeyValues(op.leftBatch, op.leftRow, op.LeftKeyIdxs))
			if matches, ok := op.buildIndex[k]; ok {
				leftVals := batchRowValues(op.leftBatch, op.leftRow)
				nLeft := len(leftVals)
				nRight := len(op.buildMat.schema)
				for _, rIdx := range matches {
					combined := make([]expr.Value, nLeft+nRight)
					copy(combined, leftVals)
					copy(combined[nLeft:], op.buildMat.rowValues(rIdx))
					op.pending.appendValues(combined)
				}
			}
			op.leftRow++
			if op.pending.rowCount() >= bs {
				break
			}
		}
		// Loop back: either emit the accumulated pending rows, or fetch
		// the next left batch if this one produced no matches.
	}
}

// (extractRowFromBatch removed — replaced by batchRowValues/batchKeyValues in rows.go)
