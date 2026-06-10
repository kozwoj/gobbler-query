package physical

import (
	"io"
	"testing"
	"time"

	"github.com/kozwoj/gobbler-query/query/ast"
	"github.com/kozwoj/gobbler-query/query/batch"
	"github.com/kozwoj/gobbler-query/query/expr"
	"github.com/kozwoj/gobbler-query/query/source"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// aggOp builds a HashAggregateOp with one group-by column ("region", string)
// and the supplied agg items.
func aggOp(input Operator, byColName string, byColType source.ColumnType, aggs ...expr.CompiledAggItem) *HashAggregateOp {
	return &HashAggregateOp{
		Input: input,
		Aggs:  aggs,
		GroupBy: []GroupByCol{{
			Name: byColName,
			Type: byColType,
			Eval: expr.CompileScalar(&ast.FieldRefExpr{Ref: ast.FieldRef{Name: byColName}}),
		}},
	}
}

// countItem returns a CompiledAggItem for count() with the given output name.
func countItem(name string) expr.CompiledAggItem {
	item := ast.AggItem{Alias: name, Call: ast.AggCall{Func: ast.AggCount}}
	return expr.CompileAggItem(item, source.TypeInt64)
}

// sumInt64Item returns a CompiledAggItem for sum(<field>) → int64.
func sumInt64Item(alias, field string) expr.CompiledAggItem {
	f := ast.FieldRef{Name: field}
	item := ast.AggItem{Alias: alias, Call: ast.AggCall{Func: ast.AggSum, Field: &f}}
	return expr.CompileAggItem(item, source.TypeInt64)
}

// avgItem returns a CompiledAggItem for avg(<field>) → float64.
func avgItem(alias, field string) expr.CompiledAggItem {
	f := ast.FieldRef{Name: field}
	item := ast.AggItem{Alias: alias, Call: ast.AggCall{Func: ast.AggAvg, Field: &f}}
	return expr.CompileAggItem(item, source.TypeFloat64)
}

// minInt32Item returns a CompiledAggItem for min(<field>) → int32.
func minInt32Item(alias, field string) expr.CompiledAggItem {
	f := ast.FieldRef{Name: field}
	item := ast.AggItem{Alias: alias, Call: ast.AggCall{Func: ast.AggMin, Field: &f}}
	return expr.CompileAggItem(item, source.TypeInt32)
}

// maxInt32Item returns a CompiledAggItem for max(<field>) → int32.
func maxInt32Item(alias, field string) expr.CompiledAggItem {
	f := ast.FieldRef{Name: field}
	item := ast.AggItem{Alias: alias, Call: ast.AggCall{Func: ast.AggMax, Field: &f}}
	return expr.CompileAggItem(item, source.TypeInt32)
}

// dcountItem returns a CompiledAggItem for dcount(<field>) → int64.
func dcountItem(alias, field string) expr.CompiledAggItem {
	f := ast.FieldRef{Name: field}
	item := ast.AggItem{Alias: alias, Call: ast.AggCall{Func: ast.AggDcount, Field: &f}}
	return expr.CompileAggItem(item, source.TypeInt64)
}

// twoColAggBatch builds a batch with a string "region" column and an int32
// "code" column.
func twoColAggBatch(regions []string, codes []int32) *batch.Batch {
	return &batch.Batch{
		Length: len(regions),
		Schema: []batch.ColumnMeta{
			{Name: "region"},
			{Name: "code"},
		},
		Columns: []batch.ColumnVector{
			&batch.StringVector{Values: append([]string{}, regions...)},
			&batch.Int32Vector{Values: append([]int32{}, codes...)},
		},
	}
}

// collectAll drains op into a single flat row list for easy assertions.
func collectAll(t *testing.T, op *HashAggregateOp) []*batch.Batch {
	t.Helper()
	var out []*batch.Batch
	for {
		b, err := op.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next() error = %v", err)
		}
		out = append(out, b)
	}
	return out
}

