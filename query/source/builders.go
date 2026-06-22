package source

import (
	"strconv"
	"time"

	"github.com/kozwoj/gobbler-query/query/batch"
)

const datetimeFormat = "2006-01-02 15:04:05.000"

// columnBuilder is an unexported interface for per-column scratch buffers.
// Builders are allocated once at construction (sized batchSize) and reused
// across every batch — no allocations occur inside the inner read loop.
type columnBuilder interface {
	Append(raw string)
	// AppendBytes is the []byte variant used by the bulk-read path. cell is a
	// slice into the file buffer; for non-string types the compiler can elide
	// the string(cell) conversion because it does not escape the call.
	AppendBytes(cell []byte)
	FinalizeColumn(n int) batch.ColumnVector
	Reset()
}

// newColumnBuilders allocates one builder per schema column.
func newColumnBuilders(schema *Schema, batchSize int) []columnBuilder {
	builders := make([]columnBuilder, len(schema.Columns))
	for i, col := range schema.Columns {
		switch col.Type {
		case TypeInt32:
			builders[i] = newInt32Builder(batchSize)
		case TypeFloat64:
			builders[i] = newFloat64Builder(batchSize)
		case TypeString:
			builders[i] = newStringBuilder(batchSize)
		case TypeBool:
			builders[i] = newBoolBuilder(batchSize)
		case TypeDatetime:
			builders[i] = newDatetimeBuilder(batchSize)
		case TypeTimespan:
			builders[i] = newTimespanBuilder(batchSize)
		case TypeDynamic:
			builders[i] = newDynamicBuilder(batchSize)
		}
	}
	return builders
}

// nullWords returns the number of uint64 words needed for n rows.
func nullWords(n int) int { return (n + 63) / 64 }

// setNull marks position idx as null in the packed bitset.
func setNull(nulls []uint64, idx int) { nulls[idx/64] |= 1 << (uint(idx) % 64) }

// copyNulls returns a copy of the first nullWords(n) words from nulls.
func copyNulls(nulls []uint64, n int) []uint64 {
	nw := nullWords(n)
	out := make([]uint64, nw)
	copy(out, nulls[:nw])
	return out
}

// resetNulls zeroes the entire nulls slice.
func resetNulls(nulls []uint64) {
	for i := range nulls {
		nulls[i] = 0
	}
}

// --- int32Builder ---

type int32Builder struct {
	values []int32
	nulls  []uint64
	cur    int
}

func newInt32Builder(batchSize int) *int32Builder {
	return &int32Builder{
		values: make([]int32, batchSize),
		nulls:  make([]uint64, nullWords(batchSize)),
	}
}

func (b *int32Builder) Append(raw string) {
	if raw == "" {
		setNull(b.nulls, b.cur)
	} else if v, err := strconv.ParseInt(raw, 10, 32); err != nil {
		setNull(b.nulls, b.cur)
	} else {
		b.values[b.cur] = int32(v)
	}
	b.cur++
}

func (b *int32Builder) AppendBytes(cell []byte) {
	if len(cell) == 0 {
		setNull(b.nulls, b.cur)
	} else if v, err := strconv.ParseInt(string(cell), 10, 32); err != nil {
		setNull(b.nulls, b.cur)
	} else {
		b.values[b.cur] = int32(v)
	}
	b.cur++
}

func (b *int32Builder) FinalizeColumn(n int) batch.ColumnVector {
	vals := make([]int32, n)
	copy(vals, b.values[:n])
	return &batch.Int32Vector{Values: vals, Nulls: copyNulls(b.nulls, n)}
}

func (b *int32Builder) Reset() { b.cur = 0; resetNulls(b.nulls) }

// --- float64Builder ---

type float64Builder struct {
	values []float64
	nulls  []uint64
	cur    int
}

func newFloat64Builder(batchSize int) *float64Builder {
	return &float64Builder{
		values: make([]float64, batchSize),
		nulls:  make([]uint64, nullWords(batchSize)),
	}
}

func (b *float64Builder) Append(raw string) {
	if raw == "" {
		setNull(b.nulls, b.cur)
	} else if v, err := strconv.ParseFloat(raw, 64); err != nil {
		setNull(b.nulls, b.cur)
	} else {
		b.values[b.cur] = v
	}
	b.cur++
}

func (b *float64Builder) AppendBytes(cell []byte) {
	if len(cell) == 0 {
		setNull(b.nulls, b.cur)
	} else if v, err := strconv.ParseFloat(string(cell), 64); err != nil {
		setNull(b.nulls, b.cur)
	} else {
		b.values[b.cur] = v
	}
	b.cur++
}

func (b *float64Builder) FinalizeColumn(n int) batch.ColumnVector {
	vals := make([]float64, n)
	copy(vals, b.values[:n])
	return &batch.Float64Vector{Values: vals, Nulls: copyNulls(b.nulls, n)}
}

func (b *float64Builder) Reset() { b.cur = 0; resetNulls(b.nulls) }

// --- stringBuilder ---

type stringBuilder struct {
	values []string
	nulls  []uint64
	cur    int
}

func newStringBuilder(batchSize int) *stringBuilder {
	return &stringBuilder{
		values: make([]string, batchSize),
		nulls:  make([]uint64, nullWords(batchSize)),
	}
}

func (b *stringBuilder) Append(raw string) {
	if raw == "" {
		setNull(b.nulls, b.cur)
	} else {
		b.values[b.cur] = raw
	}
	b.cur++
}

func (b *stringBuilder) AppendBytes(cell []byte) {
	if len(cell) == 0 {
		setNull(b.nulls, b.cur)
	} else {
		b.values[b.cur] = string(cell)
	}
	b.cur++
}

