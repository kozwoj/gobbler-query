package expr

import (
	"fmt"

	"github.com/kozwoj/gobbler-query/query/ast"
	"github.com/kozwoj/gobbler-query/query/source"
)

// AggAccumulator accumulates values for one aggregation function over a group.
type AggAccumulator interface {
	// Ingest adds one row's Value. A null input is Value{Kind: KindNull}.
	// For count(), the operator always passes Value{Kind: KindNull} because
	// count() counts rows, not values.
	Ingest(v Value)
	// Result returns the final aggregate value.
	// A null result (avg/min/max over an all-null group) is Value{Kind: KindNull}.
	// count() and dcount() always return a non-null KindInt64 Value.
	Result() Value
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

func (a *countAcc) Ingest(_ Value)  { a.n++ }
func (a *countAcc) Result() Value   { return Value{Kind: KindInt64, I: a.n} }

// ─── sumInt64Acc ─────────────────────────────────────────────────────────────

// sumInt64Acc sums int32 or int64 values; null rows are skipped.
// Used when the output type is TypeInt64 (input was TypeInt32 or TypeInt64).
type sumInt64Acc struct{ sum int64 }

func (a *sumInt64Acc) Ingest(v Value) {
	switch v.Kind {
	case KindInt32, KindInt64:
		a.sum += v.I
	}
}

func (a *sumInt64Acc) Result() Value { return Value{Kind: KindInt64, I: a.sum} }

// ─── sumFloat64Acc ───────────────────────────────────────────────────────────

// sumFloat64Acc sums float64 values; null rows are skipped.
type sumFloat64Acc struct{ sum float64 }

func (a *sumFloat64Acc) Ingest(v Value) {
	if v.Kind == KindFloat64 {
		a.sum += v.F
	}
}

func (a *sumFloat64Acc) Result() Value { return Value{Kind: KindFloat64, F: a.sum} }

// ─── avgAcc ──────────────────────────────────────────────────────────────────

// avgAcc computes the arithmetic mean as float64; null rows are skipped.
// Returns KindNull when no non-null values were ingested.
type avgAcc struct {
	sum float64
	n   int64
}

func (a *avgAcc) Ingest(v Value) {
	switch v.Kind {
	case KindInt32, KindInt64:
		a.sum += float64(v.I)
		a.n++
	case KindFloat64:
		a.sum += v.F
		a.n++
	}
}

func (a *avgAcc) Result() Value {
	if a.n == 0 {
		return Value{Kind: KindNull}
	}
	return Value{Kind: KindFloat64, F: a.sum / float64(a.n)}
}

// ─── minAcc ──────────────────────────────────────────────────────────────────

// minAcc tracks the minimum non-null value seen.
// Returns KindNull when no non-null values were ingested.
type minAcc struct {
	val    Value
	hasVal bool
}

func (a *minAcc) Ingest(v Value) {
	if v.Kind == KindNull {
		return
	}
	if !a.hasVal || CmpValue(v, a.val) < 0 {
		a.val = v
		a.hasVal = true
	}
}

func (a *minAcc) Result() Value {
	if !a.hasVal {
		return Value{Kind: KindNull}
	}
	return a.val
}

// ─── maxAcc ──────────────────────────────────────────────────────────────────

// maxAcc tracks the maximum non-null value seen.
// Returns KindNull when no non-null values were ingested.
type maxAcc struct {
	val    Value
	hasVal bool
}

func (a *maxAcc) Ingest(v Value) {
	if v.Kind == KindNull {
		return
	}
	if !a.hasVal || CmpValue(v, a.val) > 0 {
		a.val = v
		a.hasVal = true
	}
}

func (a *maxAcc) Result() Value {
	if !a.hasVal {
		return Value{Kind: KindNull}
	}
	return a.val
}

// ─── dcountAcc ───────────────────────────────────────────────────────────────

// dcountAcc counts distinct non-null values using an injective binary key
// (via appendValueKey) to avoid type-collision false positives.
type dcountAcc struct {
	seen map[string]struct{}
}

func (a *dcountAcc) Ingest(v Value) {
	if v.Kind == KindNull {
		return
	}
	if a.seen == nil {
		a.seen = make(map[string]struct{})
	}
	a.seen[string(AppendValueKey(nil, v))] = struct{}{}
}

func (a *dcountAcc) Result() Value {
	return Value{Kind: KindInt64, I: int64(len(a.seen))}
}
