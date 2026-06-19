package physical

import (
	"testing"
	"time"

	"github.com/kozwoj/gobbler-query/query/batch"
	"github.com/kozwoj/gobbler-query/query/expr"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

func intColMeta(name string) batch.ColumnMeta { return batch.ColumnMeta{Name: name, Origin: "t"} }

func int32Batch(vals []int32) *batch.Batch {
	return &batch.Batch{
		Length:  len(vals),
		Schema:  []batch.ColumnMeta{intColMeta("v")},
		Columns: []batch.ColumnVector{&batch.Int32Vector{Values: vals}},
	}
}

// ─── appendBatch ─────────────────────────────────────────────────────────────

func TestRowStore_AppendBatch_Empty_IsNoop(t *testing.T) {
	var m rowStore
	b := &batch.Batch{Length: 0, Schema: []batch.ColumnMeta{intColMeta("v")}}
	if err := m.appendBatch(b); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.rowCount() != 0 {
		t.Errorf("rowCount = %d, want 0", m.rowCount())
	}
	if m.schema != nil {
		t.Errorf("schema should remain nil after empty batch")
	}
}

func TestRowStore_AppendBatch_SingleBatch_RowCount(t *testing.T) {
	var m rowStore
	if err := m.appendBatch(int32Batch([]int32{10, 20, 30})); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.rowCount() != 3 {
		t.Fatalf("rowCount = %d, want 3", m.rowCount())
	}
}

func TestRowStore_AppendBatch_SchemaSetFromFirstBatch(t *testing.T) {
	var m rowStore
	if err := m.appendBatch(int32Batch([]int32{1})); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.schema) != 1 || m.schema[0].Name != "v" {
		t.Errorf("schema = %+v, want [{v t}]", m.schema)
	}
}

func TestRowStore_AppendBatch_ValuesExtracted(t *testing.T) {
	var m rowStore
	if err := m.appendBatch(int32Batch([]int32{100, 200})); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	v0 := m.decodeCell(0, 0)
	v1 := m.decodeCell(1, 0)
	if v0.Kind != expr.KindInt32 || int32(v0.I) != 100 {
		t.Errorf("row 0 = %+v, want KindInt32 I=100", v0)
	}
	if v1.Kind != expr.KindInt32 || int32(v1.I) != 200 {
		t.Errorf("row 1 = %+v, want KindInt32 I=200", v1)
	}
}

func TestRowStore_AppendBatch_MultipleBatches_Accumulate(t *testing.T) {
	var m rowStore
	if err := m.appendBatch(int32Batch([]int32{1, 2})); err != nil {
		t.Fatalf("batch 1 error: %v", err)
	}
	if err := m.appendBatch(int32Batch([]int32{3, 4, 5})); err != nil {
		t.Fatalf("batch 2 error: %v", err)
	}
	if m.rowCount() != 5 {
		t.Fatalf("rowCount = %d, want 5", m.rowCount())
	}
	v4 := m.decodeCell(4, 0)
	if v4.Kind != expr.KindInt32 || int32(v4.I) != 5 {
		t.Errorf("row 4 = %+v, want KindInt32 I=5", v4)
	}
}

func TestRowStore_AppendBatch_NullCell_IsDecoded(t *testing.T) {
	nulls := []uint64{0b0010} // row 1 is null
	b := &batch.Batch{
		Length:  3,
		Schema:  []batch.ColumnMeta{intColMeta("v")},
		Columns: []batch.ColumnVector{&batch.Int32Vector{Values: []int32{10, 0, 30}, Nulls: nulls}},
	}
	var m rowStore
	if err := m.appendBatch(b); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.decodeCell(0, 0).Kind == expr.KindNull {
		t.Error("row 0 should not be null")
	}
	if m.decodeCell(1, 0).Kind != expr.KindNull {
		t.Error("row 1 should be null")
	}
	if m.decodeCell(2, 0).Kind == expr.KindNull {
		t.Error("row 2 should not be null")
	}
}

