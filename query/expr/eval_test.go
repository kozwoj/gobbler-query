package expr

import (
	"testing"
	"time"

	"github.com/kozwoj/gobbler-query/query/ast"
	"github.com/kozwoj/gobbler-query/query/batch"
)

// ─── batch helpers ────────────────────────────────────────────────────────────

func intBatch(col string, vals []int32) *batch.Batch {
	return &batch.Batch{
		Length:  len(vals),
		Schema:  []batch.ColumnMeta{{Name: col, Origin: "t"}},
		Columns: []batch.ColumnVector{&batch.Int32Vector{Values: vals}},
	}
}

func strBatch(col string, vals []string) *batch.Batch {
	return &batch.Batch{
		Length:  len(vals),
		Schema:  []batch.ColumnMeta{{Name: col, Origin: "t"}},
		Columns: []batch.ColumnVector{&batch.StringVector{Values: vals}},
	}
}

func nullIntBatch(col string, vals []int32, nullRow int) *batch.Batch {
	nulls := make([]uint64, (len(vals)+63)/64)
	nulls[nullRow/64] |= 1 << (uint(nullRow) % 64)
	return &batch.Batch{
		Length:  len(vals),
		Schema:  []batch.ColumnMeta{{Name: col, Origin: "t"}},
		Columns: []batch.ColumnVector{&batch.Int32Vector{Values: vals, Nulls: nulls}},
	}
}

// checkRows evaluates pred against every row in b and checks the result
// against want. want must have exactly b.Length entries.
func checkRows(t *testing.T, label string, pred batch.RowPredicate, b *batch.Batch, want []bool) {
	t.Helper()
	for row, w := range want {
		got, err := pred(b, row)
		if err != nil {
			t.Errorf("%s row %d: unexpected error: %v", label, row, err)
			continue
		}
		if got != w {
			t.Errorf("%s row %d: got %v, want %v", label, row, got, w)
		}
	}
}

// ─── integer comparisons ──────────────────────────────────────────────────────

func TestCompile_IntCompare(t *testing.T) {
	b := intBatch("code", []int32{200, 400, 500})

	cases := []struct {
		name string
		op   ast.CompareOp
		lit  int64
		want []bool
	}{
		{"gte", ast.CmpGtEq, 400, []bool{false, true, true}},
		{"eq", ast.CmpEq, 200, []bool{true, false, false}},
		{"neq", ast.CmpNotEq, 200, []bool{false, true, true}},
		{"lt", ast.CmpLt, 400, []bool{true, false, false}},
		{"lte", ast.CmpLtEq, 400, []bool{true, true, false}},
		{"gt", ast.CmpGt, 400, []bool{false, false, true}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pred, err := Compile(&ast.CompareExpr{
				Left:  &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "code"}},
				Op:    tc.op,
				Right: &ast.IntLit{Value: tc.lit},
			})
			if err != nil {
				t.Fatal(err)
			}
			checkRows(t, tc.name, pred, b, tc.want)
		})
	}
}

// ─── string comparisons ───────────────────────────────────────────────────────

func TestCompile_StringCompare(t *testing.T) {
	b := strBatch("region", []string{"eastus", "westus", "eastasia"})

	cases := []struct {
		name string
		op   ast.CompareOp
		lit  string
		want []bool
	}{
		{"eq", ast.CmpEq, "eastus", []bool{true, false, false}},
		{"neq", ast.CmpNotEq, "eastus", []bool{false, true, true}},
		{"tildeeq", ast.CmpTildeEq, "EASTUS", []bool{true, false, false}},
		{"contains", ast.CmpContains, "east", []bool{true, false, true}},
		{"startswith", ast.CmpStartswith, "east", []bool{true, false, true}},
		{"endswith", ast.CmpEndswith, "us", []bool{true, true, false}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pred, err := Compile(&ast.CompareExpr{
				Left:  &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "region"}},
				Op:    tc.op,
				Right: &ast.StringLit{Value: tc.lit},
			})
			if err != nil {
				t.Fatal(err)
			}
			checkRows(t, tc.name, pred, b, tc.want)
		})
	}
}

// ─── float comparison with int column (type promotion) ────────────────────────

func TestCompile_IntColumnVsFloatLit(t *testing.T) {
	b := intBatch("score", []int32{1, 2, 3})

	pred, err := Compile(&ast.CompareExpr{
		Left:  &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "score"}},
		Op:    ast.CmpGt,
		Right: &ast.FloatLit{Value: 1.5},
	})
	if err != nil {
		t.Fatal(err)
	}
	checkRows(t, "gt_float", pred, b, []bool{false, true, true})
}

// ─── logical connectives ──────────────────────────────────────────────────────

