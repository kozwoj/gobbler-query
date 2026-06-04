package batch

import "time"

// ColumnMeta carries the name and origin of a column.
// Origin is the source type name (e.g. "requests") for columns that come
// directly from a CSV source, and empty for computed or aggregate columns.
type ColumnMeta struct {
	Name   string
	Origin string
}

// Batch is the unit of data flowing through the pipeline.
// Every batch is dense — there is no selection vector in Phase 1.
type Batch struct {
	Length  int          // number of rows in this batch
	Schema  []ColumnMeta // parallel to Columns; one entry per column
	Columns []ColumnVector
}

// ColumnVector is implemented by each typed vector.
// Expression evaluators type-assert to the concrete type; Len and IsNull
// allow generic null-checking without a type assertion.
type ColumnVector interface {
	Len() int
	IsNull(i int) bool
	columnVector() // unexported marker; prevents external implementations
}

// Nulls is a packed bitset: bit i set means row i is null.
// Each concrete vector embeds this helper.
func isNull(nulls []uint64, i int) bool {
	if len(nulls) == 0 {
		return false
	}
	return nulls[i/64]>>(uint(i)%64)&1 == 1
}

// Int32Vector holds a column of int32 values.
type Int32Vector struct {
	Values []int32
	Nulls  []uint64
}

func (v *Int32Vector) Len() int          { return len(v.Values) }
func (v *Int32Vector) IsNull(i int) bool { return isNull(v.Nulls, i) }
func (v *Int32Vector) columnVector()     {}

// Int64Vector holds a column of int64 values.
type Int64Vector struct {
	Values []int64
	Nulls  []uint64
}

func (v *Int64Vector) Len() int          { return len(v.Values) }
func (v *Int64Vector) IsNull(i int) bool { return isNull(v.Nulls, i) }
func (v *Int64Vector) columnVector()     {}

// Float64Vector holds a column of float64 values.
type Float64Vector struct {
	Values []float64
	Nulls  []uint64
}

func (v *Float64Vector) Len() int          { return len(v.Values) }
func (v *Float64Vector) IsNull(i int) bool { return isNull(v.Nulls, i) }
func (v *Float64Vector) columnVector()     {}

// StringVector holds a column of string values.
type StringVector struct {
	Values []string
	Nulls  []uint64
}

func (v *StringVector) Len() int          { return len(v.Values) }
func (v *StringVector) IsNull(i int) bool { return isNull(v.Nulls, i) }
func (v *StringVector) columnVector()     {}

// BoolVector holds a column of bool values.
type BoolVector struct {
	Values []bool
	Nulls  []uint64
}

func (v *BoolVector) Len() int          { return len(v.Values) }
func (v *BoolVector) IsNull(i int) bool { return isNull(v.Nulls, i) }
func (v *BoolVector) columnVector()     {}

// DatetimeVector holds a column of time.Time values.
type DatetimeVector struct {
	Values []time.Time
	Nulls  []uint64
}

func (v *DatetimeVector) Len() int          { return len(v.Values) }
func (v *DatetimeVector) IsNull(i int) bool { return isNull(v.Nulls, i) }
func (v *DatetimeVector) columnVector()     {}

// TimespanVector holds a column of time.Duration values.
// Gobbler writes timespans as Go duration strings (e.g. "1h10m10s").
type TimespanVector struct {
	Values []time.Duration
	Nulls  []uint64
}

func (v *TimespanVector) Len() int          { return len(v.Values) }
func (v *TimespanVector) IsNull(i int) bool { return isNull(v.Nulls, i) }
func (v *TimespanVector) columnVector()     {}

// DynamicVector holds a column of opaque JSON strings.
// Gobbler writes dynamic fields as CSV-quoted JSON; Go's csv.Reader
// unquotes them before they reach the builder, so the stored value is a
// plain JSON string.
type DynamicVector struct {
	Values []string
	Nulls  []uint64
}

func (v *DynamicVector) Len() int          { return len(v.Values) }
func (v *DynamicVector) IsNull(i int) bool { return isNull(v.Nulls, i) }
func (v *DynamicVector) columnVector()     {}