func TestRowStore_AppendBatch_NullCell_DecodesAsNull(t *testing.T) {
	nulls := []uint64{0b0001} // row 0 is null
	b := &batch.Batch{
		Length:  1,
		Schema:  []batch.ColumnMeta{intColMeta("v")},
		Columns: []batch.ColumnVector{&batch.Int32Vector{Values: []int32{0}, Nulls: nulls}},
	}
	var m rowStore
	if err := m.appendBatch(b); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.decodeCell(0, 0).Kind != expr.KindNull {
		t.Errorf("null cell should decode as KindNull, got %+v", m.decodeCell(0, 0))
	}
}

func TestRowStore_AppendBatch_AllTypes_Extracted(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	dur := 5 * time.Second
	b := &batch.Batch{
		Length: 1,
		Schema: []batch.ColumnMeta{
			{Name: "i32"}, {Name: "i64"}, {Name: "f64"},
			{Name: "str"}, {Name: "bool"}, {Name: "dt"}, {Name: "ts"},
		},
		Columns: []batch.ColumnVector{
			&batch.Int32Vector{Values: []int32{1}},
			&batch.Int64Vector{Values: []int64{2}},
			&batch.Float64Vector{Values: []float64{3.14}},
			&batch.StringVector{Values: []string{"hello"}},
			&batch.BoolVector{Values: []bool{true}},
			&batch.DatetimeVector{Values: []time.Time{now}},
			&batch.TimespanVector{Values: []time.Duration{dur}},
		},
	}
	var m rowStore
	if err := m.appendBatch(b); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v := m.decodeCell(0, 0); v.Kind != expr.KindInt32 || int32(v.I) != 1 {
		t.Errorf("col 0 = %+v, want KindInt32 I=1", v)
	}
	if v := m.decodeCell(0, 1); v.Kind != expr.KindInt64 || v.I != 2 {
		t.Errorf("col 1 = %+v, want KindInt64 I=2", v)
	}
	if v := m.decodeCell(0, 2); v.Kind != expr.KindFloat64 || v.F != 3.14 {
		t.Errorf("col 2 = %+v, want KindFloat64 F=3.14", v)
	}
	if v := m.decodeCell(0, 3); v.Kind != expr.KindString || v.S != "hello" {
		t.Errorf("col 3 = %+v, want KindString S=hello", v)
	}
	if v := m.decodeCell(0, 4); v.Kind != expr.KindBool || v.I != 1 {
		t.Errorf("col 4 = %+v, want KindBool I=1", v)
	}
	if v := m.decodeCell(0, 5); v.Kind != expr.KindDatetime || v.I != now.UnixNano() {
		t.Errorf("col 5 = %+v, want KindDatetime I=%d", v, now.UnixNano())
	}
	if v := m.decodeCell(0, 6); v.Kind != expr.KindTimespan || v.I != int64(dur) {
		t.Errorf("col 6 = %+v, want KindTimespan I=%d", v, int64(dur))
	}
}

func TestRowStore_AppendBatch_MultiColumn_NullsPerColumn(t *testing.T) {
	// Two columns; col 0 null at row 1, col 1 null at row 0.
	nulls0 := []uint64{0b0010}
	nulls1 := []uint64{0b0001}
	b := &batch.Batch{
		Length: 2,
		Schema: []batch.ColumnMeta{{Name: "a"}, {Name: "b"}},
		Columns: []batch.ColumnVector{
			&batch.Int32Vector{Values: []int32{10, 0}, Nulls: nulls0},
			&batch.Int32Vector{Values: []int32{0, 20}, Nulls: nulls1},
		},
	}
	var m rowStore
	if err := m.appendBatch(b); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// row 0: col 0 not null, col 1 null
	if m.decodeCell(0, 0).Kind == expr.KindNull {
		t.Error("row 0 col 0 should not be null")
	}
	if m.decodeCell(0, 1).Kind != expr.KindNull {
		t.Error("row 0 col 1 should be null")
	}
	// row 1: col 0 null, col 1 not null
	if m.decodeCell(1, 0).Kind != expr.KindNull {
		t.Error("row 1 col 0 should be null")
	}
	if m.decodeCell(1, 1).Kind == expr.KindNull {
		t.Error("row 1 col 1 should not be null")
	}
	if v := m.decodeCell(0, 0); int32(v.I) != 10 {
		t.Errorf("row 0 col 0 = %v, want int32(10)", v)
	}
	if v := m.decodeCell(1, 1); int32(v.I) != 20 {
		t.Errorf("row 1 col 1 = %v, want int32(20)", v)
	}
}