// findRow scans all batches for a row where column colIdx equals wantVal
// and returns the batch and local row index, or (-1,-1) if not found.
func findRow(batches []*batch.Batch, colIdx int, wantVal any) (batchIdx, rowIdx int) {
	for bi, b := range batches {
		for ri := 0; ri < b.Length; ri++ {
			switch col := b.Columns[colIdx].(type) {
			case *batch.StringVector:
				if s, ok := wantVal.(string); ok && col.Values[ri] == s {
					return bi, ri
				}
			case *batch.Int32Vector:
				if v, ok := wantVal.(int32); ok && col.Values[ri] == v {
					return bi, ri
				}
			case *batch.Int64Vector:
				if v, ok := wantVal.(int64); ok && col.Values[ri] == v {
					return bi, ri
				}
			case *batch.Float64Vector:
				if v, ok := wantVal.(float64); ok && col.Values[ri] == v {
					return bi, ri
				}
			}
		}
	}
	return -1, -1
}

// ─── count ────────────────────────────────────────────────────────────────────

func TestHashAggOp_Count_SingleGroup(t *testing.T) {
	b := twoColAggBatch([]string{"east", "east", "east"}, []int32{1, 2, 3})
	op := aggOp(&fakeOperator{batches: []*batch.Batch{b}}, "region", source.TypeString,
		countItem("n"))
	batches := collectAll(t, op)
	if len(batches) != 1 || batches[0].Length != 1 {
		t.Fatalf("expected 1 output row, got batches=%d", len(batches))
	}
	n := batches[0].Columns[0].(*batch.Int64Vector).Values[0]
	if n != 3 {
		t.Errorf("count = %d, want 3", n)
	}
}

func TestHashAggOp_Count_TwoGroups(t *testing.T) {
	b := twoColAggBatch(
		[]string{"east", "west", "east", "west"},
		[]int32{1, 2, 3, 4},
	)
	op := aggOp(&fakeOperator{batches: []*batch.Batch{b}}, "region", source.TypeString,
		countItem("n"))
	batches := collectAll(t, op)
	total := 0
	for _, b := range batches {
		total += b.Length
	}
	if total != 2 {
		t.Fatalf("expected 2 output rows (one per group), got %d", total)
	}
	// Each group must have count = 2.
	for _, b := range batches {
		for ri := 0; ri < b.Length; ri++ {
			n := b.Columns[0].(*batch.Int64Vector).Values[ri]
			if n != 2 {
				t.Errorf("count for a group = %d, want 2", n)
			}
		}
	}
}

// ─── sum ─────────────────────────────────────────────────────────────────────

func TestHashAggOp_Sum_TwoGroups(t *testing.T) {
	// east: 1+3=4, west: 2+4=6
	b := twoColAggBatch(
		[]string{"east", "west", "east", "west"},
		[]int32{1, 2, 3, 4},
	)
	op := aggOp(&fakeOperator{batches: []*batch.Batch{b}}, "region", source.TypeString,
		sumInt64Item("total", "code"))
	batches := collectAll(t, op)

	// Find east and west rows by the group-by column (col index 1 = "region").
	biE, riE := findRow(batches, 1, "east")
	biW, riW := findRow(batches, 1, "west")
	if biE < 0 || biW < 0 {
		t.Fatal("could not find east or west group")
	}
	eastSum := batches[biE].Columns[0].(*batch.Int64Vector).Values[riE]
	westSum := batches[biW].Columns[0].(*batch.Int64Vector).Values[riW]
	if eastSum != 4 {
		t.Errorf("east sum = %d, want 4", eastSum)
	}
	if westSum != 6 {
		t.Errorf("west sum = %d, want 6", westSum)
	}
}

// ─── avg ─────────────────────────────────────────────────────────────────────

func TestHashAggOp_Avg_SingleGroup(t *testing.T) {
	b := twoColAggBatch([]string{"east", "east"}, []int32{10, 20})
	op := aggOp(&fakeOperator{batches: []*batch.Batch{b}}, "region", source.TypeString,
		avgItem("mean", "code"))
	batches := collectAll(t, op)
	avg := batches[0].Columns[0].(*batch.Float64Vector).Values[0]
	if avg != 15.0 {
		t.Errorf("avg = %v, want 15.0", avg)
	}
}

// ─── min / max ────────────────────────────────────────────────────────────────

