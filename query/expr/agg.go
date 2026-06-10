package expr

import (
	"fmt"
	"time"

	"github.com/kozwoj/gobbler-query/query/ast"
	"github.com/kozwoj/gobbler-query/query/source"
)

// AggAccumulator accumulates values for one aggregation function over a group.
type AggAccumulator interface {
	// Ingest adds one row's value. if val is nil and null is true when the source
	// cell is null. For count(), the operator always passes (nil, false) because
	// count() counts rows, not values.
	Ingest(val any, null bool)
	// Result returns the final aggregate value and a null flag.
	// The only null results are avg/min/max over an all-null group.
	// count() and dcount() always return a non-null int64.
	Result() (any, bool)
}

// CompiledAggItem is a ready-to-execute aggregation column.
// The planner constructs one per AggItem after InferAndValidate.
type CompiledAggItem struct {
	Name   string            // output column name
	Type   source.ColumnType // output column type (from InferAndValidate)
	Eval   ScalarEval        // nil for count()
	NewAcc func() AggAccumulator
}

// CompileAggItem builds a CompiledAggItem from an ast.AggItem.
// outputType must be the type returned by InferAndValidate for this item.
// The planner passes the already-resolved output name via item.Alias; the
// default naming here mirrors validateAggItem in logical/infer.go and acts as
// a fallback for tests that construct items without a resolved alias.
func CompileAggItem(item ast.AggItem, outputType source.ColumnType) CompiledAggItem {
	name := item.Alias

	fieldEval := func() ScalarEval {
		return CompileScalar(&ast.FieldRefExpr{Ref: *item.Call.Field})
	}

	switch item.Call.Func {

	case ast.AggCount:
		if name == "" {
			name = "count_"
		}
		return CompiledAggItem{
			Name:   name,
			Type:   source.TypeInt64,
			Eval:   nil,
			NewAcc: func() AggAccumulator { return &countAcc{} },
		}

	case ast.AggSum:
		if name == "" {
			name = "sum_" + item.Call.Field.Name
		}
		if outputType == source.TypeFloat64 {
			return CompiledAggItem{
				Name:   name,
				Type:   source.TypeFloat64,
				Eval:   fieldEval(),
				NewAcc: func() AggAccumulator { return &sumFloat64Acc{} },
			}
		}
		return CompiledAggItem{
			Name:   name,
			Type:   source.TypeInt64,
			Eval:   fieldEval(),
			NewAcc: func() AggAccumulator { return &sumInt64Acc{} },
		}

	case ast.AggAvg:
		if name == "" {
			name = "avg_" + item.Call.Field.Name
		}
		return CompiledAggItem{
			Name:   name,
			Type:   source.TypeFloat64,
			Eval:   fieldEval(),
			NewAcc: func() AggAccumulator { return &avgAcc{} },
		}

	case ast.AggMin:
		if name == "" {
			name = "min_" + item.Call.Field.Name
		}
		return CompiledAggItem{
			Name:   name,
			Type:   outputType,
			Eval:   fieldEval(),
			NewAcc: func() AggAccumulator { return &minAcc{} },
		}

	case ast.AggMax:
		if name == "" {
			name = "max_" + item.Call.Field.Name
		}
		return CompiledAggItem{
			Name:   name,
			Type:   outputType,
			Eval:   fieldEval(),
			NewAcc: func() AggAccumulator { return &maxAcc{} },
		}

	case ast.AggDcount:
		if name == "" {
			name = "dcount_" + item.Call.Field.Name
		}
		return CompiledAggItem{
			Name:   name,
			Type:   source.TypeInt64,
			Eval:   fieldEval(),
			NewAcc: func() AggAccumulator { return &dcountAcc{} },
		}
	}

	panic(fmt.Sprintf("CompileAggItem: unknown AggFunc %v", item.Call.Func))
}

// ─── countAcc ────────────────────────────────────────────────────────────────

// countAcc counts every ingested row regardless of nulls.
// (count() in KQL counts rows, not values.)
type countAcc struct{ n int64 }

func (a *countAcc) Ingest(_ any, _ bool) { a.n++ }
func (a *countAcc) Result() (any, bool)  { return a.n, false }

// ─── sumInt64Acc ─────────────────────────────────────────────────────────────

