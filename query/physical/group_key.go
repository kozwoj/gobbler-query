package physical

import (
	"time"

	"github.com/kozwoj/gobbler-query/query/expr"
)

// groupKeyFromValues encodes a slice of Values into an injective binary string
// suitable for use as a map key. Uses expr.AppendValueKey for each element —
// no fmt.Sprintf, no per-field allocations.
func groupKeyFromValues(vals []expr.Value) string {
	var buf []byte
	for _, v := range vals {
		buf = expr.AppendValueKey(buf, v)
	}
	return string(buf)
}

// groupKey encodes the values of the specified columns from a []any row into
// a binary map key by first converting each cell to an expr.Value.
// Still used by hash_join_op.go which has [][]any row storage until Step 6.
func groupKey(row []any, nulls []bool, colIndices []int) string {
	vals := make([]expr.Value, len(colIndices))
	for i, idx := range colIndices {
		vals[i] = anyToExprValue(row[idx], nulls[idx])
	}
	return groupKeyFromValues(vals)
}

// anyToExprValue bridges a legacy (any, bool) cell to a typed Value.
// Removed in Step 6 once [][]any row storage is gone.
func anyToExprValue(v any, null bool) expr.Value {
	if null {
		return expr.Value{Kind: expr.KindNull}
	}
	switch x := v.(type) {
	case int32:
		return expr.Value{Kind: expr.KindInt32, I: int64(x)}
	case int64:
		return expr.Value{Kind: expr.KindInt64, I: x}
	case float64:
		return expr.Value{Kind: expr.KindFloat64, F: x}
	case string:
		return expr.Value{Kind: expr.KindString, S: x}
	case bool:
		i := int64(0)
		if x {
			i = 1
		}
		return expr.Value{Kind: expr.KindBool, I: i}
	case time.Time:
		return expr.Value{Kind: expr.KindDatetime, I: x.UnixNano()}
	case time.Duration:
		return expr.Value{Kind: expr.KindTimespan, I: int64(x)}
	default:
		return expr.Value{Kind: expr.KindNull}
	}
}
