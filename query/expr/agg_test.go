package expr

import (
	"testing"
	"time"

	"github.com/kozwoj/gobbler-query/query/ast"
	"github.com/kozwoj/gobbler-query/query/source"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// anyToValue converts a raw Go value to a Value for use in tests.
// nil becomes KindNull.
func anyToValue(v any) Value {
	switch x := v.(type) {
	case nil:
		return Value{Kind: KindNull}
	case int32:
		return Value{Kind: KindInt32, I: int64(x)}
	case int64:
		return Value{Kind: KindInt64, I: x}
	case float64:
		return Value{Kind: KindFloat64, F: x}
	case bool:
		i := int64(0)
		if x {
			i = 1
		}
		return Value{Kind: KindBool, I: i}
	case string:
		return Value{Kind: KindString, S: x}
	case time.Time:
		return Value{Kind: KindDatetime, I: x.UnixNano()}
	case time.Duration:
		return Value{Kind: KindTimespan, I: int64(x)}
	default:
		panic("anyToValue: unsupported type")
	}
}

func ingest(acc AggAccumulator, vals ...any) {
	for _, v := range vals {
		acc.Ingest(anyToValue(v))
	}
}

// ─── countAcc ────────────────────────────────────────────────────────────────

func TestCountAcc_CountsRows(t *testing.T) {
	a := &countAcc{}
	ingest(a, int32(1), nil, int32(3))
	got := a.Result()
	if got.Kind == KindNull {
		t.Fatal("count result should not be null")
	}
	if got.I != 3 {
		t.Errorf("count = %v, want 3", got.I)
	}
}

func TestCountAcc_Empty(t *testing.T) {
	a := &countAcc{}
	got := a.Result()
	if got.Kind == KindNull {
		t.Fatal("empty count should not be null")
	}
	if got.I != 0 {
		t.Errorf("empty count = %v, want 0", got.I)
	}
}

// ─── sumInt64Acc ─────────────────────────────────────────────────────────────

func TestSumInt64Acc_Int64Values(t *testing.T) {
	a := &sumInt64Acc{}
	ingest(a, int64(10), int64(20), nil, int64(5))
	got := a.Result()
	if got.Kind == KindNull {
		t.Fatal("sum result should not be null")
	}
	if got.I != 35 {
		t.Errorf("sum = %v, want 35", got.I)
	}
}

func TestSumInt64Acc_Int32Values(t *testing.T) {
	// int32 inputs are widened to int64 by the accumulator
	a := &sumInt64Acc{}
	ingest(a, int32(3), int32(4))
	got := a.Result()
	if got.Kind == KindNull {
		t.Fatal("sum result should not be null")
	}
	if got.I != 7 {
		t.Errorf("sum = %v, want 7", got.I)
	}
}

func TestSumInt64Acc_AllNull(t *testing.T) {
	a := &sumInt64Acc{}
	ingest(a, nil, nil)
	got := a.Result()
	if got.Kind == KindNull {
		t.Fatal("sum result should not be null even when all inputs are null")
	}
	if got.I != 0 {
		t.Errorf("all-null sum = %v, want 0", got.I)
	}
}

// ─── sumFloat64Acc ───────────────────────────────────────────────────────────

func TestSumFloat64Acc_Basic(t *testing.T) {
	a := &sumFloat64Acc{}
	ingest(a, 1.5, nil, 2.5)
	got := a.Result()
	if got.Kind == KindNull {
		t.Fatal("sum result should not be null")
	}
	if got.F != 4.0 {
		t.Errorf("sum = %v, want 4.0", got.F)
	}
}

// ─── avgAcc ──────────────────────────────────────────────────────────────────

func TestAvgAcc_Float64(t *testing.T) {
	a := &avgAcc{}
	ingest(a, 10.0, 20.0, 30.0)
	got := a.Result()
	if got.Kind == KindNull {
		t.Fatal("avg result should not be null")
	}
	if got.F != 20.0 {
		t.Errorf("avg = %v, want 20.0", got.F)
	}
}

func TestAvgAcc_Int32(t *testing.T) {
	a := &avgAcc{}
	ingest(a, int32(1), int32(3))
	got := a.Result()
	if got.Kind == KindNull {
		t.Fatal("avg result should not be null")
	}
	if got.F != 2.0 {
		t.Errorf("avg = %v, want 2.0", got.F)
	}
}

func TestAvgAcc_SkipsNulls(t *testing.T) {
	a := &avgAcc{}
	ingest(a, nil, 10.0, nil, 20.0)
	got := a.Result()
	if got.F != 15.0 {
		t.Errorf("avg skipping nulls = %v, want 15.0", got.F)
	}
}

func TestAvgAcc_AllNull_ReturnsNull(t *testing.T) {
	a := &avgAcc{}
	ingest(a, nil, nil)
	got := a.Result()
	if got.Kind != KindNull {
		t.Error("all-null avg should return null")
	}
}

// ─── cmpVal ──────────────────────────────────────────────────────────────────

func TestCmpVal_Int32(t *testing.T) {
	if CmpValue(anyToValue(int32(1)), anyToValue(int32(2))) != -1 {
		t.Error("1 < 2")
	}
	if CmpValue(anyToValue(int32(2)), anyToValue(int32(2))) != 0 {
		t.Error("2 == 2")
	}
	if CmpValue(anyToValue(int32(3)), anyToValue(int32(2))) != 1 {
		t.Error("3 > 2")
	}
}

func TestCmpVal_Float64(t *testing.T) {
	if CmpValue(anyToValue(1.0), anyToValue(2.0)) != -1 {
		t.Error("1.0 < 2.0")
	}
}

func TestCmpVal_Datetime(t *testing.T) {
	earlier := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	later := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
	if CmpValue(anyToValue(earlier), anyToValue(later)) != -1 {
		t.Error("earlier < later")
	}
	if CmpValue(anyToValue(later), anyToValue(later)) != 0 {
		t.Error("equal datetimes")
	}
}

func TestCmpVal_Timespan(t *testing.T) {
	if CmpValue(anyToValue(time.Second), anyToValue(2*time.Second)) != -1 {
		t.Error("1s < 2s")
	}
}

// ─── minAcc ──────────────────────────────────────────────────────────────────

func TestMinAcc_Int32(t *testing.T) {
	a := &minAcc{}
	ingest(a, int32(5), int32(2), int32(8))
	got := a.Result()
	if got.Kind == KindNull {
		t.Fatal("min should not be null")
	}
	if int32(got.I) != 2 {
		t.Errorf("min = %v, want 2", got.I)
	}
}

func TestMinAcc_SkipsNulls(t *testing.T) {
	a := &minAcc{}
	ingest(a, nil, int32(3), nil)
	got := a.Result()
	if got.Kind == KindNull {
		t.Fatal("min should not be null")
	}
	if int32(got.I) != 3 {
		t.Errorf("min = %v, want 3", got.I)
	}
}

func TestMinAcc_AllNull_ReturnsNull(t *testing.T) {
	a := &minAcc{}
	ingest(a, nil, nil)
	got := a.Result()
	if got.Kind != KindNull {
		t.Error("all-null min should return null")
	}
}

func TestMinAcc_Datetime(t *testing.T) {
	t1 := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC)
	a := &minAcc{}
	ingest(a, t1, t2)
	got := a.Result()
	wantNano := t2.UnixNano()
	if got.Kind != KindDatetime || got.I != wantNano {
		t.Errorf("min datetime nano = %v, want %v", got.I, wantNano)
	}
}

