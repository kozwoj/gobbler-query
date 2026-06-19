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
// Returns a typed Value. Value{Kind: KindNull} represents a null result.
func evalScalar(e ast.ScalarExpr, b *batch.Batch, row int) (Value, error) {
	switch s := e.(type) {
	case *ast.FieldRefExpr:
		idx, err := columnIndex(b.Schema, s.Ref)
		if err != nil {
			return Value{}, err
		}
		col := b.Columns[idx]
		if col.IsNull(row) {
			return Value{Kind: KindNull}, nil
		}
		switch v := col.(type) {
		case *batch.Int32Vector:
			return Value{Kind: KindInt32, I: int64(v.Values[row])}, nil
		case *batch.Int64Vector:
			return Value{Kind: KindInt64, I: v.Values[row]}, nil
		case *batch.Float64Vector:
			return Value{Kind: KindFloat64, F: v.Values[row]}, nil
		case *batch.StringVector:
			return Value{Kind: KindString, S: v.Values[row]}, nil
		case *batch.BoolVector:
			i := int64(0)
			if v.Values[row] {
				i = 1
			}
			return Value{Kind: KindBool, I: i}, nil
		case *batch.DatetimeVector:
			return Value{Kind: KindDatetime, I: v.Values[row].UnixNano()}, nil
		case *batch.TimespanVector:
			return Value{Kind: KindTimespan, I: int64(v.Values[row])}, nil
		case *batch.DynamicVector:
			return Value{Kind: KindDynamic, S: v.Values[row]}, nil
		default:
			return Value{}, fmt.Errorf("expr: unsupported column type %T", col)
		}

	case *ast.IntLit:
		return Value{Kind: KindInt64, I: s.Value}, nil
	case *ast.FloatLit:
		return Value{Kind: KindFloat64, F: s.Value}, nil
	case *ast.StringLit:
		return Value{Kind: KindString, S: s.Value}, nil
	case *ast.BoolLit:
		i := int64(0)
		if s.Value {
			i = 1
		}
		return Value{Kind: KindBool, I: i}, nil
	case *ast.DatetimeLit:
		return Value{Kind: KindDatetime, I: s.Value.UnixNano()}, nil
	case *ast.AgoExpr:
		return Value{Kind: KindDatetime, I: time.Now().Add(-s.Duration.Duration).UnixNano()}, nil

	case *ast.BinaryExpr:
		lv, err := evalScalar(s.Left, b, row)
		if err != nil {
			return Value{}, err
		}
		rv, err := evalScalar(s.Right, b, row)
		if err != nil {
			return Value{}, err
		}
		if lv.Kind == KindNull || rv.Kind == KindNull {
			return Value{Kind: KindNull}, nil
		}
		return applyBinaryOp(lv, s.Op, rv)

	case *ast.UnaryMinusExpr:
		v, err := evalScalar(s.Expr, b, row)
		if err != nil {
			return Value{}, err
		}
		if v.Kind == KindNull {
			return Value{Kind: KindNull}, nil
		}
		return applyUnaryMinus(v)

	default:
		return Value{}, fmt.Errorf("expr: unsupported ScalarExpr type %T", e)
	}
}

// applyBinaryOp evaluates lv op rv. Int32 is normalised to Int64 before dispatch.
func applyBinaryOp(lv Value, op ast.BinaryOp, rv Value) (Value, error) {
	// Normalise Int32 → Int64 for arithmetic
	if lv.Kind == KindInt32 {
		lv.Kind = KindInt64
	}
	if rv.Kind == KindInt32 {
		rv.Kind = KindInt64
	}

	switch lv.Kind {
	case KindInt64:
		switch rv.Kind {
		case KindInt64:
			switch op {
			case ast.BinAdd:
				return Value{Kind: KindInt64, I: lv.I + rv.I}, nil
			case ast.BinSub:
				return Value{Kind: KindInt64, I: lv.I - rv.I}, nil
			case ast.BinMul:
				return Value{Kind: KindInt64, I: lv.I * rv.I}, nil
			case ast.BinDiv:
				if rv.I == 0 {
					return Value{}, fmt.Errorf("expr: integer division by zero")
				}
				return Value{Kind: KindInt64, I: lv.I / rv.I}, nil
			}
		case KindFloat64:
			return applyBinaryOp(Value{Kind: KindFloat64, F: float64(lv.I)}, op, rv)
		}
	case KindFloat64:
		switch rv.Kind {
		case KindFloat64:
			switch op {
			case ast.BinAdd:
				return Value{Kind: KindFloat64, F: lv.F + rv.F}, nil
			case ast.BinSub:
				return Value{Kind: KindFloat64, F: lv.F - rv.F}, nil
			case ast.BinMul:
				return Value{Kind: KindFloat64, F: lv.F * rv.F}, nil
			case ast.BinDiv:
				if rv.F == 0 {
					return Value{}, fmt.Errorf("expr: float division by zero")
				}
				return Value{Kind: KindFloat64, F: lv.F / rv.F}, nil
			}
		case KindInt64:
			return applyBinaryOp(lv, op, Value{Kind: KindFloat64, F: float64(rv.I)})
		}
	case KindDatetime:
		switch rv.Kind {
		case KindDatetime:
			if op == ast.BinSub {
				return Value{Kind: KindTimespan, I: lv.I - rv.I}, nil
			}
		case KindTimespan:
			switch op {
			case ast.BinAdd:
				return Value{Kind: KindDatetime, I: lv.I + rv.I}, nil
			case ast.BinSub:
				return Value{Kind: KindDatetime, I: lv.I - rv.I}, nil
			}
		}
	case KindTimespan:
		switch rv.Kind {
		case KindTimespan:
			switch op {
			case ast.BinAdd:
				return Value{Kind: KindTimespan, I: lv.I + rv.I}, nil
			case ast.BinSub:
				return Value{Kind: KindTimespan, I: lv.I - rv.I}, nil
			}
		case KindInt64:
			switch op {
			case ast.BinMul:
				return Value{Kind: KindTimespan, I: lv.I * rv.I}, nil
			case ast.BinDiv:
				if rv.I == 0 {
					return Value{}, fmt.Errorf("expr: division by zero")
				}
				return Value{Kind: KindTimespan, I: lv.I / rv.I}, nil
			}
		case KindFloat64:
			switch op {
			case ast.BinMul:
				return Value{Kind: KindTimespan, I: int64(float64(lv.I) * rv.F)}, nil
			case ast.BinDiv:
				if rv.F == 0 {
					return Value{}, fmt.Errorf("expr: division by zero")
				}
				return Value{Kind: KindTimespan, I: int64(float64(lv.I) / rv.F)}, nil
			}
		}
	}
	return Value{}, fmt.Errorf("expr: no binary rule for %v %v %v", lv.Kind, op, rv.Kind)
}

