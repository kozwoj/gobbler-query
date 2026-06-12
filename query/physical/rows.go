package physical

import (
	"fmt"
	"time"

	"github.com/kozwoj/gobbler-query/query/batch"
	"github.com/kozwoj/gobbler-query/query/source"
)

// VecKind records which concrete ColumnVector type a column uses.
// It is captured from the first batch and used to build correctly-typed
// all-null vectors in buildBatchFromRows.
type VecKind int

const (
	vecInt32 VecKind = iota
	vecInt64
	vecFloat64
	vecString
	vecBool
	vecDatetime
	vecTimespan
	vecDynamic
)

func vecKindOf(cv batch.ColumnVector) (VecKind, error) {
	switch cv.(type) {
	case *batch.Int32Vector:
		return vecInt32, nil
	case *batch.Int64Vector:
		return vecInt64, nil
	case *batch.Float64Vector:
		return vecFloat64, nil
	case *batch.StringVector:
		return vecString, nil
	case *batch.BoolVector:
		return vecBool, nil
	case *batch.DatetimeVector:
		return vecDatetime, nil
	case *batch.TimespanVector:
		return vecTimespan, nil
	case *batch.DynamicVector:
		return vecDynamic, nil
	default:
		return 0, fmt.Errorf("rows: unsupported column type %T", cv)
	}
}

// VecKindFromColumnType converts a source.ColumnType to the matching VecKind.
// Used so CompiledProjectItem.Type can seed buildBatchFromRows when no input
// batch has been seen (all-null column from the very first row).
func VecKindFromColumnType(ct source.ColumnType) VecKind {
	switch ct {
	case source.TypeInt32:
		return vecInt32
	case source.TypeInt64:
		return vecInt64
	case source.TypeFloat64:
		return vecFloat64
	case source.TypeString:
		return vecString
	case source.TypeBool:
		return vecBool
	case source.TypeDatetime:
		return vecDatetime
	case source.TypeTimespan:
		return vecTimespan
	case source.TypeDynamic:
		return vecDynamic
	default:
		return vecString // safe fallback; should not be reached after InferAndValidate
	}
}

// emptyTypedVector builds an all-null ColumnVector of the given kind and length.
func emptyTypedVector(kind VecKind, n int, nullBits []uint64) batch.ColumnVector {
	switch kind {
	case vecInt32:
		return &batch.Int32Vector{Values: make([]int32, n), Nulls: nullBits}
	case vecInt64:
		return &batch.Int64Vector{Values: make([]int64, n), Nulls: nullBits}
	case vecFloat64:
		return &batch.Float64Vector{Values: make([]float64, n), Nulls: nullBits}
	case vecBool:
		return &batch.BoolVector{Values: make([]bool, n), Nulls: nullBits}
	case vecDatetime:
		return &batch.DatetimeVector{Values: make([]time.Time, n), Nulls: nullBits}
	case vecTimespan:
		return &batch.TimespanVector{Values: make([]time.Duration, n), Nulls: nullBits}
	case vecDynamic:
		return &batch.DynamicVector{Values: make([]string, n), Nulls: nullBits}
	default: // vecString
		return &batch.StringVector{Values: make([]string, n), Nulls: nullBits}
	}
}

// materializedRows holds all rows extracted from one or more batches in
// row-major form. It is the shared accumulation store for blocking operators
// (SortOp, HashAggregateOp, HashJoinOp).
type materializedRows struct {
	Schema   []batch.ColumnMeta
	ColKinds []VecKind // concrete vector type per column; set on first appendBatch
	Rows     [][]any   // Rows[i][j] = value at row i, column j; nil when null
	Nulls    [][]bool  // Nulls[i][j] = true when cell (i,j) is null
}

// appendBatch extracts every row from b and appends it to m.
// The schema and column kinds are captured from the first non-empty batch;
// subsequent batches must have the same column count.
// Returns an error if a column has an unrecognised concrete type.
func (m *materializedRows) appendBatch(b *batch.Batch) error {
	if b.Length == 0 {
		return nil
	}
	if m.Schema == nil {
		m.Schema = b.Schema
		m.ColKinds = make([]VecKind, len(b.Columns))
		for i, cv := range b.Columns {
			k, err := vecKindOf(cv)
			if err != nil {
				return err
			}
			m.ColKinds[i] = k
		}
	} else if len(b.Columns) != len(m.Schema) {
		return fmt.Errorf("rows: schema mismatch: expected %d columns, got %d", len(m.Schema), len(b.Columns))
	}
	ncols := len(b.Columns)
	for row := 0; row < b.Length; row++ {
		vals := make([]any, ncols)
		nulls := make([]bool, ncols)
		for col, cv := range b.Columns {
			if cv.IsNull(row) {
				nulls[col] = true
				continue
			}
			v, err := extractCell(cv, row)
			if err != nil {
				return err
			}
			vals[col] = v
		}
		m.Rows = append(m.Rows, vals)
		m.Nulls = append(m.Nulls, nulls)
	}
	return nil
}

// extractCell returns the concrete value at row i of cv.
func extractCell(cv batch.ColumnVector, row int) (any, error) {
	switch v := cv.(type) {
	case *batch.Int32Vector:
		return v.Values[row], nil
	case *batch.Int64Vector:
		return v.Values[row], nil
	case *batch.Float64Vector:
		return v.Values[row], nil
	case *batch.StringVector:
		return v.Values[row], nil
	case *batch.BoolVector:
		return v.Values[row], nil
	case *batch.DatetimeVector:
		return v.Values[row], nil
	case *batch.TimespanVector:
		return v.Values[row], nil
	case *batch.DynamicVector:
		return v.Values[row], nil
	default:
		return nil, fmt.Errorf("rows: unsupported column type %T", cv)
	}
}

// buildBatchFromRows assembles a *batch.Batch from the rows in [start, end).
// It is the inverse of appendBatch: row-major [][]any → columnar ColumnVectors.
// For all-null columns, the correct typed vector is produced using ColKinds.
func (m *materializedRows) buildBatchFromRows(start, end int) *batch.Batch {
	n := end - start
	ncols := len(m.Schema)

	cols := make([]batch.ColumnVector, ncols)
	for col := 0; col < ncols; col++ {
		vals := make([]any, n)
		nullFlags := make([]bool, n)
		for i := 0; i < n; i++ {
			nullFlags[i] = m.Nulls[start+i][col]
			if !nullFlags[i] {
				vals[i] = m.Rows[start+i][col]
			}
		}
		cols[col] = buildVectorTyped(vals, nullFlags, m.ColKinds[col])
	}

	return &batch.Batch{
		Length:  n,
		Schema:  m.Schema,
		Columns: cols,
	}
}
