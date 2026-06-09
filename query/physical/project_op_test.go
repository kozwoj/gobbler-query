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

// ─── helpers ──────────────────────────────────────────────────────────────────

// twoColBatch builds a batch with an int32 and a string column.
func twoColBatch() *batch.Batch {
	return &batch.Batch{
		Length: 3,
		Schema: []batch.ColumnMeta{
			{Name: "code", Origin: "req"},
			{Name: "region", Origin: "req"},
		},
		Columns: []batch.ColumnVector{
			&batch.Int32Vector{Values: []int32{200, 400, 500}},
			&batch.StringVector{Values: []string{"east", "west", "east"}},
		},
	}
}

func projectOp(input Operator, items ...expr.CompiledProjectItem) *ProjectOp {
	return &ProjectOp{Input: input, Items: items}
}

func fieldItem(alias, col, origin string, ct source.ColumnType) expr.CompiledProjectItem {
	ref := ast.FieldRef{Name: col}
	name := col
	if alias != "" {
		name = alias
	}
	return expr.CompiledProjectItem{
		Name:   name,
		Origin: origin,
		Type:   ct,
		Eval:   expr.CompileScalar(&ast.FieldRefExpr{Ref: ref}),
	}
}

// ─── bare FieldRef keeps column ───────────────────────────────────────────────

func TestProjectOp_BareRef_KeepsColumn(t *testing.T) {
	b := twoColBatch()
	op := projectOp(&fakeOperator{batches: []*batch.Batch{b}},
		fieldItem("", "code", "req", source.TypeInt32),
	)

	got, err := op.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if got.Length != 3 {
		t.Fatalf("Length = %d, want 3", got.Length)
	}
	if len(got.Columns) != 1 {
		t.Fatalf("got %d columns, want 1", len(got.Columns))
	}
	if got.Schema[0].Name != "code" || got.Schema[0].Origin != "req" {
		t.Errorf("schema[0] = %+v, want {code req}", got.Schema[0])
	}
	vec := got.Columns[0].(*batch.Int32Vector)
	if len(vec.Values) != 3 || vec.Values[1] != 400 {
		t.Errorf("values = %v, want [200 400 500]", vec.Values)
	}
}

// ─── rename (alias = FieldRef) ────────────────────────────────────────────────

func TestProjectOp_Rename_ChangesNamePreservesOrigin(t *testing.T) {
	b := twoColBatch()
	op := projectOp(&fakeOperator{batches: []*batch.Batch{b}},
		fieldItem("statusCode", "code", "req", source.TypeInt32),
	)

	got, err := op.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if got.Schema[0].Name != "statusCode" {
		t.Errorf("schema name = %q, want statusCode", got.Schema[0].Name)
	}
	if got.Schema[0].Origin != "req" {
		t.Errorf("schema origin = %q, want req", got.Schema[0].Origin)
	}
}

// ─── computed column (alias = BinaryExpr) ─────────────────────────────────────

func TestProjectOp_Compute_IntAddition(t *testing.T) {
	b := &batch.Batch{
		Length: 2,
		Schema: []batch.ColumnMeta{{Name: "a", Origin: "t"}, {Name: "b", Origin: "t"}},
		Columns: []batch.ColumnVector{
			&batch.Int64Vector{Values: []int64{10, 20}},
			&batch.Int64Vector{Values: []int64{3, 7}},
		},
	}
	sumExpr := &ast.BinaryExpr{
		Left:  &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "a"}},
		Op:    ast.BinAdd,
		Right: &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "b"}},
	}
	op := projectOp(&fakeOperator{batches: []*batch.Batch{b}},
		expr.CompiledProjectItem{Name: "total", Origin: "", Type: source.TypeInt64, Eval: expr.CompileScalar(sumExpr)},
	)

	got, err := op.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if got.Schema[0].Name != "total" || got.Schema[0].Origin != "" {
		t.Errorf("schema = %+v, want {total }", got.Schema[0])
	}
	vec := got.Columns[0].(*batch.Int64Vector)
	if vec.Values[0] != 13 || vec.Values[1] != 27 {
		t.Errorf("values = %v, want [13 27]", vec.Values)
	}
}

func TestProjectOp_Compute_DatetimeSubtract_YieldsTimespan(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 1, 1, 13, 30, 0, 0, time.UTC) // 90 minutes later
	b := &batch.Batch{
		Length: 1,
		Schema: []batch.ColumnMeta{{Name: "start", Origin: "t"}, {Name: "end", Origin: "t"}},
		Columns: []batch.ColumnVector{
			&batch.DatetimeVector{Values: []time.Time{t0}},
			&batch.DatetimeVector{Values: []time.Time{t1}},
		},
	}
	durExpr := &ast.BinaryExpr{
		Left:  &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "end"}},
		Op:    ast.BinSub,
		Right: &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "start"}},
	}
	op := projectOp(&fakeOperator{batches: []*batch.Batch{b}},
		expr.CompiledProjectItem{Name: "dur", Origin: "", Type: source.TypeTimespan, Eval: expr.CompileScalar(durExpr)},
	)

	got, err := op.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	vec := got.Columns[0].(*batch.TimespanVector)
	if vec.Values[0] != 90*time.Minute {
		t.Errorf("dur = %v, want 90m", vec.Values[0])
	}
}

// ─── multiple output columns ──────────────────────────────────────────────────