func (b *stringBuilder) FinalizeColumn(n int) batch.ColumnVector {
	vals := make([]string, n)
	copy(vals, b.values[:n])
	return &batch.StringVector{Values: vals, Nulls: copyNulls(b.nulls, n)}
}

func (b *stringBuilder) Reset() { b.cur = 0; resetNulls(b.nulls) }

// --- boolBuilder ---

type boolBuilder struct {
	values []bool
	nulls  []uint64
	cur    int
}

func newBoolBuilder(batchSize int) *boolBuilder {
	return &boolBuilder{
		values: make([]bool, batchSize),
		nulls:  make([]uint64, nullWords(batchSize)),
	}
}

func (b *boolBuilder) Append(raw string) {
	if raw == "" {
		setNull(b.nulls, b.cur)
	} else if v, err := strconv.ParseBool(raw); err != nil {
		setNull(b.nulls, b.cur)
	} else {
		b.values[b.cur] = v
	}
	b.cur++
}

func (b *boolBuilder) AppendBytes(cell []byte) {
	if len(cell) == 0 {
		setNull(b.nulls, b.cur)
	} else if v, err := strconv.ParseBool(string(cell)); err != nil {
		setNull(b.nulls, b.cur)
	} else {
		b.values[b.cur] = v
	}
	b.cur++
}

func (b *boolBuilder) FinalizeColumn(n int) batch.ColumnVector {
	vals := make([]bool, n)
	copy(vals, b.values[:n])
	return &batch.BoolVector{Values: vals, Nulls: copyNulls(b.nulls, n)}
}

func (b *boolBuilder) Reset() { b.cur = 0; resetNulls(b.nulls) }

// --- datetimeBuilder ---

type datetimeBuilder struct {
	values []time.Time
	nulls  []uint64
	cur    int
}

func newDatetimeBuilder(batchSize int) *datetimeBuilder {
	return &datetimeBuilder{
		values: make([]time.Time, batchSize),
		nulls:  make([]uint64, nullWords(batchSize)),
	}
}

func (b *datetimeBuilder) Append(raw string) {
	if raw == "" {
		setNull(b.nulls, b.cur)
	} else if v, err := time.Parse(datetimeFormat, raw); err != nil {
		setNull(b.nulls, b.cur)
	} else {
		b.values[b.cur] = v
	}
	b.cur++
}

func (b *datetimeBuilder) AppendBytes(cell []byte) {
	if len(cell) == 0 {
		setNull(b.nulls, b.cur)
	} else if v, err := time.Parse(datetimeFormat, string(cell)); err != nil {
		setNull(b.nulls, b.cur)
	} else {
		b.values[b.cur] = v
	}
	b.cur++
}

func (b *datetimeBuilder) FinalizeColumn(n int) batch.ColumnVector {
	vals := make([]time.Time, n)
	copy(vals, b.values[:n])
	return &batch.DatetimeVector{Values: vals, Nulls: copyNulls(b.nulls, n)}
}

func (b *datetimeBuilder) Reset() { b.cur = 0; resetNulls(b.nulls) }

// --- timespanBuilder ---

type timespanBuilder struct {
	values []time.Duration
	nulls  []uint64
	cur    int
}

func newTimespanBuilder(batchSize int) *timespanBuilder {
	return &timespanBuilder{
		values: make([]time.Duration, batchSize),
		nulls:  make([]uint64, nullWords(batchSize)),
	}
}

func (b *timespanBuilder) Append(raw string) {
	if raw == "" {
		setNull(b.nulls, b.cur)
	} else if v, err := time.ParseDuration(raw); err != nil {
		setNull(b.nulls, b.cur)
	} else {
		b.values[b.cur] = v
	}
	b.cur++
}

func (b *timespanBuilder) AppendBytes(cell []byte) {
	if len(cell) == 0 {
		setNull(b.nulls, b.cur)
	} else if v, err := time.ParseDuration(string(cell)); err != nil {
		setNull(b.nulls, b.cur)
	} else {
		b.values[b.cur] = v
	}
	b.cur++
}

func (b *timespanBuilder) FinalizeColumn(n int) batch.ColumnVector {
	vals := make([]time.Duration, n)
	copy(vals, b.values[:n])
	return &batch.TimespanVector{Values: vals, Nulls: copyNulls(b.nulls, n)}
}

func (b *timespanBuilder) Reset() { b.cur = 0; resetNulls(b.nulls) }

// --- dynamicBuilder ---
// Identical to stringBuilder. Gobbler writes dynamic fields as CSV-quoted JSON;
// Go's csv.Reader unquotes them before Append is called, so the stored value is
// already a plain JSON string.

type dynamicBuilder struct {
	values []string
	nulls  []uint64
	cur    int
}

func newDynamicBuilder(batchSize int) *dynamicBuilder {
	return &dynamicBuilder{
		values: make([]string, batchSize),
		nulls:  make([]uint64, nullWords(batchSize)),
	}
}

func (b *dynamicBuilder) Append(raw string) {
	if raw == "" {
		setNull(b.nulls, b.cur)
	} else {
		b.values[b.cur] = raw
	}
	b.cur++
}

func (b *dynamicBuilder) AppendBytes(cell []byte) {
	if len(cell) == 0 {
		setNull(b.nulls, b.cur)
	} else {
		b.values[b.cur] = string(cell)
	}
	b.cur++
}

func (b *dynamicBuilder) FinalizeColumn(n int) batch.ColumnVector {
	vals := make([]string, n)
	copy(vals, b.values[:n])
	return &batch.DynamicVector{Values: vals, Nulls: copyNulls(b.nulls, n)}
}

func (b *dynamicBuilder) Reset() { b.cur = 0; resetNulls(b.nulls) }