func TestRowStore_AppendBatch_SchemaMismatch_IsError(t *testing.T) {
	var m rowStore
	if err := m.appendBatch(int32Batch([]int32{1})); err != nil {
		t.Fatalf("first append error: %v", err)
	}
	b2 := &batch.Batch{
		Length: 1,
		Schema: []batch.ColumnMeta{{Name: "v"}, {Name: "w"}},
		Columns: []batch.ColumnVector{
			&batch.Int32Vector{Values: []int32{2}},
			&batch.Int32Vector{Values: []int32{3}},
		},
	}
	if err := m.appendBatch(b2); err == nil {
		t.Fatal("expected schema mismatch error, got nil")
	}
}

// ─── buildBatchFromRows ───────────────────────────────────────────────────────

func TestBuildBatchFromRows_RoundTrip_Values(t *testing.T) {
	var m rowStore
	if err := m.appendBatch(int32Batch([]int32{10, 20, 30})); err != nil {
		t.Fatalf("appendBatch: %v", err)
	}
	b := m.buildBatchFromRows(0, 3)
	if b.Length != 3 {
		t.Fatalf("Length = %d, want 3", b.Length)
	}
	vec := b.Columns[0].(*batch.Int32Vector)
	if vec.Values[0] != 10 || vec.Values[1] != 20 || vec.Values[2] != 30 {
		t.Errorf("values = %v, want [10 20 30]", vec.Values)
	}
}

func TestBuildBatchFromRows_SubRange(t *testing.T) {
	var m rowStore
	if err := m.appendBatch(int32Batch([]int32{1, 2, 3, 4, 5})); err != nil {
		t.Fatalf("appendBatch: %v", err)
	}
	b := m.buildBatchFromRows(1, 4) // rows 1,2,3 → values 2,3,4
	if b.Length != 3 {
		t.Fatalf("Length = %d, want 3", b.Length)
	}
	vec := b.Columns[0].(*batch.Int32Vector)
	if vec.Values[0] != 2 || vec.Values[1] != 3 || vec.Values[2] != 4 {
		t.Errorf("values = %v, want [2 3 4]", vec.Values)
	}
}

func TestBuildBatchFromRows_SchemaPreserved(t *testing.T) {
	var m rowStore
	if err := m.appendBatch(int32Batch([]int32{1})); err != nil {
		t.Fatalf("appendBatch: %v", err)
	}
	b := m.buildBatchFromRows(0, 1)
	if len(b.Schema) != 1 || b.Schema[0].Name != "v" || b.Schema[0].Origin != "t" {
		t.Errorf("Schema = %+v, want [{v t}]", b.Schema)
	}
}

func TestBuildBatchFromRows_NullsPreserved(t *testing.T) {
	nulls := []uint64{0b0010} // row 1 null
	b := &batch.Batch{
		Length:  3,
		Schema:  []batch.ColumnMeta{intColMeta("v")},
		Columns: []batch.ColumnVector{&batch.Int32Vector{Values: []int32{10, 0, 30}, Nulls: nulls}},
	}
	var m rowStore
	if err := m.appendBatch(b); err != nil {
		t.Fatalf("appendBatch: %v", err)
	}
	got := m.buildBatchFromRows(0, 3)
	vec := got.Columns[0].(*batch.Int32Vector)
	if !vec.IsNull(1) {
		t.Errorf("row 1 IsNull = false, want true")
	}
	if vec.IsNull(0) || vec.IsNull(2) {
		t.Errorf("rows 0 and 2 should not be null")
	}
	if vec.Values[0] != 10 || vec.Values[2] != 30 {
		t.Errorf("non-null values wrong: %v", vec.Values)
	}
}