func applyUnaryMinus(v Value) (Value, error) {
	switch v.Kind {
	case KindInt32, KindInt64:
		return Value{Kind: KindInt64, I: -v.I}, nil
	case KindFloat64:
		return Value{Kind: KindFloat64, F: -v.F}, nil
	case KindTimespan:
		return Value{Kind: KindTimespan, I: -v.I}, nil
	}
	return Value{}, fmt.Errorf("expr: unary minus not applicable to %v", v.Kind)
}

func compileCompare(e *ast.CompareExpr) RowPredicate {
	return func(b *batch.Batch, row int) (bool, error) {
		lv, err := evalScalar(e.Left, b, row)
		if err != nil {
			return false, err
		}
		rv, err := evalScalar(e.Right, b, row)
		if err != nil {
			return false, err
		}
		if lv.Kind == KindNull || rv.Kind == KindNull {
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

// compareValues compares two Values with numeric type promotion.
// Promotion rules: Int32 → Int64; Int64 → Float64 when the other side is Float64.
func compareValues(left Value, op ast.CompareOp, right Value) (bool, error) {
	// Normalise Int32 → Int64
	if left.Kind == KindInt32 {
		left.Kind = KindInt64
	}
	if right.Kind == KindInt32 {
		right.Kind = KindInt64
	}
	// Cross-type numeric promotion: int64 <-> float64
	if left.Kind == KindInt64 && right.Kind == KindFloat64 {
		left = Value{Kind: KindFloat64, F: float64(left.I)}
	} else if left.Kind == KindFloat64 && right.Kind == KindInt64 {
		right = Value{Kind: KindFloat64, F: float64(right.I)}
	}

	switch left.Kind {
	case KindInt64:
		if right.Kind != KindInt64 {
			return false, fmt.Errorf("expr: type mismatch: int64 vs %v", right.Kind)
		}
		return cmpOrdered(left.I, op, right.I)
	case KindFloat64:
		if right.Kind != KindFloat64 {
			return false, fmt.Errorf("expr: type mismatch: float64 vs %v", right.Kind)
		}
		return cmpOrdered(left.F, op, right.F)
	case KindString, KindDynamic:
		// Dynamic and String both store their value in .S and compare lexicographically.
		// A string literal compared against a dynamic column is a common pattern.
		if right.Kind != KindString && right.Kind != KindDynamic {
			return false, fmt.Errorf("expr: type mismatch: %v vs %v", left.Kind, right.Kind)
		}
		return cmpString(left.S, op, right.S)
	case KindBool:
		if right.Kind != KindBool {
			return false, fmt.Errorf("expr: type mismatch: bool vs %v", right.Kind)
		}
		return cmpBool(left.I != 0, op, right.I != 0)
	case KindDatetime:
		if right.Kind != KindDatetime {
			return false, fmt.Errorf("expr: type mismatch: datetime vs %v", right.Kind)
		}
		return cmpOrdered(left.I, op, right.I)
	case KindTimespan:
		if right.Kind != KindTimespan {
			return false, fmt.Errorf("expr: type mismatch: timespan vs %v", right.Kind)
		}
		return cmpOrdered(left.I, op, right.I)
	default:
		return false, fmt.Errorf("expr: unsupported comparison kind %v", left.Kind)
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
