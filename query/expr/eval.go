package expr

import (
	"fmt"
	"strings"
	"time"

	"github.com/kozwoj/gobbler-query/query/ast"
	"github.com/kozwoj/gobbler-query/query/batch"
)

// RowPredicate evaluates a single row from a batch.
// Returns true if the row passes the predicate, false if it should be excluded.
// A non-nil error indicates an unrecoverable evaluation failure (type mismatch,
// missing column, etc.).
type RowPredicate func(b *batch.Batch, row int) (bool, error)

// Compile compiles an ast.BoolExpr into a RowPredicate.
// The returned predicate captures only the expression tree; column resolution
// happens at evaluation time against the batch schema.
func Compile(e ast.BoolExpr) (RowPredicate, error) {
	switch n := e.(type) {
	case *ast.AndExpr:
		left, err := Compile(n.Left)
		if err != nil {
			return nil, err
		}
		right, err := Compile(n.Right)
		if err != nil {
			return nil, err
		}
		return func(b *batch.Batch, row int) (bool, error) {
			lv, err := left(b, row)
			if err != nil || !lv {
				return false, err
			}
			return right(b, row)
		}, nil

	case *ast.OrExpr:
		left, err := Compile(n.Left)
		if err != nil {
			return nil, err
		}
		right, err := Compile(n.Right)
		if err != nil {
			return nil, err
		}
		return func(b *batch.Batch, row int) (bool, error) {
			lv, err := left(b, row)
			if err != nil {
				return false, err
			}
			if lv {
				return true, nil
			}
			return right(b, row)
		}, nil

	case *ast.NotExpr:
		inner, err := Compile(n.Expr)
		if err != nil {
			return nil, err
		}
		return func(b *batch.Batch, row int) (bool, error) {
			v, err := inner(b, row)
			if err != nil {
				return false, err
			}
			return !v, nil
		}, nil

	case *ast.CompareExpr:
		return compileCompare(n), nil

	case *ast.IsNullExpr:
		return compileIsNull(n), nil

	case *ast.InExpr, *ast.BetweenExpr:
		return nil, fmt.Errorf("expr: %T not yet implemented", e)

	default:
		return nil, fmt.Errorf("expr: unsupported BoolExpr type %T", e)
	}
}

// columnIndex returns the zero-based index of the column matching ref
// within the schema, or an error if not found or ambiguous.
func columnIndex(schema []batch.ColumnMeta, ref ast.FieldRef) (int, error) {
	found := -1
	for i, col := range schema {
		if col.Name != ref.Name {
			continue
		}
		if ref.Table != "" && col.Origin != ref.Table {
			continue
		}
		if found != -1 {
			return -1, fmt.Errorf("expr: ambiguous column %q; qualify with table name", ref.Name)
		}
		found = i
	}
	if found == -1 {
		if ref.Table != "" {
			return -1, fmt.Errorf("expr: column %q.%q not found", ref.Table, ref.Name)
		}
		return -1, fmt.Errorf("expr: column %q not found", ref.Name)
	}
	return found, nil
}

// evalScalar evaluates an ast.ScalarExpr for one row.
// Returns (value, isNull, error). The concrete value type is one of:
// int32, int64, float64, string, bool, time.Time, time.Duration.
func evalScalar(e ast.ScalarExpr, b *batch.Batch, row int) (any, bool, error) {
	switch s := e.(type) {
	case *ast.FieldRefExpr:
		idx, err := columnIndex(b.Schema, s.Ref)
		if err != nil {
			return nil, false, err
		}
		col := b.Columns[idx]
		if col.IsNull(row) {
			return nil, true, nil
		}
		switch v := col.(type) {
		case *batch.Int32Vector:
			return v.Values[row], false, nil
		case *batch.Int64Vector:
			return v.Values[row], false, nil
		case *batch.Float64Vector:
			return v.Values[row], false, nil
		case *batch.StringVector:
			return v.Values[row], false, nil
		case *batch.BoolVector:
			return v.Values[row], false, nil
		case *batch.DatetimeVector:
			return v.Values[row], false, nil
		case *batch.TimespanVector:
			return v.Values[row], false, nil
		case *batch.DynamicVector:
			return v.Values[row], false, nil
		default:
			return nil, false, fmt.Errorf("expr: unsupported column type %T", col)
		}

	case *ast.IntLit:
		return s.Value, false, nil
	case *ast.FloatLit:
		return s.Value, false, nil
	case *ast.StringLit:
		return s.Value, false, nil
	case *ast.BoolLit:
		return s.Value, false, nil
	case *ast.DatetimeLit:
		return s.Value, false, nil
	case *ast.AgoExpr:
		return time.Now().Add(-s.Duration.Duration), false, nil

	default:
		return nil, false, fmt.Errorf("expr: unsupported ScalarExpr type %T", e)
	}
}