// ─── maxAcc ──────────────────────────────────────────────────────────────────

func TestMaxAcc_Int64(t *testing.T) {
	a := &maxAcc{}
	ingest(a, int64(1), int64(9), int64(4))
	got := a.Result()
	if got.Kind == KindNull {
		t.Fatal("max should not be null")
	}
	if got.I != 9 {
		t.Errorf("max = %v, want 9", got.I)
	}
}

func TestMaxAcc_AllNull_ReturnsNull(t *testing.T) {
	a := &maxAcc{}
	ingest(a, nil)
	got := a.Result()
	if got.Kind != KindNull {
		t.Error("all-null max should return null")
	}
}

// ─── dcountAcc ───────────────────────────────────────────────────────────────

func TestDcountAcc_CountsDistinct(t *testing.T) {
	a := &dcountAcc{}
	ingest(a, "east", "west", "east", "north")
	got := a.Result()
	if got.Kind == KindNull {
		t.Fatal("dcount should not be null")
	}
	if got.I != 3 {
		t.Errorf("dcount = %v, want 3", got.I)
	}
}

func TestDcountAcc_SkipsNulls(t *testing.T) {
	a := &dcountAcc{}
	ingest(a, nil, "east", nil)
	got := a.Result()
	if got.I != 1 {
		t.Errorf("dcount skipping nulls = %v, want 1", got.I)
	}
}

