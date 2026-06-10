package expr

import (
	"testing"
	"time"

	"github.com/kozwoj/gobbler-query/query/ast"
	"github.com/kozwoj/gobbler-query/query/source"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

func ingest(acc AggAccumulator, vals ...any) {
	for _, v := range vals {
		acc.Ingest(v, v == nil)
	}
}

// ─── countAcc ────────────────────────────────────────────────────────────────

func TestCountAcc_CountsRows(t *testing.T) {
	a := &countAcc{}
	ingest(a, int32(1), nil, int32(3))
	got, null := a.Result()
	if null {
		t.Fatal("count result should not be null")
	}
	if got.(int64) != 3 {
		t.Errorf("count = %v, want 3", got)
	}
}

func TestCountAcc_Empty(t *testing.T) {
	a := &countAcc{}
	got, null := a.Result()
	if null {
		t.Fatal("empty count should not be null")
	}
	if got.(int64) != 0 {
		t.Errorf("empty count = %v, want 0", got)
	}
}

// ─── sumInt64Acc ─────────────────────────────────────────────────────────────

func TestSumInt64Acc_Int64Values(t *testing.T) {
	a := &sumInt64Acc{}
	ingest(a, int64(10), int64(20), nil, int64(5))
	got, null := a.Result()
	if null {
		t.Fatal("sum result should not be null")
	}
	if got.(int64) != 35 {
		t.Errorf("sum = %v, want 35", got)
	}
}

func TestSumInt64Acc_Int32Values(t *testing.T) {
	// int32 inputs are widened to int64 by the accumulator
	a := &sumInt64Acc{}
	ingest(a, int32(3), int32(4))
	got, null := a.Result()
	if null {
		t.Fatal("sum result should not be null")
	}
	if got.(int64) != 7 {
		t.Errorf("sum = %v, want 7", got)
	}
}

func TestSumInt64Acc_AllNull(t *testing.T) {
	a := &sumInt64Acc{}
	ingest(a, nil, nil)
	got, null := a.Result()
	if null {
		t.Fatal("sum result should not be null even when all inputs are null")
	}
	if got.(int64) != 0 {
		t.Errorf("all-null sum = %v, want 0", got)
	}
}

// ─── sumFloat64Acc ───────────────────────────────────────────────────────────

func TestSumFloat64Acc_Basic(t *testing.T) {
	a := &sumFloat64Acc{}
	ingest(a, 1.5, nil, 2.5)
	got, null := a.Result()
	if null {
		t.Fatal("sum result should not be null")
	}
	if got.(float64) != 4.0 {
		t.Errorf("sum = %v, want 4.0", got)
	}
}

// ─── avgAcc ──────────────────────────────────────────────────────────────────

func TestAvgAcc_Float64(t *testing.T) {
	a := &avgAcc{}
	ingest(a, 10.0, 20.0, 30.0)
	got, null := a.Result()
	if null {
		t.Fatal("avg result should not be null")
	}
	if got.(float64) != 20.0 {
		t.Errorf("avg = %v, want 20.0", got)
	}
}

func TestAvgAcc_Int32(t *testing.T) {
	a := &avgAcc{}
	ingest(a, int32(1), int32(3))
	got, null := a.Result()
	if null {
		t.Fatal("avg result should not be null")
	}
	if got.(float64) != 2.0 {
		t.Errorf("avg = %v, want 2.0", got)
	}
}

func TestAvgAcc_SkipsNulls(t *testing.T) {
	a := &avgAcc{}
	ingest(a, nil, 10.0, nil, 20.0)
	got, _ := a.Result()
	if got.(float64) != 15.0 {
		t.Errorf("avg skipping nulls = %v, want 15.0", got)
	}
}

func TestAvgAcc_AllNull_ReturnsNull(t *testing.T) {
	a := &avgAcc{}
	ingest(a, nil, nil)
	_, null := a.Result()
	if !null {
		t.Error("all-null avg should return null")
	}
}

// ─── cmpVal ──────────────────────────────────────────────────────────────────

func TestCmpVal_Int32(t *testing.T) {
	if cmpVal(int32(1), int32(2)) != -1 {
		t.Error("1 < 2")
	}
	if cmpVal(int32(2), int32(2)) != 0 {
		t.Error("2 == 2")
	}
	if cmpVal(int32(3), int32(2)) != 1 {
		t.Error("3 > 2")
	}
}

func TestCmpVal_Float64(t *testing.T) {
	if cmpVal(1.0, 2.0) != -1 {
		t.Error("1.0 < 2.0")
	}
}

func TestCmpVal_Datetime(t *testing.T) {
	earlier := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	later := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
	if cmpVal(earlier, later) != -1 {
		t.Error("earlier < later")
	}
	if cmpVal(later, later) != 0 {
		t.Error("equal datetimes")
	}
}

func TestCmpVal_Timespan(t *testing.T) {
	if cmpVal(time.Second, 2*time.Second) != -1 {
		t.Error("1s < 2s")
	}
}

// ─── minAcc ──────────────────────────────────────────────────────────────────

func TestMinAcc_Int32(t *testing.T) {
	a := &minAcc{}
	ingest(a, int32(5), int32(2), int32(8))
	got, null := a.Result()
	if null {
		t.Fatal("min should not be null")
	}
	if got.(int32) != 2 {
		t.Errorf("min = %v, want 2", got)
	}
}

func TestMinAcc_SkipsNulls(t *testing.T) {
	a := &minAcc{}
	ingest(a, nil, int32(3), nil)
	got, null := a.Result()
	if null {
		t.Fatal("min should not be null")
	}
	if got.(int32) != 3 {
		t.Errorf("min = %v, want 3", got)
	}
}

func TestMinAcc_AllNull_ReturnsNull(t *testing.T) {
	a := &minAcc{}
	ingest(a, nil, nil)
	_, null := a.Result()
	if !null {
		t.Error("all-null min should return null")
	}
}

func TestMinAcc_Datetime(t *testing.T) {
	t1 := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC)
	a := &minAcc{}
	ingest(a, t1, t2)
	got, _ := a.Result()
	if !got.(time.Time).Equal(t2) {
		t.Errorf("min datetime = %v, want %v", got, t2)
	}
}

