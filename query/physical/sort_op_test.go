package physical

import (
	"io"
	"testing"
	"time"

	"github.com/kozwoj/gobbler-query/query/batch"
)

// ─── compareAny ──────────────────────────────────────────────────────────────

func TestCompareAny_BothNull(t *testing.T) {
	if got := compareAny(nil, nil, true, true); got != 0 {
		t.Errorf("both null = %d, want 0", got)
	}
}

func TestCompareAny_LeftNull(t *testing.T) {
	if got := compareAny(nil, int32(1), true, false); got != 1 {
		t.Errorf("left null = %d, want 1", got)
	}
}

func TestCompareAny_RightNull(t *testing.T) {
	if got := compareAny(int32(1), nil, false, true); got != -1 {
		t.Errorf("right null = %d, want -1", got)
	}
}

func TestCompareAny_Int32(t *testing.T) {
	cases := []struct {
		a, b int32
		want int
	}{{1, 2, -1}, {2, 2, 0}, {3, 2, 1}}
	for _, c := range cases {
		if got := compareAny(c.a, c.b, false, false); got != c.want {
			t.Errorf("compareAny(%d,%d) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestCompareAny_Int64(t *testing.T) {
	if got := compareAny(int64(10), int64(20), false, false); got != -1 {
		t.Errorf("int64 = %d, want -1", got)
	}
}

func TestCompareAny_Float64(t *testing.T) {
	if got := compareAny(float64(1.5), float64(1.5), false, false); got != 0 {
		t.Errorf("float64 eq = %d, want 0", got)
	}
}

func TestCompareAny_String(t *testing.T) {
	if got := compareAny("apple", "banana", false, false); got != -1 {
		t.Errorf("string = %d, want -1", got)
	}
}

func TestCompareAny_Bool(t *testing.T) {
	// false < true
	if got := compareAny(false, true, false, false); got != -1 {
		t.Errorf("bool false<true = %d, want -1", got)
	}
	if got := compareAny(true, false, false, false); got != 1 {
		t.Errorf("bool true>false = %d, want 1", got)
	}
}

func TestCompareAny_Datetime(t *testing.T) {
	earlier := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	later := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := compareAny(earlier, later, false, false); got != -1 {
		t.Errorf("datetime earlier<later = %d, want -1", got)
	}
	if got := compareAny(later, earlier, false, false); got != 1 {
		t.Errorf("datetime later>earlier = %d, want 1", got)
	}
	if got := compareAny(earlier, earlier, false, false); got != 0 {
		t.Errorf("datetime equal = %d, want 0", got)
	}
}

func TestCompareAny_Timespan(t *testing.T) {
	if got := compareAny(time.Second, 2*time.Second, false, false); got != -1 {
		t.Errorf("duration = %d, want -1", got)
	}
}

// ─── compareRows ─────────────────────────────────────────────────────────────

func makeRow(vals ...any) ([]any, []bool) {
	nulls := make([]bool, len(vals))
	for i, v := range vals {
		if v == nil {
			nulls[i] = true
		}
	}
	return vals, nulls
}

func TestCompareRows_SingleKeyAsc(t *testing.T) {
	a, aN := makeRow(int32(1))
	b, bN := makeRow(int32(2))
	keys := []CompiledSortKey{{ColIdx: 0, Desc: false}}
	if !compareRows(a, b, aN, bN, keys) {
		t.Error("1 < 2 asc: expected true")
	}
	if compareRows(b, a, bN, aN, keys) {
		t.Error("2 < 1 asc: expected false")
	}
}

func TestCompareRows_SingleKeyDesc(t *testing.T) {
	a, aN := makeRow(int32(1))
	b, bN := makeRow(int32(2))
	keys := []CompiledSortKey{{ColIdx: 0, Desc: true}}
	// Desc: larger value sorts first, so 2 < 1 in desc order
	if !compareRows(b, a, bN, aN, keys) {
		t.Error("2 < 1 Desc: expected true")
	}
}

func TestCompareRows_TieBreak(t *testing.T) {
	a, aN := makeRow(int32(1), "alpha")
	b, bN := makeRow(int32(1), "beta")
	keys := []CompiledSortKey{{ColIdx: 0, Desc: false}, {ColIdx: 1, Desc: false}}
	if !compareRows(a, b, aN, bN, keys) {
		t.Error("tie-break alpha<beta: expected true")
	}
}

func TestCompareRows_AllEqual_ReturnsFalse(t *testing.T) {
	a, aN := makeRow(int32(5), "x")
	b, bN := makeRow(int32(5), "x")
	keys := []CompiledSortKey{{ColIdx: 0, Desc: false}, {ColIdx: 1, Desc: false}}
	if compareRows(a, b, aN, bN, keys) {
		t.Error("equal rows: expected false")
	}
}

func TestCompareRows_NullSortsLast_Asc(t *testing.T) {
	a, aN := makeRow(nil)
	b, bN := makeRow(int32(1))
	keys := []CompiledSortKey{{ColIdx: 0, Desc: false}}
	// null sorts after non-null in asc
	if compareRows(a, b, aN, bN, keys) {
		t.Error("null vs non-null asc: null should sort last (false)")
	}
	if !compareRows(b, a, bN, aN, keys) {
		t.Error("non-null vs null asc: non-null should sort first (true)")
	}
}

func TestCompareRows_NullSortsLast_Desc(t *testing.T) {
	a, aN := makeRow(nil)
	b, bN := makeRow(int32(1))
	keys := []CompiledSortKey{{ColIdx: 0, Desc: true}}
	// null sorts after non-null even in desc
	if compareRows(a, b, aN, bN, keys) {
		t.Error("null vs non-null Desc: null should still sort last (false)")
	}
}

// ─── SortOp ──────────────────────────────────────────────────────────────────

func int32SortBatch(vals []int32) *batch.Batch {
	return &batch.Batch{
		Length: len(vals),
		Schema: []batch.ColumnMeta{{Name: "v", Origin: "t"}},
		Columns: []batch.ColumnVector{
			&batch.Int32Vector{Values: append([]int32{}, vals...)},
		},
	}
}

func collectSortOp(t *testing.T, op *SortOp) []int32 {
	t.Helper()
	var out []int32
	for {
		b, err := op.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next() error = %v", err)
		}
		for _, v := range b.Columns[0].(*batch.Int32Vector).Values {
			out = append(out, v)
		}
	}
	return out
}

func TestSortOp_SingleBatch_Asc(t *testing.T) {
	input := &fakeOperator{batches: []*batch.Batch{int32SortBatch([]int32{3, 1, 2})}}
	op := &SortOp{
		Input: input,
		Keys:  []CompiledSortKey{{ColIdx: 0, Desc: false}},
	}
	got := collectSortOp(t, op)
	want := []int32{1, 2, 3}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("row %d = %d, want %d", i, got[i], v)
		}
	}
}