func TestHashAggOp_MinMax_SingleGroup(t *testing.T) {
	b := twoColAggBatch([]string{"east", "east", "east"}, []int32{5, 1, 9})
	op := aggOp(&fakeOperator{batches: []*batch.Batch{b}}, "region", source.TypeString,
		minInt32Item("lo", "code"),
		maxInt32Item("hi", "code"),
	)
	batches := collectAll(t, op)
	lo := batches[0].Columns[0].(*batch.Int32Vector).Values[0]
	hi := batches[0].Columns[1].(*batch.Int32Vector).Values[0]
	if lo != 1 {
		t.Errorf("min = %d, want 1", lo)
	}
	if hi != 9 {
		t.Errorf("max = %d, want 9", hi)
	}
}

// ─── dcount ───────────────────────────────────────────────────────────────────

func TestHashAggOp_Dcount_SingleGroup(t *testing.T) {
	b := twoColAggBatch(
		[]string{"east", "east", "east", "east"},
		[]int32{200, 400, 200, 500},
	)
	op := aggOp(&fakeOperator{batches: []*batch.Batch{b}}, "region", source.TypeString,
		dcountItem("distinct", "code"))
	batches := collectAll(t, op)
	n := batches[0].Columns[0].(*batch.Int64Vector).Values[0]
	if n != 3 {
		t.Errorf("dcount = %d, want 3", n)
	}
}

// ─── multi-batch input ────────────────────────────────────────────────────────

func TestHashAggOp_MultiBatch_AccumulatesAcrossBatches(t *testing.T) {
	// east appears in both batches; its count should be 3.
	b1 := twoColAggBatch([]string{"east", "west"}, []int32{1, 2})
	b2 := twoColAggBatch([]string{"east", "east"}, []int32{3, 4})
	op := aggOp(&fakeOperator{batches: []*batch.Batch{b1, b2}}, "region", source.TypeString,
		countItem("n"))
	batches := collectAll(t, op)

	bi, ri := findRow(batches, 1, "east")
	if bi < 0 {
		t.Fatal("east group not found")
	}
	n := batches[bi].Columns[0].(*batch.Int64Vector).Values[ri]
	if n != 3 {
		t.Errorf("east count across batches = %d, want 3", n)
	}
}

// ─── empty input ──────────────────────────────────────────────────────────────

func TestHashAggOp_EmptyInput_ReturnsEOF(t *testing.T) {
	op := aggOp(&fakeOperator{batches: nil}, "region", source.TypeString, countItem("n"))
	_, err := op.Next()
	if err != io.EOF {
		t.Errorf("empty input: err = %v, want io.EOF", err)
	}
}

// ─── null in group-by ─────────────────────────────────────────────────────────

func TestHashAggOp_NullGroupByKey_FormsSeparateGroup(t *testing.T) {
	// One non-null "east" row and one null row — should produce two groups.
	nullBits := []uint64{0b0010} // row 1 is null
	b := &batch.Batch{
		Length: 2,
		Schema: []batch.ColumnMeta{{Name: "region"}, {Name: "code"}},
		Columns: []batch.ColumnVector{
			&batch.StringVector{Values: []string{"east", ""}, Nulls: nullBits},
			&batch.Int32Vector{Values: []int32{1, 2}},
		},
	}
	op := aggOp(&fakeOperator{batches: []*batch.Batch{b}}, "region", source.TypeString,
		countItem("n"))
	batches := collectAll(t, op)
	total := 0
	for _, b := range batches {
		total += b.Length
	}
	if total != 2 {
		t.Errorf("expected 2 groups (non-null + null), got %d", total)
	}
}

// ─── output schema ────────────────────────────────────────────────────────────

func TestHashAggOp_OutputSchema_AggsThenGroupBy(t *testing.T) {
	b := twoColAggBatch([]string{"east"}, []int32{1})
	op := aggOp(&fakeOperator{batches: []*batch.Batch{b}}, "region", source.TypeString,
		countItem("n"))
	batches := collectAll(t, op)
	if len(batches) == 0 {
		t.Fatal("no output")
	}
	schema := batches[0].Schema
	// Expect: "n" (agg), "region" (group-by)
	if len(schema) != 2 {
		t.Fatalf("schema len = %d, want 2", len(schema))
	}
	if schema[0].Name != "n" {
		t.Errorf("col 0 name = %q, want n", schema[0].Name)
	}
	if schema[1].Name != "region" {
		t.Errorf("col 1 name = %q, want region", schema[1].Name)
	}
}

