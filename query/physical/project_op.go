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
		schema[i] = batch.ColumnMeta{Name: item.Name, Origin: item.Origin}
		col, err := evalProjectColumn(item.Eval, b)
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
func evalProjectColumn(eval expr.ScalarEval, b *batch.Batch) (batch.ColumnVector, error) {
	n := b.Length
	vals := make([]any, n)
	nullFlags := make([]bool, n)
	for row := 0; row < n; row++ {
		v, isNull, err := eval(b, row)
		if err != nil {
			return nil, err
		}
		vals[row] = v
		nullFlags[row] = isNull
	}
	return buildVector(vals, nullFlags), nil
}

// buildVector creates a typed ColumnVector from a slice of any values.
// The concrete vector type is inferred from the first non-null value.
// If all values are null, a StringVector with all-null bits is returned.
func buildVector(vals []any, nullFlags []bool) batch.ColumnVector {
	for i, v := range vals {
		if nullFlags[i] {
			continue
		}
		switch v.(type) {
		case int32:
			return buildInt32Vector(vals, nullFlags)
		case int64:
			return buildInt64Vector(vals, nullFlags)
		case float64:
			return buildFloat64Vector(vals, nullFlags)
		case string:
			return buildStringVector(vals, nullFlags)
		case bool:
			return buildBoolVector(vals, nullFlags)
		case time.Time:
			return buildDatetimeVector(vals, nullFlags)
		case time.Duration:
			return buildTimespanVector(vals, nullFlags)
		}
	}
	// all null — type unknown; use StringVector as a placeholder
	return &batch.StringVector{Values: make([]string, len(vals)), Nulls: packNullBits(nullFlags)}
}

func buildInt32Vector(vals []any, nullFlags []bool) batch.ColumnVector {
	v := &batch.Int32Vector{Values: make([]int32, len(vals)), Nulls: packNullBits(nullFlags)}
	for i, x := range vals {
		if !nullFlags[i] {
			v.Values[i] = x.(int32)
		}
	}
	return v
}

func buildInt64Vector(vals []any, nullFlags []bool) batch.ColumnVector {
	v := &batch.Int64Vector{Values: make([]int64, len(vals)), Nulls: packNullBits(nullFlags)}
	for i, x := range vals {
		if !nullFlags[i] {
			v.Values[i] = x.(int64)
		}
	}
	return v
}

func buildFloat64Vector(vals []any, nullFlags []bool) batch.ColumnVector {
	v := &batch.Float64Vector{Values: make([]float64, len(vals)), Nulls: packNullBits(nullFlags)}
	for i, x := range vals {
		if !nullFlags[i] {
			v.Values[i] = x.(float64)
		}
	}
	return v
}

func buildStringVector(vals []any, nullFlags []bool) batch.ColumnVector {
	v := &batch.StringVector{Values: make([]string, len(vals)), Nulls: packNullBits(nullFlags)}
	for i, x := range vals {
		if !nullFlags[i] {
			v.Values[i] = x.(string)
		}
	}
	return v
}

func buildBoolVector(vals []any, nullFlags []bool) batch.ColumnVector {
	v := &batch.BoolVector{Values: make([]bool, len(vals)), Nulls: packNullBits(nullFlags)}
	for i, x := range vals {
		if !nullFlags[i] {
			v.Values[i] = x.(bool)
		}
	}
	return v
}

func buildDatetimeVector(vals []any, nullFlags []bool) batch.ColumnVector {
	v := &batch.DatetimeVector{Values: make([]time.Time, len(vals)), Nulls: packNullBits(nullFlags)}
	for i, x := range vals {
		if !nullFlags[i] {
			v.Values[i] = x.(time.Time)
		}
	}
	return v
}

func buildTimespanVector(vals []any, nullFlags []bool) batch.ColumnVector {
	v := &batch.TimespanVector{Values: make([]time.Duration, len(vals)), Nulls: packNullBits(nullFlags)}
	for i, x := range vals {
		if !nullFlags[i] {
			v.Values[i] = x.(time.Duration)
		}
	}
	return v
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