func TestSortOp_SingleBatch_Desc(t *testing.T) {
	input := &fakeOperator{batches: []*batch.Batch{int32SortBatch([]int32{3, 1, 2})}}
	op := &SortOp{
		Input: input,
		Keys:  []CompiledSortKey{{ColIdx: 0, Desc: true}},
	}
	got := collectSortOp(t, op)
	want := []int32{3, 2, 1}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("row %d = %d, want %d", i, got[i], v)
		}
	}
}

func TestSortOp_MultiBatch_Sorted(t *testing.T) {
	input := &fakeOperator{batches: []*batch.Batch{
		int32SortBatch([]int32{5, 3}),
		int32SortBatch([]int32{1, 4, 2}),
	}}
	op := &SortOp{
		Input: input,
		Keys:  []CompiledSortKey{{ColIdx: 0, Desc: false}},
	}
	got := collectSortOp(t, op)
	want := []int32{1, 2, 3, 4, 5}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("row %d = %d, want %d", i, got[i], v)
		}
	}
}

func TestSortOp_EmptyInput_ReturnsEOF(t *testing.T) {
	op := &SortOp{
		Input: &fakeOperator{batches: nil},
		Keys:  []CompiledSortKey{{ColIdx: 0, Desc: false}},
	}
	_, err := op.Next()
	if err != io.EOF {
		t.Errorf("empty input: err = %v, want io.EOF", err)
	}
}