// ─── batch size ───────────────────────────────────────────────────────────────

func TestHashAggOp_BatchSize_Respected(t *testing.T) {
	// 4 groups, BatchSize=2 → 2 output batches.
	b := twoColAggBatch(
		[]string{"a", "b", "c", "d"},
		[]int32{1, 2, 3, 4},
	)
	op := &HashAggregateOp{
		Input:     &fakeOperator{batches: []*batch.Batch{b}},
		Aggs:      []expr.CompiledAggItem{countItem("n")},
		GroupBy:   []GroupByCol{{Name: "region", Type: source.TypeString, Eval: expr.CompileScalar(&ast.FieldRefExpr{Ref: ast.FieldRef{Name: "region"}})}},
		BatchSize: 2,
	}
	batches := collectAll(t, op)
	if len(batches) != 2 {
		t.Errorf("expected 2 output batches, got %d", len(batches))
	}
}

// ─── close ────────────────────────────────────────────────────────────────────

func TestHashAggOp_Close_Delegates(t *testing.T) {
	input := &fakeOperator{closeErr: io.ErrUnexpectedEOF}
	op := &HashAggregateOp{Input: input}
	if err := op.Close(); err != io.ErrUnexpectedEOF {
		t.Errorf("Close() = %v, want ErrUnexpectedEOF", err)
	}
}

// ─── no group-by (global aggregate) ─────────────────────────────────────────

func TestHashAggOp_NoGroupBy_GlobalCount(t *testing.T) {
	b := int32SortBatch([]int32{1, 2, 3, 4, 5})
	op := &HashAggregateOp{
		Input: &fakeOperator{batches: []*batch.Batch{b}},
		Aggs:  []expr.CompiledAggItem{countItem("n")},
		// GroupBy is empty — all rows in one group.
	}
	batches := collectAll(t, op)
	if len(batches) != 1 || batches[0].Length != 1 {
		t.Fatalf("expected 1 output row, got batches %d", len(batches))
	}
	n := batches[0].Columns[0].(*batch.Int64Vector).Values[0]
	if n != 5 {
		t.Errorf("global count = %d, want 5", n)
	}
}

// ─── null agg result (all-null group) ────────────────────────────────────────

func TestHashAggOp_AllNullAggColumn_ProducesNullResult(t *testing.T) {
	// All code values for "east" are null → avg should be null.
	nullBits := []uint64{0b0011} // rows 0 and 1 are null
	b := &batch.Batch{
		Length: 2,
		Schema: []batch.ColumnMeta{{Name: "region"}, {Name: "code"}},
		Columns: []batch.ColumnVector{
			&batch.StringVector{Values: []string{"east", "east"}},
			&batch.Int32Vector{Values: []int32{0, 0}, Nulls: nullBits},
		},
	}
	op := aggOp(&fakeOperator{batches: []*batch.Batch{b}}, "region", source.TypeString,
		avgItem("mean", "code"))
	batches := collectAll(t, op)
	if !batches[0].Columns[0].IsNull(0) {
		t.Error("avg over all-null group should be null")
	}
}

// ─── datetime group-by ────────────────────────────────────────────────────────

func TestHashAggOp_DatetimeGroupBy(t *testing.T) {
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	b := &batch.Batch{
		Length: 3,
		Schema: []batch.ColumnMeta{{Name: "ts"}, {Name: "code"}},
		Columns: []batch.ColumnVector{
			&batch.DatetimeVector{Values: []time.Time{t1, t2, t1}},
			&batch.Int32Vector{Values: []int32{1, 2, 3}},
		},
	}
	op := &HashAggregateOp{
		Input: &fakeOperator{batches: []*batch.Batch{b}},
		Aggs:  []expr.CompiledAggItem{countItem("n")},
		GroupBy: []GroupByCol{{
			Name: "ts",
			Type: source.TypeDatetime,
			Eval: expr.CompileScalar(&ast.FieldRefExpr{Ref: ast.FieldRef{Name: "ts"}}),
		}},
	}
	batches := collectAll(t, op)
	total := 0
	for _, b := range batches {
		total += b.Length
	}
	if total != 2 {
		t.Errorf("expected 2 groups (one per distinct datetime), got %d", total)
	}
}