func TestProjectOp_MultipleItems_ReorderColumns(t *testing.T) {
	b := twoColBatch()
	op := projectOp(&fakeOperator{batches: []*batch.Batch{b}},
		fieldItem("", "region", "req", source.TypeString),
		fieldItem("", "code", "req", source.TypeInt32),
	)

	got, err := op.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if len(got.Columns) != 2 {
		t.Fatalf("got %d columns, want 2", len(got.Columns))
	}
	if got.Schema[0].Name != "region" || got.Schema[1].Name != "code" {
		t.Errorf("schema = %v %v, want region code", got.Schema[0].Name, got.Schema[1].Name)
	}
	// first col is strings
	if _, ok := got.Columns[0].(*batch.StringVector); !ok {
		t.Errorf("col[0] type = %T, want *batch.StringVector", got.Columns[0])
	}
	// second col is int32
	if _, ok := got.Columns[1].(*batch.Int32Vector); !ok {
		t.Errorf("col[1] type = %T, want *batch.Int32Vector", got.Columns[1])
	}
}

// ─── null propagation ─────────────────────────────────────────────────────────

func TestProjectOp_NullRow_PropagatedFromFieldRef(t *testing.T) {
	// row 1 is null
	nulls := []uint64{0b010} // bit 1 set
	b := &batch.Batch{
		Length: 3,
		Schema: []batch.ColumnMeta{{Name: "code", Origin: "t"}},
		Columns: []batch.ColumnVector{
			&batch.Int32Vector{Values: []int32{200, 0, 500}, Nulls: nulls},
		},
	}
	op := projectOp(&fakeOperator{batches: []*batch.Batch{b}},
		fieldItem("", "code", "t", source.TypeInt32),
	)

	got, err := op.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	vec := got.Columns[0].(*batch.Int32Vector)
	if got.Length != 3 {
		t.Fatalf("Length = %d, want 3", got.Length)
	}
	if vec.IsNull(1) != true {
		t.Errorf("row 1 IsNull = false, want true")
	}
	if vec.IsNull(0) || vec.IsNull(2) {
		t.Errorf("rows 0 and 2 should not be null")
	}
}

func TestProjectOp_NullRow_PropagatedInBinaryExpr(t *testing.T) {
	// a[1] is null → a + b at row 1 should also be null
	nulls := []uint64{0b010}
	b := &batch.Batch{
		Length: 3,
		Schema: []batch.ColumnMeta{{Name: "a", Origin: "t"}, {Name: "b", Origin: "t"}},
		Columns: []batch.ColumnVector{
			&batch.Int64Vector{Values: []int64{1, 0, 3}, Nulls: nulls},
			&batch.Int64Vector{Values: []int64{10, 20, 30}},
		},
	}
	sumExpr := &ast.BinaryExpr{
		Left:  &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "a"}},
		Op:    ast.BinAdd,
		Right: &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "b"}},
	}
	op := projectOp(&fakeOperator{batches: []*batch.Batch{b}},
		expr.CompiledProjectItem{Name: "sum", Origin: "", Type: source.TypeInt64, Eval: expr.CompileScalar(sumExpr)},
	)

	got, err := op.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	vec := got.Columns[0].(*batch.Int64Vector)
	if vec.Values[0] != 11 {
		t.Errorf("row 0 = %d, want 11", vec.Values[0])
	}
	if !vec.IsNull(1) {
		t.Errorf("row 1 should be null (a is null)")
	}
	if vec.Values[2] != 33 {
		t.Errorf("row 2 = %d, want 33", vec.Values[2])
	}
}

// ─── EOF and Close ────────────────────────────────────────────────────────────

func TestProjectOp_EOF_PropagatesFromInput(t *testing.T) {
	op := projectOp(&fakeOperator{batches: nil},
		fieldItem("", "code", "t", source.TypeInt32),
	)
	_, err := op.Next()
	if err != io.EOF {
		t.Fatalf("Next() error = %v, want io.EOF", err)
	}
}

func TestProjectOp_Close_Delegates(t *testing.T) {
	input := &fakeOperator{closeErr: io.ErrUnexpectedEOF}
	op := &ProjectOp{Input: input}
	if err := op.Close(); err != io.ErrUnexpectedEOF {
		t.Errorf("Close() = %v, want ErrUnexpectedEOF", err)
	}
}

func TestProjectOp_AllNull_ProducesCorrectVectorType(t *testing.T) {
	// All 3 rows are null in an int32 column.
	nulls := []uint64{0b0111} // bits 0,1,2 set
	b := &batch.Batch{
		Length: 3,
		Schema: []batch.ColumnMeta{{Name: "code", Origin: "t"}},
		Columns: []batch.ColumnVector{
			&batch.Int32Vector{Values: []int32{0, 0, 0}, Nulls: nulls},
		},
	}
	op := projectOp(&fakeOperator{batches: []*batch.Batch{b}},
		fieldItem("", "code", "t", source.TypeInt32),
	)
	got, err := op.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if _, ok := got.Columns[0].(*batch.Int32Vector); !ok {
		t.Errorf("all-null column type = %T, want *batch.Int32Vector", got.Columns[0])
	}
	for i := 0; i < 3; i++ {
		if !got.Columns[0].IsNull(i) {
			t.Errorf("row %d IsNull = false, want true", i)
		}
	}
}