// sumInt64Acc sums int32 or int64 values; null rows are skipped.
// Used when the output type is TypeInt64 (input was TypeInt32 or TypeInt64).
type sumInt64Acc struct{ sum int64 }

func (a *sumInt64Acc) Ingest(val any, null bool) {
	if null {
		return
	}
	switch v := val.(type) {
	case int32:
		a.sum += int64(v)
	case int64:
		a.sum += v
	}
}

func (a *sumInt64Acc) Result() (any, bool) { return a.sum, false }

// ─── sumFloat64Acc ───────────────────────────────────────────────────────────

// sumFloat64Acc sums float64 values; null rows are skipped.
type sumFloat64Acc struct{ sum float64 }

func (a *sumFloat64Acc) Ingest(val any, null bool) {
	if null {
		return
	}
	if v, ok := val.(float64); ok {
		a.sum += v
	}
}

func (a *sumFloat64Acc) Result() (any, bool) { return a.sum, false }

// ─── avgAcc ──────────────────────────────────────────────────────────────────

// avgAcc computes the arithmetic mean as float64; null rows are skipped.
// Returns null when no non-null values were ingested.
type avgAcc struct {
	sum float64
	n   int64
}

func (a *avgAcc) Ingest(val any, null bool) {
	if null {
		return
	}
	switch v := val.(type) {
	case int32:
		a.sum += float64(v)
	case int64:
		a.sum += float64(v)
	case float64:
		a.sum += v
	}
	a.n++
}

func (a *avgAcc) Result() (any, bool) {
	if a.n == 0 {
		return nil, true
	}
	return a.sum / float64(a.n), false
}

// ─── cmpVal ──────────────────────────────────────────────────────────────────

// cmpVal compares two non-null ordered values of the same concrete type.
// Returns -1, 0, or +1. Used by minAcc and maxAcc.
func cmpVal(a, b any) int {
	switch av := a.(type) {
	case int32:
		bv := b.(int32)
		if av < bv {
			return -1
		} else if av > bv {
			return 1
		}
	case int64:
		bv := b.(int64)
		if av < bv {
			return -1
		} else if av > bv {
			return 1
		}
	case float64:
		bv := b.(float64)
		if av < bv {
			return -1
		} else if av > bv {
			return 1
		}
	case time.Time:
		bv := b.(time.Time)
		if av.Before(bv) {
			return -1
		} else if av.After(bv) {
			return 1
		}
	case time.Duration:
		bv := b.(time.Duration)
		if av < bv {
			return -1
		} else if av > bv {
			return 1
		}
	}
	return 0
}

// ─── minAcc ──────────────────────────────────────────────────────────────────

// minAcc tracks the minimum non-null value seen.
// Returns null when no non-null values were ingested.
type minAcc struct {
	val    any
	hasVal bool
}

func (a *minAcc) Ingest(val any, null bool) {
	if null {
		return
	}
	if !a.hasVal || cmpVal(val, a.val) < 0 {
		a.val = val
		a.hasVal = true
	}
}

func (a *minAcc) Result() (any, bool) {
	if !a.hasVal {
		return nil, true
	}
	return a.val, false
}

// ─── maxAcc ──────────────────────────────────────────────────────────────────

// maxAcc tracks the maximum non-null value seen.
// Returns null when no non-null values were ingested.
type maxAcc struct {
	val    any
	hasVal bool
}

func (a *maxAcc) Ingest(val any, null bool) {
	if null {
		return
	}
	if !a.hasVal || cmpVal(val, a.val) > 0 {
		a.val = val
		a.hasVal = true
	}
}

func (a *maxAcc) Result() (any, bool) {
	if !a.hasVal {
		return nil, true
	}
	return a.val, false
}

// ─── dcountAcc ───────────────────────────────────────────────────────────────

// dcountAcc counts distinct non-null values using their fmt.Sprintf("%v") key.
// A single dcount field always has one concrete type so there is no collision risk.
type dcountAcc struct {
	seen map[string]struct{}
}

func (a *dcountAcc) Ingest(val any, null bool) {
	if null {
		return
	}
	if a.seen == nil {
		a.seen = make(map[string]struct{})
	}
	a.seen[fmt.Sprintf("%v", val)] = struct{}{}
}

func (a *dcountAcc) Result() (any, bool) {
	return int64(len(a.seen)), false
}