func compileCompare(e *ast.CompareExpr) RowPredicate {
	return func(b *batch.Batch, row int) (bool, error) {
		lv, lNull, err := evalScalar(e.Left, b, row)
		if err != nil {
			return false, err
		}
		rv, rNull, err := evalScalar(e.Right, b, row)
		if err != nil {
			return false, err
		}
		if lNull || rNull {
			return false, nil // null propagates to false
		}
		return compareValues(lv, e.Op, rv)
	}
}

func compileIsNull(e *ast.IsNullExpr) RowPredicate {
	return func(b *batch.Batch, row int) (bool, error) {
		idx, err := columnIndex(b.Schema, e.Field)
		if err != nil {
			return false, err
		}
		col := b.Columns[idx]
		isNull := col.IsNull(row)

		switch e.Kind {
		case ast.KindIsNull:
			return isNull, nil
		case ast.KindIsNotNull:
			return !isNull, nil
		case ast.KindIsEmpty:
			if isNull {
				return true, nil
			}
			if sv, ok := col.(*batch.StringVector); ok {
				return sv.Values[row] == "", nil
			}
			return false, nil
		default:
			return false, fmt.Errorf("expr: unknown IsNullKind %v", e.Kind)
		}
	}
}

// compareValues compares two scalar values with numeric type promotion.
// Promotion rules: int32 → int64; int64 → float64 when the other side is float64.
func compareValues(left any, op ast.CompareOp, right any) (bool, error) {
	if v, ok := left.(int32); ok {
		left = int64(v)
	}
	if v, ok := right.(int32); ok {
		right = int64(v)
	}
	switch left.(type) {
	case int64:
		if _, ok := right.(float64); ok {
			left = float64(left.(int64))
		}
	case float64:
		if r, ok := right.(int64); ok {
			right = float64(r)
		}
	}

	switch l := left.(type) {
	case int64:
		r, ok := right.(int64)
		if !ok {
			return false, fmt.Errorf("expr: type mismatch: int64 vs %T", right)
		}
		return cmpOrdered(l, op, r)
	case float64:
		r, ok := right.(float64)
		if !ok {
			return false, fmt.Errorf("expr: type mismatch: float64 vs %T", right)
		}
		return cmpOrdered(l, op, r)
	case string:
		r, ok := right.(string)
		if !ok {
			return false, fmt.Errorf("expr: type mismatch: string vs %T", right)
		}
		return cmpString(l, op, r)
	case bool:
		r, ok := right.(bool)
		if !ok {
			return false, fmt.Errorf("expr: type mismatch: bool vs %T", right)
		}
		return cmpBool(l, op, r)
	case time.Time:
		r, ok := right.(time.Time)
		if !ok {
			return false, fmt.Errorf("expr: type mismatch: time.Time vs %T", right)
		}
		return cmpOrdered(l.UnixNano(), op, r.UnixNano())
	case time.Duration:
		r, ok := right.(time.Duration)
		if !ok {
			return false, fmt.Errorf("expr: type mismatch: time.Duration vs %T", right)
		}
		return cmpOrdered(int64(l), op, int64(r))
	default:
		return false, fmt.Errorf("expr: unsupported comparison type %T", left)
	}
}

func cmpOrdered[T interface{ ~int64 | ~float64 }](l T, op ast.CompareOp, r T) (bool, error) {
	switch op {
	case ast.CmpEq:
		return l == r, nil
	case ast.CmpNotEq:
		return l != r, nil
	case ast.CmpLt:
		return l < r, nil
	case ast.CmpLtEq:
		return l <= r, nil
	case ast.CmpGt:
		return l > r, nil
	case ast.CmpGtEq:
		return l >= r, nil
	default:
		return false, fmt.Errorf("expr: operator %v not applicable to numeric type", op)
	}
}

func cmpString(l string, op ast.CompareOp, r string) (bool, error) {
	switch op {
	case ast.CmpEq:
		return l == r, nil
	case ast.CmpNotEq:
		return l != r, nil
	case ast.CmpLt:
		return l < r, nil
	case ast.CmpLtEq:
		return l <= r, nil
	case ast.CmpGt:
		return l > r, nil
	case ast.CmpGtEq:
		return l >= r, nil
	case ast.CmpTildeEq:
		return strings.EqualFold(l, r), nil
	case ast.CmpContains:
		return strings.Contains(l, r), nil
	case ast.CmpStartswith:
		return strings.HasPrefix(l, r), nil
	case ast.CmpEndswith:
		return strings.HasSuffix(l, r), nil
	default:
		return false, fmt.Errorf("expr: unknown operator %v for string comparison", op)
	}
}

func cmpBool(l bool, op ast.CompareOp, r bool) (bool, error) {
	switch op {
	case ast.CmpEq:
		return l == r, nil
	case ast.CmpNotEq:
		return l != r, nil
	default:
		return false, fmt.Errorf("expr: operator %v not applicable to bool", op)
	}
}
