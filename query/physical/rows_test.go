package physical

import (
	"testing"
	"time"

	"github.com/kozwoj/gobbler-query/query/batch"
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

func TestMaterializedRows_AppendBatch_Empty_IsNoop(t *testing.T) {
	var m materializedRows
	b := &batch.Batch{Length: 0, Schema: []batch.ColumnMeta{intColMeta("v")}}
	if err := m.appendBatch(b); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.Rows) != 0 {
		t.Errorf("Rows len = %d, want 0", len(m.Rows))
	}
	if m.Schema != nil {
		t.Errorf("Schema should remain nil after empty batch")
	}
}

func TestMaterializedRows_AppendBatch_SingleBatch_RowCount(t *testing.T) {
	var m materializedRows
	if err := m.appendBatch(int32Batch([]int32{10, 20, 30})); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.Rows) != 3 {
		t.Fatalf("Rows len = %d, want 3", len(m.Rows))
	}
}

func TestMaterializedRows_AppendBatch_SchemaSetFromFirstBatch(t *testing.T) {
	var m materializedRows
	if err := m.appendBatch(int32Batch([]int32{1})); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.Schema) != 1 || m.Schema[0].Name != "v" {
		t.Errorf("Schema = %+v, want [{v t}]", m.Schema)
	}
}

func TestMaterializedRows_AppendBatch_ValuesExtracted(t *testing.T) {
	var m materializedRows
	if err := m.appendBatch(int32Batch([]int32{100, 200})); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Rows[0][0] != int32(100) {
		t.Errorf("Rows[0][0] = %v, want int32(100)", m.Rows[0][0])
	}
	if m.Rows[1][0] != int32(200) {
		t.Errorf("Rows[1][0] = %v, want int32(200)", m.Rows[1][0])
	}
}

func TestMaterializedRows_AppendBatch_MultipleBatches_Accumulate(t *testing.T) {
	var m materializedRows
	if err := m.appendBatch(int32Batch([]int32{1, 2})); err != nil {
		t.Fatalf("batch 1 error: %v", err)
	}
	if err := m.appendBatch(int32Batch([]int32{3, 4, 5})); err != nil {
		t.Fatalf("batch 2 error: %v", err)
	}
	if len(m.Rows) != 5 {
		t.Fatalf("Rows len = %d, want 5", len(m.Rows))
	}
	if m.Rows[4][0] != int32(5) {
		t.Errorf("Rows[4][0] = %v, want int32(5)", m.Rows[4][0])
	}
}

func TestMaterializedRows_AppendBatch_NullCell_FlagSet(t *testing.T) {
	nulls := []uint64{0b0010} // row 1 is null
	b := &batch.Batch{
		Length:  3,
		Schema:  []batch.ColumnMeta{intColMeta("v")},
		Columns: []batch.ColumnVector{&batch.Int32Vector{Values: []int32{10, 0, 30}, Nulls: nulls}},
	}
	var m materializedRows
	if err := m.appendBatch(b); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Nulls[0][0] {
		t.Errorf("row 0 should not be null")
	}
	if !m.Nulls[1][0] {
		t.Errorf("row 1 should be null")
	}
	if m.Nulls[2][0] {
		t.Errorf("row 2 should not be null")
	}
}

func TestMaterializedRows_AppendBatch_NullCell_ValueIsZero(t *testing.T) {
	nulls := []uint64{0b0001} // row 0 is null
	b := &batch.Batch{
		Length:  1,
		Schema:  []batch.ColumnMeta{intColMeta("v")},
		Columns: []batch.ColumnVector{&batch.Int32Vector{Values: []int32{0}, Nulls: nulls}},
	}
	var m materializedRows
	if err := m.appendBatch(b); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Rows[0][0] != nil {
		t.Errorf("null cell value should be nil, got %v", m.Rows[0][0])
	}
}

func TestMaterializedRows_AppendBatch_AllTypes_Extracted(t *testing.T) {
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
	var m materializedRows
	if err := m.appendBatch(b); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	row := m.Rows[0]
	if row[0] != int32(1) {
		t.Errorf("col 0 = %v (%T), want int32(1)", row[0], row[0])
	}
	if row[1] != int64(2) {
		t.Errorf("col 1 = %v (%T), want int64(2)", row[1], row[1])
	}
	if row[2] != float64(3.14) {
		t.Errorf("col 2 = %v, want 3.14", row[2])
	}
	if row[3] != "hello" {
		t.Errorf("col 3 = %v, want hello", row[3])
	}
	if row[4] != true {
		t.Errorf("col 4 = %v, want true", row[4])
	}
	if row[5] != now {
		t.Errorf("col 5 = %v, want %v", row[5], now)
	}
	if row[6] != dur {
		t.Errorf("col 6 = %v, want %v", row[6], dur)
	}
}

func TestMaterializedRows_AppendBatch_MultiColumn_NullsPerColumn(t *testing.T) {
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
	var m materializedRows
	if err := m.appendBatch(b); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Nulls[0][0] || !m.Nulls[0][1] {
		t.Errorf("row 0: col 0 null=%v col 1 null=%v, want false true", m.Nulls[0][0], m.Nulls[0][1])
	}
	if !m.Nulls[1][0] || m.Nulls[1][1] {
		t.Errorf("row 1: col 0 null=%v col 1 null=%v, want true false", m.Nulls[1][0], m.Nulls[1][1])
	}
	if m.Rows[0][0] != int32(10) {
		t.Errorf("row 0 col 0 = %v, want int32(10)", m.Rows[0][0])
	}
	if m.Rows[1][1] != int32(20) {
		t.Errorf("row 1 col 1 = %v, want int32(20)", m.Rows[1][1])
	}
}

func TestMaterializedRows_AppendBatch_SchemaMismatch_IsError(t *testing.T) {
	var m materializedRows
	if err := m.appendBatch(int32Batch([]int32{1})); err != nil {
		t.Fatalf("first append error: %v", err)
	}
	// Second batch has 2 columns; first had 1.
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
	var m materializedRows
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
	var m materializedRows
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
	var m materializedRows
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
	var m materializedRows
	if err := m.appendBatch(b); err != nil {
		t.Fatalf("appendBatch: %v", err)
	}
	got := m.buildBatchFromRows(0, 3)
	vec := got.Columns[0].(*batch.Int32Vector)
	if vec.IsNull(1) != true {
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
	var m materializedRows
	if err := m.appendBatch(int32Batch([]int32{1, 2})); err != nil {
		t.Fatalf("batch 1: %v", err)
	}
	if err := m.appendBatch(int32Batch([]int32{3, 4, 5})); err != nil {
		t.Fatalf("batch 2: %v", err)
	}
	// Emit as two batches: [0,3) and [3,5)
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
	var m materializedRows
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
	// All rows null in an int64 column — ColKinds must drive the output type.
	nulls := []uint64{0b0001}
	b := &batch.Batch{
		Length:  1,
		Schema:  []batch.ColumnMeta{{Name: "v", Origin: "t"}},
		Columns: []batch.ColumnVector{&batch.Int64Vector{Values: []int64{0}, Nulls: nulls}},
	}
	var m materializedRows
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