// ─── maxAcc ──────────────────────────────────────────────────────────────────

func TestMaxAcc_Int64(t *testing.T) {
	a := &maxAcc{}
	ingest(a, int64(1), int64(9), int64(4))
	got, null := a.Result()
	if null {
		t.Fatal("max should not be null")
	}
	if got.(int64) != 9 {
		t.Errorf("max = %v, want 9", got)
	}
}

func TestMaxAcc_AllNull_ReturnsNull(t *testing.T) {
	a := &maxAcc{}
	ingest(a, nil)
	_, null := a.Result()
	if !null {
		t.Error("all-null max should return null")
	}
}

// ─── dcountAcc ───────────────────────────────────────────────────────────────

func TestDcountAcc_CountsDistinct(t *testing.T) {
	a := &dcountAcc{}
	ingest(a, "east", "west", "east", "north")
	got, null := a.Result()
	if null {
		t.Fatal("dcount should not be null")
	}
	if got.(int64) != 3 {
		t.Errorf("dcount = %v, want 3", got)
	}
}

func TestDcountAcc_SkipsNulls(t *testing.T) {
	a := &dcountAcc{}
	ingest(a, nil, "east", nil)
	got, _ := a.Result()
	if got.(int64) != 1 {
		t.Errorf("dcount skipping nulls = %v, want 1", got)
	}
}

func TestDcountAcc_Empty(t *testing.T) {
	a := &dcountAcc{}
	got, null := a.Result()
	if null {
		t.Fatal("empty dcount should not be null")
	}
	if got.(int64) != 0 {
		t.Errorf("empty dcount = %v, want 0", got)
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
