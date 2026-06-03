package source

import (
	"testing"
	"time"

	"github.com/kozwoj/gobbler-query/query/batch"
)

// helper: append n values to a builder, finalize, check length and nulls.

func TestInt32Builder(t *testing.T) {
	b := newInt32Builder(8)

	b.Append("42")
	b.Append("")             // null
	b.Append("not a number") // malformed → null
	b.Append("-7")

	vec := b.FinalizeColumn(4).(*batch.Int32Vector)

	if vec.Len() != 4 {
		t.Fatalf("len = %d, want 4", vec.Len())
	}
	if vec.Values[0] != 42 {
		t.Errorf("row 0: got %d, want 42", vec.Values[0])
	}
	if !vec.IsNull(1) {
		t.Error("row 1 (empty): expected null")
	}
	if !vec.IsNull(2) {
		t.Error("row 2 (malformed): expected null")
	}
	if vec.Values[3] != -7 {
		t.Errorf("row 3: got %d, want -7", vec.Values[3])
	}
	if vec.IsNull(0) || vec.IsNull(3) {
		t.Error("rows 0 and 3 should not be null")
	}

	// Reset and reuse
	b.Reset()
	b.Append("100")
	vec2 := b.FinalizeColumn(1).(*batch.Int32Vector)
	if vec2.Values[0] != 100 {
		t.Errorf("after reset: got %d, want 100", vec2.Values[0])
	}
	if vec2.IsNull(0) {
		t.Error("after reset: row 0 should not be null")
	}
}

func TestFloat64Builder(t *testing.T) {
	b := newFloat64Builder(4)

	b.Append("3.14")
	b.Append("")
	b.Append("bad")
	b.Append("-2.5")

	vec := b.FinalizeColumn(4).(*batch.Float64Vector)

	if vec.Values[0] != 3.14 {
		t.Errorf("row 0: got %v, want 3.14", vec.Values[0])
	}
	if !vec.IsNull(1) {
		t.Error("row 1: expected null")
	}
	if !vec.IsNull(2) {
		t.Error("row 2: expected null")
	}
	if vec.Values[3] != -2.5 {
		t.Errorf("row 3: got %v, want -2.5", vec.Values[3])
	}
}

func TestStringBuilder(t *testing.T) {
	b := newStringBuilder(4)

	b.Append("hello")
	b.Append("")
	b.Append("world")

	vec := b.FinalizeColumn(3).(*batch.StringVector)

	if vec.Values[0] != "hello" {
		t.Errorf("row 0: got %q", vec.Values[0])
	}
	if !vec.IsNull(1) {
		t.Error("row 1: expected null")
	}
	if vec.Values[2] != "world" {
		t.Errorf("row 2: got %q", vec.Values[2])
	}
}

func TestBoolBuilder(t *testing.T) {
	b := newBoolBuilder(4)

	b.Append("true")
	b.Append("false")
	b.Append("")
	b.Append("notabool")

	vec := b.FinalizeColumn(4).(*batch.BoolVector)

	if !vec.Values[0] {
		t.Error("row 0: want true")
	}
	if vec.Values[1] {
		t.Error("row 1: want false")
	}
	if !vec.IsNull(2) {
		t.Error("row 2: expected null")
	}
	if !vec.IsNull(3) {
		t.Error("row 3: expected null")
	}
}

func TestDatetimeBuilder(t *testing.T) {
	b := newDatetimeBuilder(4)

	b.Append("2026-05-01 00:00:11.758")
	b.Append("")
	b.Append("not-a-date")

	vec := b.FinalizeColumn(3).(*batch.DatetimeVector)

	want := time.Date(2026, 5, 1, 0, 0, 11, 758_000_000, time.UTC)
	if !vec.Values[0].Equal(want) {
		t.Errorf("row 0: got %v, want %v", vec.Values[0], want)
	}
	if !vec.IsNull(1) {
		t.Error("row 1: expected null")
	}
	if !vec.IsNull(2) {
		t.Error("row 2: expected null")
	}
}

func TestTimespanBuilder(t *testing.T) {
	b := newTimespanBuilder(4)

	b.Append("1h10m10s")
	b.Append("15m")
	b.Append("")
	b.Append("notaduration")

	vec := b.FinalizeColumn(4).(*batch.TimespanVector)

	if vec.Values[0] != 1*time.Hour+10*time.Minute+10*time.Second {
		t.Errorf("row 0: got %v", vec.Values[0])
	}
	if vec.Values[1] != 15*time.Minute {
		t.Errorf("row 1: got %v", vec.Values[1])
	}
	if !vec.IsNull(2) {
		t.Error("row 2: expected null")
	}
	if !vec.IsNull(3) {
		t.Error("row 3: expected null")
	}
}

func TestDynamicBuilder(t *testing.T) {
	b := newDynamicBuilder(4)

	b.Append(`{"key":"value"}`)
	b.Append("")
	b.Append(`[1,2,3]`)

	vec := b.FinalizeColumn(3).(*batch.DynamicVector)

	if vec.Values[0] != `{"key":"value"}` {
		t.Errorf("row 0: got %q", vec.Values[0])
	}
	if !vec.IsNull(1) {
		t.Error("row 1: expected null")
	}
	if vec.Values[2] != `[1,2,3]` {
		t.Errorf("row 2: got %q", vec.Values[2])
	}
}

func TestBuilderReset_NullBitsCleared(t *testing.T) {
	// Verify that null bits from batch N do not bleed into batch N+1.
	b := newInt32Builder(4)

	b.Append("") // null at position 0
	b.FinalizeColumn(1)
	b.Reset()

	b.Append("99") // position 0 again — must not be null
	vec := b.FinalizeColumn(1).(*batch.Int32Vector)
	if vec.IsNull(0) {
		t.Error("after Reset, row 0 should not be null")
	}
	if vec.Values[0] != 99 {
		t.Errorf("after Reset: got %d, want 99", vec.Values[0])
	}
}

func TestNewColumnBuilders(t *testing.T) {
	schema := &Schema{
		Columns: []ColumnSchema{
			{Name: "timestamp", Type: TypeDatetime},
			{Name: "count", Type: TypeInt32},
			{Name: "ratio", Type: TypeFloat64},
			{Name: "label", Type: TypeString},
			{Name: "active", Type: TypeBool},
			{Name: "ttl", Type: TypeTimespan},
			{Name: "meta", Type: TypeDynamic},
		},
	}
	builders := newColumnBuilders(schema, 16)
	if len(builders) != len(schema.Columns) {
		t.Fatalf("got %d builders, want %d", len(builders), len(schema.Columns))
	}
	// Each builder must be non-nil.
	for i, bld := range builders {
		if bld == nil {
			t.Errorf("builders[%d] is nil", i)
		}
	}
	// Spot-check types by appending and finalizing.
	builders[0].Append("2026-01-01 00:00:00.000")
	if _, ok := builders[0].FinalizeColumn(1).(*batch.DatetimeVector); !ok {
		t.Error("builders[0] should produce DatetimeVector")
	}
	builders[1].Reset()
	builders[1].Append("7")
	if _, ok := builders[1].FinalizeColumn(1).(*batch.Int32Vector); !ok {
		t.Error("builders[1] should produce Int32Vector")
	}
}