func TestBuildBatchFromRows_MultiBatch_RoundTrip(t *testing.T) {
	var m rowStore
	if err := m.appendBatch(int32Batch([]int32{1, 2})); err != nil {
		t.Fatalf("batch 1: %v", err)
	}
	if err := m.appendBatch(int32Batch([]int32{3, 4, 5})); err != nil {
		t.Fatalf("batch 2: %v", err)
	}
	b1 := m.buildBatchFromRows(0, 3)
	b2 := m.buildBatchFromRows(3, 5)
	if b1.Length != 3 || b2.Length != 2 {
		t.Fatalf("lengths = %d %d, want 3 2", b1.Length, b2.Length)
	}
	v1 := b1.Columns[0].(*batch.Int32Vector)
	v2 := b2.Columns[0].(*batch.Int32Vector)
	if v1.Values[2] != 3 {
		t.Errorf("b1 row 2 = %d, want 3", v1.Values[2])
	}
	if v2.Values[1] != 5 {
		t.Errorf("b2 row 1 = %d, want 5", v2.Values[1])
	}
}

func TestBuildBatchFromRows_AllTypes_RoundTrip(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	dur := 5 * time.Second
	src := &batch.Batch{
		Length: 1,
		Schema: []batch.ColumnMeta{
			{Name: "i32"}, {Name: "i64"}, {Name: "f64"},
			{Name: "str"}, {Name: "bool"}, {Name: "dt"}, {Name: "ts"},
		},
		Columns: []batch.ColumnVector{
			&batch.Int32Vector{Values: []int32{1}},
			&batch.Int64Vector{Values: []int64{2}},
			&batch.Float64Vector{Values: []float64{3.14}},
			&batch.StringVector{Values: []string{"hello"}},
			&batch.BoolVector{Values: []bool{true}},
			&batch.DatetimeVector{Values: []time.Time{now}},
			&batch.TimespanVector{Values: []time.Duration{dur}},
		},
	}
	var m rowStore
	if err := m.appendBatch(src); err != nil {
		t.Fatalf("appendBatch: %v", err)
	}
	got := m.buildBatchFromRows(0, 1)
	if _, ok := got.Columns[0].(*batch.Int32Vector); !ok {
		t.Errorf("col 0 type = %T, want *batch.Int32Vector", got.Columns[0])
	}
	if _, ok := got.Columns[1].(*batch.Int64Vector); !ok {
		t.Errorf("col 1 type = %T, want *batch.Int64Vector", got.Columns[1])
	}
	if _, ok := got.Columns[5].(*batch.DatetimeVector); !ok {
		t.Errorf("col 5 type = %T, want *batch.DatetimeVector", got.Columns[5])
	}
	if _, ok := got.Columns[6].(*batch.TimespanVector); !ok {
		t.Errorf("col 6 type = %T, want *batch.TimespanVector", got.Columns[6])
	}
}

func TestBuildBatchFromRows_AllNull_ProducesCorrectVectorType(t *testing.T) {
	nulls := []uint64{0b0001}
	b := &batch.Batch{
		Length:  1,
		Schema:  []batch.ColumnMeta{{Name: "v", Origin: "t"}},
		Columns: []batch.ColumnVector{&batch.Int64Vector{Values: []int64{0}, Nulls: nulls}},
	}
	var m rowStore
	if err := m.appendBatch(b); err != nil {
		t.Fatalf("appendBatch: %v", err)
	}
	got := m.buildBatchFromRows(0, 1)
	if _, ok := got.Columns[0].(*batch.Int64Vector); !ok {
		t.Errorf("all-null col type = %T, want *batch.Int64Vector", got.Columns[0])
	}
	if !got.Columns[0].IsNull(0) {
		t.Errorf("row 0 IsNull = false, want true")
	}
}
