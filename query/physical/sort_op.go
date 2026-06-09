package physical

import (
	"io"
	"sort"
	"time"

	"github.com/kozwoj/gobbler-query/query/batch"
)

// compiledSortKey records which column drives a sort level and the direction.
type compiledSortKey struct {
	colIdx int
	desc   bool
}

// compareAny returns -1, 0, or +1.
// Null values sort after non-null values regardless of direction.
func compareAny(a, b any, aNull, bNull bool) int {
	switch {
	case aNull && bNull:
		return 0
	case aNull:
		return 1
	case bNull:
		return -1
	}
	switch av := a.(type) {
	case int32:
		bv := b.(int32)
		if av < bv {
			return -1
		} else if av > bv {
			return 1
		}
		return 0
	case int64:
		bv := b.(int64)
		if av < bv {
			return -1
		} else if av > bv {
			return 1
		}
		return 0
	case float64:
		bv := b.(float64)
		if av < bv {
			return -1
		} else if av > bv {
			return 1
		}
		return 0
	case string:
		bv := b.(string)
		if av < bv {
			return -1
		} else if av > bv {
			return 1
		}
		return 0
	case bool:
		bv := b.(bool)
		// false < true
		if !av && bv {
			return -1
		} else if av && !bv {
			return 1
		}
		return 0
	case time.Time:
		bv := b.(time.Time)
		if av.Before(bv) {
			return -1
		} else if av.After(bv) {
			return 1
		}
		return 0
	case time.Duration:
		bv := b.(time.Duration)
		if av < bv {
			return -1
		} else if av > bv {
			return 1
		}
		return 0
	default:
		return 0
	}
}

// compareRows returns true if rowA should sort before rowB according to keys.
// Keys are evaluated in order: if an earlier key produces a definitive result
// (< or >), the remaining keys are not consulted. Only when two rows are equal
// on a key does evaluation advance to the next one.
// Null values always sort last regardless of sort direction.
func compareRows(
	rowA, rowB []any,
	nullsA, nullsB []bool,
	keys []compiledSortKey,
) bool {
	for _, k := range keys {
		aN := nullsA[k.colIdx]
		bN := nullsB[k.colIdx]
		cmp := compareAny(rowA[k.colIdx], rowB[k.colIdx], aN, bN)
		if cmp == 0 {
			continue
		}
		// Apply desc only when both values are non-null.
		// Null ordering is position-independent of direction.
		if k.desc && !aN && !bN {
			cmp = -cmp
		}
		if cmp < 0 {
			return true
		}
		return false
	}
	return false // all keys equal → stable: keep original order
}

// ─── SortOp ──────────────────────────────────────────────────────────────────

const defaultSortBatchSize = 512

// SortOp is a blocking operator that materialises its entire input, sorts it,
// and then emits rows in sorted order one batch at a time.
type SortOp struct {
	Input     Operator
	Keys      []compiledSortKey
	BatchSize int // rows per output batch; defaults to defaultSortBatchSize

	rows   *materializedRows
	offset int
}

func (s *SortOp) Next() (*batch.Batch, error) {
	// First call: drain input, sort, initialise cursor.
	if s.rows == nil {
		if err := s.materialize(); err != nil {
			return nil, err
		}
	}
	if s.offset >= len(s.rows.Rows) {
		return nil, io.EOF
	}
	bs := s.BatchSize
	if bs <= 0 {
		bs = defaultSortBatchSize
	}
	end := s.offset + bs
	if end > len(s.rows.Rows) {
		end = len(s.rows.Rows)
	}
	b := s.rows.buildBatchFromRows(s.offset, end)
	s.offset = end
	return b, nil
}

func (s *SortOp) Close() error {
	s.rows = nil
	return s.Input.Close()
}

// materialize drains the input into m, then sorts m.Rows and m.Nulls together.
func (s *SortOp) materialize() error {
	// Phase 1: consume all input batches into row-major storage.
	// appendBatch converts each columnar batch into [][]any rows so that
	// compareRows can access individual cell values by column index without
	// repeated type assertions against ColumnVector slices.
	m := &materializedRows{}
	for {
		b, err := s.Input.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if err := m.appendBatch(b); err != nil {
			return err
		}
	}

	// Phase 2: sort via an index permutation.
	// m.Rows and m.Nulls are parallel slices — row i's values are in m.Rows[i]
	// and its null flags in m.Nulls[i]. Sorting m.Rows directly (e.g. with
	// sort.SliceStable(m.Rows, less)) would leave m.Nulls in the original order,
	// corrupting the null flags for every row that moved.
	//
	// Instead we sort a plain integer index slice [0, 1, ..., n-1] using the
	// same comparator. Once the index is in sorted order we build new Rows and
	// Nulls slices by reading from the original slices in permuted order.
	// This keeps both slices in sync without copying any cell data.
	keys := s.Keys
	n := len(m.Rows)
	indices := make([]int, n)
	for i := range indices {
		indices[i] = i
	}
	sort.SliceStable(indices, func(i, j int) bool {
		return compareRows(m.Rows[indices[i]], m.Rows[indices[j]], m.Nulls[indices[i]], m.Nulls[indices[j]], keys)
	})
	newRows := make([][]any, n)
	newNulls := make([][]bool, n)
	for i, idx := range indices {
		newRows[i] = m.Rows[idx]
		newNulls[i] = m.Nulls[idx]
	}
	m.Rows = newRows
	m.Nulls = newNulls
	s.rows = m
	return nil
}