func TestSortOp_BatchSizeRespected(t *testing.T) {
	// 5 rows, BatchSize=2 → 3 output batches (2,2,1)
	input := &fakeOperator{batches: []*batch.Batch{int32SortBatch([]int32{5, 4, 3, 2, 1})}}
	op := &SortOp{
		Input:     input,
		Keys:      []CompiledSortKey{{ColIdx: 0, Desc: false}},
		BatchSize: 2,
	}
	var lengths []int
	for {
		b, err := op.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next() error = %v", err)
		}
		lengths = append(lengths, b.Length)
	}
	if len(lengths) != 3 || lengths[0] != 2 || lengths[1] != 2 || lengths[2] != 1 {
		t.Errorf("batch lengths = %v, want [2 2 1]", lengths)
	}
}

func TestSortOp_StableSort_TiedKeys(t *testing.T) {
	// Rows interleave two key groups (1 and 2) so the sort must actually move
	// rows. Within each tied group the original relative order must be kept.
	// Input:  k=2 v="a",  k=1 v="b",  k=2 v="c",  k=1 v="d"
	// Sorted: k=1 v="b",  k=1 v="d",  k=2 v="a",  k=2 v="c"
	b := &batch.Batch{
		Length: 4,
		Schema: []batch.ColumnMeta{{Name: "k"}, {Name: "v"}},
		Columns: []batch.ColumnVector{
			&batch.Int32Vector{Values: []int32{2, 1, 2, 1}},
			&batch.StringVector{Values: []string{"a", "b", "c", "d"}},
		},
	}
	input := &fakeOperator{batches: []*batch.Batch{b}}
	op := &SortOp{
		Input: input,
		Keys:  []CompiledSortKey{{ColIdx: 0, Desc: false}},
	}
	got, err := op.Next()
	if err != nil {
		t.Fatalf("Next() = %v", err)
	}
	vals := got.Columns[1].(*batch.StringVector).Values
	want := []string{"b", "d", "a", "c"}
	for i, w := range want {
		if vals[i] != w {
			t.Errorf("row %d: got %q, want %q (full: %v)", i, vals[i], w, vals)
		}
	}
}

func TestSortOp_NullSortsLast(t *testing.T) {
	nullBits := []uint64{0b0010} // row 1 is null
	b := &batch.Batch{
		Length: 3,
		Schema: []batch.ColumnMeta{{Name: "v"}},
		Columns: []batch.ColumnVector{
			&batch.Int32Vector{Values: []int32{3, 0, 1}, Nulls: nullBits},
		},
	}
	input := &fakeOperator{batches: []*batch.Batch{b}}
	op := &SortOp{
		Input: input,
		Keys:  []CompiledSortKey{{ColIdx: 0, Desc: false}},
	}
	got, err := op.Next()
	if err != nil {
		t.Fatalf("Next() = %v", err)
	}
	col := got.Columns[0].(*batch.Int32Vector)
	// Expect order: 1, 3, null
	if col.Values[0] != 1 || col.Values[1] != 3 {
		t.Errorf("sorted values = %v, want [1 3 <null>]", col.Values)
	}
	if !got.Columns[0].IsNull(2) {
		t.Errorf("row 2 should be null")
	}
}

func TestSortOp_Close_Delegates(t *testing.T) {
	input := &fakeOperator{closeErr: io.ErrUnexpectedEOF}
	op := &SortOp{Input: input, Keys: []CompiledSortKey{{ColIdx: 0}}}
	if err := op.Close(); err != io.ErrUnexpectedEOF {
		t.Errorf("Close() = %v, want ErrUnexpectedEOF", err)
	}
}
