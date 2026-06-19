package physical

import (
	"time"

	"github.com/kozwoj/gobbler-query/query/batch"
	"github.com/kozwoj/gobbler-query/query/expr"
)

// ProjectOp selects, renames, and/or computes output columns per batch.
// Items is built by the planner from the validated LogicalProject node.
type ProjectOp struct {
	Input Operator
	Items []expr.CompiledProjectItem
}

func (op *ProjectOp) Next() (*batch.Batch, error) {
	b, err := op.Input.Next()
	if err != nil {
		return nil, err
	}

	schema := make([]batch.ColumnMeta, len(op.Items))
	cols := make([]batch.ColumnVector, len(op.Items))

	for i, item := range op.Items {
		schema[i] = batch.ColumnMeta{Name: item.Name, Origin: item.Origin, Type: item.Type}
		col, err := evalProjectColumn(item.Eval, VecKindFromColumnType(item.Type), b)
		if err != nil {
			return nil, err
		}
		cols[i] = col
	}

	return &batch.Batch{Length: b.Length, Schema: schema, Columns: cols}, nil
}

func (op *ProjectOp) Close() error {
	if op.Input != nil {
		return op.Input.Close()
	}
	return nil
}

// evalProjectColumn evaluates eval for every row of b and returns a typed ColumnVector.
// kind is used to produce the correct all-null vector when every row evaluates to null.
func evalProjectColumn(eval expr.ScalarEval, kind VecKind, b *batch.Batch) (batch.ColumnVector, error) {
	n := b.Length
	vals := make([]expr.Value, n)
	for row := 0; row < n; row++ {
		v, err := eval(b, row)
		if err != nil {
			return nil, err
		}
		vals[row] = v
	}
	return buildVectorFromValues(vals, kind), nil
}

// buildVectorFromValues builds a typed ColumnVector from a slice of Value.
// All-null columns use kind to choose the correct vector type.
func buildVectorFromValues(vals []expr.Value, kind VecKind) batch.ColumnVector {
	n := len(vals)
	nullFlags := make([]bool, n)
	for i, v := range vals {
		nullFlags[i] = v.Kind == expr.KindNull
	}
	// Determine the actual kind from the first non-null value.
	actualKind := kind
	for _, v := range vals {
		if v.Kind != expr.KindNull {
			actualKind = vecKindFromValueKind(v.Kind)
			break
		}
	}
	nullBits := packNullBits(nullFlags)
	switch actualKind {
	case vecInt32:
		out := &batch.Int32Vector{Values: make([]int32, n), Nulls: nullBits}
		for i, v := range vals {
			if !nullFlags[i] {
				out.Values[i] = int32(v.I)
			}
		}
		return out
	case vecInt64:
		out := &batch.Int64Vector{Values: make([]int64, n), Nulls: nullBits}
		for i, v := range vals {
			if !nullFlags[i] {
				out.Values[i] = v.I
			}
		}
		return out
	case vecFloat64:
		out := &batch.Float64Vector{Values: make([]float64, n), Nulls: nullBits}
		for i, v := range vals {
			if !nullFlags[i] {
				out.Values[i] = v.F
			}
		}
		return out
	case vecString:
		out := &batch.StringVector{Values: make([]string, n), Nulls: nullBits}
		for i, v := range vals {
			if !nullFlags[i] {
				out.Values[i] = v.S
			}
		}
		return out
	case vecBool:
		out := &batch.BoolVector{Values: make([]bool, n), Nulls: nullBits}
		for i, v := range vals {
			if !nullFlags[i] {
				out.Values[i] = v.I != 0
			}
		}
		return out
	case vecDatetime:
		out := &batch.DatetimeVector{Values: make([]time.Time, n), Nulls: nullBits}
		for i, v := range vals {
			if !nullFlags[i] {
				out.Values[i] = time.Unix(0, v.I)
			}
		}
		return out
	case vecTimespan:
		out := &batch.TimespanVector{Values: make([]time.Duration, n), Nulls: nullBits}
		for i, v := range vals {
			if !nullFlags[i] {
				out.Values[i] = time.Duration(v.I)
			}
		}
		return out
	default: // vecDynamic
		out := &batch.DynamicVector{Values: make([]string, n), Nulls: nullBits}
		for i, v := range vals {
			if !nullFlags[i] {
				out.Values[i] = v.S
			}
		}
		return out
	}
}

// vecKindFromValueKind maps a ValueKind to the corresponding VecKind.
func vecKindFromValueKind(k expr.ValueKind) VecKind {
	switch k {
	case expr.KindInt32:
		return vecInt32
	case expr.KindInt64:
		return vecInt64
	case expr.KindFloat64:
		return vecFloat64
	case expr.KindString:
		return vecString
	case expr.KindBool:
		return vecBool
	case expr.KindDatetime:
		return vecDatetime
	case expr.KindTimespan:
		return vecTimespan
	default:
		return vecDynamic
	}
}

// packNullBits converts a bool slice into a packed uint64 bitset.
// Returns nil when no values are null (consistent with batch.isNull semantics).
func packNullBits(nullFlags []bool) []uint64 {
	if len(nullFlags) == 0 {
		return nil
	}
	bits := make([]uint64, (len(nullFlags)+63)/64)
	hasNull := false
	for i, n := range nullFlags {
		if n {
			bits[i/64] |= 1 << uint(i%64)
			hasNull = true
		}
	}
	if !hasNull {
		return nil
	}
	return bits
}
