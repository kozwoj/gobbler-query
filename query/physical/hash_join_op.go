package physical

import (
	"io"

	"github.com/kozwoj/gobbler-query/query/batch"
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
	buildMat   materializedRows // all right-side rows in row-major form
	buildIndex map[string][]int // join key → row indices into buildMat

	// ── join phase state ─────────────────────────────────────────────────────

	leftBatch     *batch.Batch     // current left batch being joined
	leftRow       int              // next row index to join in leftBatch
	pending       materializedRows // output rows accumulated from matches, not yet emitted
	pendingOffset int              // next row to emit from pending
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
		startIdx := len(op.buildMat.Rows)
		if err := op.buildMat.appendBatch(b); err != nil {
			return err
		}
		// Index each newly appended row.
		for i := startIdx; i < len(op.buildMat.Rows); i++ {
			k := groupKey(op.buildMat.Rows[i], op.buildMat.Nulls[i], op.RightKeyIdxs)
			op.buildIndex[k] = append(op.buildIndex[k], i)
		}
	}
	return nil
}

func (op *HashJoinOp) Close() error {
	op.buildMat = materializedRows{}
	op.buildIndex = nil
	op.pending = materializedRows{}
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
		op.pending.Schema = op.OutSchema
		op.pending.ColKinds = op.OutKinds
	}

	bs := op.BatchSize
	if bs <= 0 {
		bs = defaultSortBatchSize
	}

	for {
		// Emit accumulated output rows if available.
		if op.pendingOffset < len(op.pending.Rows) {
			end := op.pendingOffset + bs
			if end > len(op.pending.Rows) {
				end = len(op.pending.Rows)
			}
			b := op.pending.buildBatchFromRows(op.pendingOffset, end)
			op.pendingOffset = end
			return b, nil
		}
		// All pending rows emitted — reset for next round.
		op.pending.Rows = op.pending.Rows[:0]
		op.pending.Nulls = op.pending.Nulls[:0]
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
			leftVals, leftNulls, err := extractRowFromBatch(op.leftBatch, op.leftRow)
			if err != nil {
				return nil, err
			}
			k := groupKey(leftVals, leftNulls, op.LeftKeyIdxs)
			if matches, ok := op.buildIndex[k]; ok {
				for _, rIdx := range matches {
					rightVals := op.buildMat.Rows[rIdx]
					rightNulls := op.buildMat.Nulls[rIdx]
					combined := make([]any, len(leftVals)+len(rightVals))
					combinedNulls := make([]bool, len(leftNulls)+len(rightNulls))
					copy(combined, leftVals)
					copy(combined[len(leftVals):], rightVals)
					copy(combinedNulls, leftNulls)
					copy(combinedNulls[len(leftNulls):], rightNulls)
					op.pending.Rows = append(op.pending.Rows, combined)
					op.pending.Nulls = append(op.pending.Nulls, combinedNulls)
				}
			}
			op.leftRow++
			if len(op.pending.Rows) >= bs {
				break
			}
		}
		// Loop back: either emit the accumulated pending rows, or fetch
		// the next left batch if this one produced no matches.
	}
}

// extractRowFromBatch extracts all values and null flags for a single row from b.
func extractRowFromBatch(b *batch.Batch, row int) ([]any, []bool, error) {
	vals := make([]any, len(b.Columns))
	nulls := make([]bool, len(b.Columns))
	for col, cv := range b.Columns {
		if cv.IsNull(row) {
			nulls[col] = true
			continue
		}
		v, err := extractCell(cv, row)
		if err != nil {
			return nil, nil, err
		}
		vals[col] = v
	}
	return vals, nulls, nil
}