func TestCompile_And(t *testing.T) {
	b := intBatch("code", []int32{200, 400, 500})

	pred, err := Compile(&ast.AndExpr{
		Left: &ast.CompareExpr{
			Left:  &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "code"}},
			Op:    ast.CmpGtEq,
			Right: &ast.IntLit{Value: 400},
		},
		Right: &ast.CompareExpr{
			Left:  &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "code"}},
			Op:    ast.CmpLt,
			Right: &ast.IntLit{Value: 500},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	checkRows(t, "and", pred, b, []bool{false, true, false})
}

func TestCompile_Or(t *testing.T) {
	b := intBatch("code", []int32{200, 400, 500})

	pred, err := Compile(&ast.OrExpr{
		Left: &ast.CompareExpr{
			Left:  &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "code"}},
			Op:    ast.CmpEq,
			Right: &ast.IntLit{Value: 200},
		},
		Right: &ast.CompareExpr{
			Left:  &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "code"}},
			Op:    ast.CmpEq,
			Right: &ast.IntLit{Value: 500},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	checkRows(t, "or", pred, b, []bool{true, false, true})
}

func TestCompile_Not(t *testing.T) {
	b := intBatch("code", []int32{200, 400, 500})

	pred, err := Compile(&ast.NotExpr{
		Expr: &ast.CompareExpr{
			Left:  &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "code"}},
			Op:    ast.CmpEq,
			Right: &ast.IntLit{Value: 200},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	checkRows(t, "not", pred, b, []bool{false, true, true})
}

// ─── null tests ───────────────────────────────────────────────────────────────

func TestCompile_IsNull(t *testing.T) {
	b := nullIntBatch("code", []int32{200, 0, 500}, 1)

	pred, err := Compile(&ast.IsNullExpr{Kind: ast.KindIsNull, Field: ast.FieldRef{Name: "code"}})
	if err != nil {
		t.Fatal(err)
	}
	checkRows(t, "isnull", pred, b, []bool{false, true, false})
}

func TestCompile_IsNotNull(t *testing.T) {
	b := nullIntBatch("code", []int32{200, 0, 500}, 1)

	pred, err := Compile(&ast.IsNullExpr{Kind: ast.KindIsNotNull, Field: ast.FieldRef{Name: "code"}})
	if err != nil {
		t.Fatal(err)
	}
	checkRows(t, "isnotnull", pred, b, []bool{true, false, true})
}

func TestCompile_IsEmpty(t *testing.T) {
	b := strBatch("msg", []string{"hello", "", "world"})

	pred, err := Compile(&ast.IsNullExpr{Kind: ast.KindIsEmpty, Field: ast.FieldRef{Name: "msg"}})
	if err != nil {
		t.Fatal(err)
	}
	checkRows(t, "isempty", pred, b, []bool{false, true, false})
}

func TestCompile_NullPropagatesInCompare(t *testing.T) {
	// Row 0 is null; comparing null == 0 must return false, not true.
	b := nullIntBatch("code", []int32{0}, 0)

	pred, err := Compile(&ast.CompareExpr{
		Left:  &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "code"}},
		Op:    ast.CmpEq,
		Right: &ast.IntLit{Value: 0},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := pred(b, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Error("null == 0 should be false (null propagation), got true")
	}
}

// ─── datetime comparison ──────────────────────────────────────────────────────

func TestCompile_DatetimeCompare(t *testing.T) {
	t0 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	b := &batch.Batch{
		Length:  3,
		Schema:  []batch.ColumnMeta{{Name: "ts", Origin: "t"}},
		Columns: []batch.ColumnVector{&batch.DatetimeVector{Values: []time.Time{t0, t1, t2}}},
	}

	pred, err := Compile(&ast.CompareExpr{
		Left:  &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "ts"}},
		Op:    ast.CmpGt,
		Right: &ast.DatetimeLit{Value: t1},
	})
	if err != nil {
		t.Fatal(err)
	}
	checkRows(t, "datetime_gt", pred, b, []bool{false, false, true})
}

// ─── qualified column reference ───────────────────────────────────────────────

func TestCompile_QualifiedFieldRef(t *testing.T) {
	b := &batch.Batch{
		Length: 2,
		Schema: []batch.ColumnMeta{
			{Name: "id", Origin: "users"},
			{Name: "id", Origin: "requests"},
		},
		Columns: []batch.ColumnVector{
			&batch.Int32Vector{Values: []int32{1, 2}},
			&batch.Int32Vector{Values: []int32{10, 20}},
		},
	}

	pred, err := Compile(&ast.CompareExpr{
		Left:  &ast.FieldRefExpr{Ref: ast.FieldRef{Table: "requests", Name: "id"}},
		Op:    ast.CmpGt,
		Right: &ast.IntLit{Value: 15},
	})
	if err != nil {
		t.Fatal(err)
	}
	// requests.id values are [10, 20]; only row 1 > 15.
	checkRows(t, "qualified_ref", pred, b, []bool{false, true})
}