func TestDcountAcc_Empty(t *testing.T) {
	a := &dcountAcc{}
	got := a.Result()
	if got.Kind == KindNull {
		t.Fatal("empty dcount should not be null")
	}
	if got.I != 0 {
		t.Errorf("empty dcount = %v, want 0", got.I)
	}
}

// ─── CompileAggItem ──────────────────────────────────────────────────────────

func field(name string) *ast.FieldRef { return &ast.FieldRef{Name: name} }

func TestCompileAggItem_Count_NameAndType(t *testing.T) {
	item := ast.AggItem{Alias: "total", Call: ast.AggCall{Func: ast.AggCount}}
	c := CompileAggItem(item, source.TypeInt64)
	if c.Name != "total" {
		t.Errorf("name = %q, want total", c.Name)
	}
	if c.Type != source.TypeInt64 {
		t.Errorf("type = %v, want TypeInt64", c.Type)
	}
	if c.Eval != nil {
		t.Error("count Eval should be nil")
	}
}

func TestCompileAggItem_Count_DefaultName(t *testing.T) {
	item := ast.AggItem{Call: ast.AggCall{Func: ast.AggCount}}
	c := CompileAggItem(item, source.TypeInt64)
	if c.Name != "count_" {
		t.Errorf("name = %q, want count_", c.Name)
	}
}

func TestCompileAggItem_Sum_Int64(t *testing.T) {
	item := ast.AggItem{Call: ast.AggCall{Func: ast.AggSum, Field: field("v")}}
	c := CompileAggItem(item, source.TypeInt64)
	if c.Type != source.TypeInt64 {
		t.Errorf("type = %v, want TypeInt64", c.Type)
	}
	if c.Name != "sum_v" {
		t.Errorf("name = %q, want sum_v", c.Name)
	}
	if _, ok := c.NewAcc().(*sumInt64Acc); !ok {
		t.Error("NewAcc should return *sumInt64Acc")
	}
}

func TestCompileAggItem_Sum_Float64(t *testing.T) {
	item := ast.AggItem{Call: ast.AggCall{Func: ast.AggSum, Field: field("v")}}
	c := CompileAggItem(item, source.TypeFloat64)
	if c.Type != source.TypeFloat64 {
		t.Errorf("type = %v, want TypeFloat64", c.Type)
	}
	if _, ok := c.NewAcc().(*sumFloat64Acc); !ok {
		t.Error("NewAcc should return *sumFloat64Acc")
	}
}

func TestCompileAggItem_Avg(t *testing.T) {
	item := ast.AggItem{Call: ast.AggCall{Func: ast.AggAvg, Field: field("ms")}}
	c := CompileAggItem(item, source.TypeFloat64)
	if c.Type != source.TypeFloat64 {
		t.Errorf("type = %v, want TypeFloat64", c.Type)
	}
	if _, ok := c.NewAcc().(*avgAcc); !ok {
		t.Error("NewAcc should return *avgAcc")
	}
}

func TestCompileAggItem_Min(t *testing.T) {
	item := ast.AggItem{Alias: "lo", Call: ast.AggCall{Func: ast.AggMin, Field: field("v")}}
	c := CompileAggItem(item, source.TypeInt32)
	if c.Name != "lo" {
		t.Errorf("name = %q, want lo", c.Name)
	}
	if c.Type != source.TypeInt32 {
		t.Errorf("type = %v, want TypeInt32", c.Type)
	}
	if _, ok := c.NewAcc().(*minAcc); !ok {
		t.Error("NewAcc should return *minAcc")
	}
}

func TestCompileAggItem_Max(t *testing.T) {
	item := ast.AggItem{Call: ast.AggCall{Func: ast.AggMax, Field: field("v")}}
	c := CompileAggItem(item, source.TypeFloat64)
	if _, ok := c.NewAcc().(*maxAcc); !ok {
		t.Error("NewAcc should return *maxAcc")
	}
}

func TestCompileAggItem_Dcount(t *testing.T) {
	item := ast.AggItem{Call: ast.AggCall{Func: ast.AggDcount, Field: field("region")}}
	c := CompileAggItem(item, source.TypeInt64)
	if c.Type != source.TypeInt64 {
		t.Errorf("type = %v, want TypeInt64", c.Type)
	}
	if c.Name != "dcount_region" {
		t.Errorf("name = %q, want dcount_region", c.Name)
	}
	if _, ok := c.NewAcc().(*dcountAcc); !ok {
		t.Error("NewAcc should return *dcountAcc")
	}
}
