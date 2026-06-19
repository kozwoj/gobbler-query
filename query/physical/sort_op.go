package physical

import (
	"io"
	"sort"
	"time"

	"github.com/kozwoj/gobbler-query/query/batch"
)

// CompiledSortKey records which column drives a sort level and the direction.
type CompiledSortKey struct {
	ColIdx int
	Desc   bool
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
	keys []CompiledSortKey,
) bool {
	for _, k := range keys {
		aN := nullsA[k.ColIdx]
		bN := nullsB[k.ColIdx]
		cmp := compareAny(rowA[k.ColIdx], rowB[k.ColIdx], aN, bN)
		if cmp == 0 {
			continue
		}
		// Apply desc only when both values are non-null.
		// Null ordering is position-independent of direction.
		if k.Desc && !aN && !bN {
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
	Keys      []CompiledSortKey
	BatchSize int // rows per output batch; defaults to defaultSortBatchSize

	rows   *rowStore
	offset int
}

func (s *SortOp) Next() (*batch.Batch, error) {
	// First call: drain input, sort, initialise cursor.
	if s.rows == nil {
		if err := s.materialize(); err != nil {
			return nil, err
		}
	}
	if s.offset >= s.rows.rowCount() {
		return nil, io.EOF
	}
	bs := s.BatchSize
	if bs <= 0 {
		bs = defaultSortBatchSize
	}
	end := s.offset + bs
	if end > s.rows.rowCount() {
		end = s.rows.rowCount()
	}
	b := s.rows.buildBatchFromRows(s.offset, end)
	s.offset = end
	return b, nil
}

func (s *SortOp) Close() error {
	s.rows = nil
	return s.Input.Close()
}

// materialize drains the input into m, then reorders m.rows via index permutation.
func (s *SortOp) materialize() error {
	m := &rowStore{}
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

	// Sort via an index permutation so the encodedRow slice stays contiguous.
	keys := s.Keys
	n := m.rowCount()
	indices := make([]int, n)
	for i := range indices {
		indices[i] = i
	}
	sort.SliceStable(indices, func(i, j int) bool {
		return m.compare(indices[i], indices[j], keys)
	})
	sorted := make([]encodedRow, n)
	for i, idx := range indices {
		sorted[i] = m.rows[idx]
	}
	m.rows = sorted
	s.rows = m
	return nil
}
