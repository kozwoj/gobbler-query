package physical

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/kozwoj/gobbler-query/query/batch"
	"github.com/kozwoj/gobbler-query/query/expr"
	"github.com/kozwoj/gobbler-query/query/source"
)

// VecKind records which concrete ColumnVector type a column uses.
// It is captured from the first batch and used to build correctly-typed
// all-null vectors in buildBatchFromRows.
type VecKind int

const (
	vecInt32 VecKind = iota
	vecInt64
	vecFloat64
	vecString
	vecBool
	vecDatetime
	vecTimespan
	vecDynamic
)

func vecKindOf(cv batch.ColumnVector) (VecKind, error) {
	switch cv.(type) {
	case *batch.Int32Vector:
		return vecInt32, nil
	case *batch.Int64Vector:
		return vecInt64, nil
	case *batch.Float64Vector:
		return vecFloat64, nil
	case *batch.StringVector:
		return vecString, nil
	case *batch.BoolVector:
		return vecBool, nil
	case *batch.DatetimeVector:
		return vecDatetime, nil
	case *batch.TimespanVector:
		return vecTimespan, nil
	case *batch.DynamicVector:
		return vecDynamic, nil
	default:
		return 0, fmt.Errorf("rows: unsupported column type %T", cv)
	}
}

// VecKindFromColumnType converts a source.ColumnType to the matching VecKind.
// Used so CompiledProjectItem.Type can seed buildBatchFromRows when no input
// batch has been seen (all-null column from the very first row).
func VecKindFromColumnType(ct source.ColumnType) VecKind {
	switch ct {
	case source.TypeInt32:
		return vecInt32
	case source.TypeInt64:
		return vecInt64
	case source.TypeFloat64:
		return vecFloat64
	case source.TypeString:
		return vecString
	case source.TypeBool:
		return vecBool
	case source.TypeDatetime:
		return vecDatetime
	case source.TypeTimespan:
		return vecTimespan
	case source.TypeDynamic:
		return vecDynamic
	default:
		return vecString // safe fallback; should not be reached after InferAndValidate
	}
}

// encodedRow stores one row as contiguous bytes with per-cell offsets and a null bitmap.
// Null cells occupy 0 bytes; offsets[i] == offsets[i+1] and bit i is set in nulls.
type encodedRow struct {
	data    []byte   // cell bytes packed sequentially
	offsets []uint32 // len = ncols+1; cell i occupies data[offsets[i]:offsets[i+1]]
	nulls   []uint64 // packed bitset: bit i set → col i is null
}

// rowStore is the shared accumulation store for blocking operators (SortOp,
// HashAggregateOp, HashJoinOp). It replaces the previous materializedRows
// which used [][]any and [][]bool per row.
type rowStore struct {
	schema []batch.ColumnMeta
	kinds  []VecKind
	rows   []encodedRow
}

// rowCount returns the number of accumulated rows.
func (rs *rowStore) rowCount() int { return len(rs.rows) }

// appendBatch extracts every row from b and appends it to rs.
// schema and kinds are captured from the first non-empty batch.
func (rs *rowStore) appendBatch(b *batch.Batch) error {
	if b.Length == 0 {
		return nil
	}
	if rs.schema == nil {
		rs.schema = b.Schema
		rs.kinds = make([]VecKind, len(b.Columns))
		for i, cv := range b.Columns {
			k, err := vecKindOf(cv)
			if err != nil {
				return err
			}
			rs.kinds[i] = k
		}
	} else if len(b.Columns) != len(rs.schema) {
		return fmt.Errorf("rows: schema mismatch: expected %d columns, got %d", len(rs.schema), len(b.Columns))
	}
	ncols := len(b.Columns)
	for row := 0; row < b.Length; row++ {
		offsets := make([]uint32, ncols+1)
		var data []byte
		var nullBits []uint64
		for col, cv := range b.Columns {
			offsets[col] = uint32(len(data))
			if cv.IsNull(row) {
				nullBits = setNullBit(nullBits, col, ncols)
			} else {
				data = encodeBatchCell(data, cv, row, rs.kinds[col])
			}
		}
		offsets[ncols] = uint32(len(data))
		rs.rows = append(rs.rows, encodedRow{data: data, offsets: offsets, nulls: nullBits})
	}
	return nil
}

// appendValues encodes a []expr.Value slice as a new row.
// rs.kinds must be populated before calling this method.
func (rs *rowStore) appendValues(vals []expr.Value) {
	ncols := len(vals)
	offsets := make([]uint32, ncols+1)
	var data []byte
	var nullBits []uint64
	for i, v := range vals {
		offsets[i] = uint32(len(data))
		if v.Kind == expr.KindNull {
			nullBits = setNullBit(nullBits, i, ncols)
		} else {
			data = encodeValue(data, v)
		}
	}
	offsets[ncols] = uint32(len(data))
	rs.rows = append(rs.rows, encodedRow{data: data, offsets: offsets, nulls: nullBits})
}

// decodeCell decodes the value at (rowIdx, colIdx) to an expr.Value.
func (rs *rowStore) decodeCell(rowIdx, colIdx int) expr.Value {
	row := rs.rows[rowIdx]
	if isNullBit(row.nulls, colIdx) {
		return expr.Value{Kind: expr.KindNull}
	}
	return decodeBytes(row.data[row.offsets[colIdx]:row.offsets[colIdx+1]], rs.kinds[colIdx])
}

// rowValues decodes all cells of row rowIdx to a []expr.Value.
func (rs *rowStore) rowValues(rowIdx int) []expr.Value {
	ncols := len(rs.schema)
	vals := make([]expr.Value, ncols)
	for col := 0; col < ncols; col++ {
		vals[col] = rs.decodeCell(rowIdx, col)
	}
	return vals
}

// keyValues decodes the columns at colIdxs for row rowIdx.
func (rs *rowStore) keyValues(rowIdx int, colIdxs []int) []expr.Value {
	vals := make([]expr.Value, len(colIdxs))
	for i, col := range colIdxs {
		vals[i] = rs.decodeCell(rowIdx, col)
	}
	return vals
}

// compare returns true if row i should sort before row j according to keys.
// Nulls always sort last, regardless of direction.
func (rs *rowStore) compare(i, j int, keys []CompiledSortKey) bool {
	for _, k := range keys {
		vi := rs.decodeCell(i, k.ColIdx)
		vj := rs.decodeCell(j, k.ColIdx)
		iNull := vi.Kind == expr.KindNull
		jNull := vj.Kind == expr.KindNull
		switch {
		case iNull && jNull:
			continue
		case iNull:
			return false // null sorts after non-null
		case jNull:
			return true // non-null sorts before null
		}
		cmp := expr.CmpValue(vi, vj)
		if cmp == 0 {
			continue
		}
		if k.Desc {
			cmp = -cmp
		}
		return cmp < 0
	}
	return false
}

// buildBatchFromRows assembles a *batch.Batch from rows in [start, end).
func (rs *rowStore) buildBatchFromRows(start, end int) *batch.Batch {
	n := end - start
	ncols := len(rs.schema)
	cols := make([]batch.ColumnVector, ncols)
	for col := 0; col < ncols; col++ {
		vals := make([]expr.Value, n)
		for i := 0; i < n; i++ {
			vals[i] = rs.decodeCell(start+i, col)
		}
		cols[col] = buildVectorFromValues(vals, rs.kinds[col])
	}
	return &batch.Batch{
		Length:  n,
		Schema:  rs.schema,
		Columns: cols,
	}
}

// ─── null bitmap helpers ──────────────────────────────────────────────────────

func setNullBit(nulls []uint64, col, ncols int) []uint64 {
	if len(nulls) == 0 {
		nulls = make([]uint64, (ncols+63)/64)
	}
	nulls[col/64] |= 1 << uint(col%64)
	return nulls
}

func isNullBit(nulls []uint64, col int) bool {
	if len(nulls) == 0 {
		return false
	}
	return nulls[col/64]>>(uint(col)%64)&1 == 1
}

// ─── cell encoding / decoding ─────────────────────────────────────────────────

func encodeValue(buf []byte, v expr.Value) []byte {
	switch v.Kind {
	case expr.KindInt32:
		return binary.LittleEndian.AppendUint32(buf, uint32(int32(v.I)))
	case expr.KindInt64, expr.KindDatetime, expr.KindTimespan:
		return binary.LittleEndian.AppendUint64(buf, uint64(v.I))
	case expr.KindFloat64:
		return binary.LittleEndian.AppendUint64(buf, math.Float64bits(v.F))
	case expr.KindBool:
		b := byte(0)
		if v.I != 0 {
			b = 1
		}
		return append(buf, b)
	case expr.KindString, expr.KindDynamic:
		return append(buf, v.S...)
	}
	return buf
}

func encodeBatchCell(buf []byte, cv batch.ColumnVector, row int, kind VecKind) []byte {
	switch kind {
	case vecInt32:
		return binary.LittleEndian.AppendUint32(buf, uint32(cv.(*batch.Int32Vector).Values[row]))
	case vecInt64:
		return binary.LittleEndian.AppendUint64(buf, uint64(cv.(*batch.Int64Vector).Values[row]))
	case vecFloat64:
		return binary.LittleEndian.AppendUint64(buf, math.Float64bits(cv.(*batch.Float64Vector).Values[row]))
	case vecBool:
		b := byte(0)
		if cv.(*batch.BoolVector).Values[row] {
			b = 1
		}
		return append(buf, b)
	case vecString:
		return append(buf, cv.(*batch.StringVector).Values[row]...)
	case vecDynamic:
		return append(buf, cv.(*batch.DynamicVector).Values[row]...)
	case vecDatetime:
		return binary.LittleEndian.AppendUint64(buf, uint64(cv.(*batch.DatetimeVector).Values[row].UnixNano()))
	case vecTimespan:
		return binary.LittleEndian.AppendUint64(buf, uint64(int64(cv.(*batch.TimespanVector).Values[row])))
	}
	return buf
}

func decodeBytes(data []byte, kind VecKind) expr.Value {
	switch kind {
	case vecInt32:
		return expr.Value{Kind: expr.KindInt32, I: int64(int32(binary.LittleEndian.Uint32(data)))}
	case vecInt64:
		return expr.Value{Kind: expr.KindInt64, I: int64(binary.LittleEndian.Uint64(data))}
	case vecFloat64:
		return expr.Value{Kind: expr.KindFloat64, F: math.Float64frombits(binary.LittleEndian.Uint64(data))}
	case vecBool:
		return expr.Value{Kind: expr.KindBool, I: int64(data[0])}
	case vecString:
		return expr.Value{Kind: expr.KindString, S: string(data)}
	case vecDynamic:
		return expr.Value{Kind: expr.KindDynamic, S: string(data)}
	case vecDatetime:
		return expr.Value{Kind: expr.KindDatetime, I: int64(binary.LittleEndian.Uint64(data))}
	case vecTimespan:
		return expr.Value{Kind: expr.KindTimespan, I: int64(binary.LittleEndian.Uint64(data))}
	}
	return expr.Value{Kind: expr.KindNull}
}

// ─── batch row helpers ────────────────────────────────────────────────────────

// batchCellValue decodes one cell from a batch column to an expr.Value.
func batchCellValue(cv batch.ColumnVector, row int) expr.Value {
	if cv.IsNull(row) {
		return expr.Value{Kind: expr.KindNull}
	}
	switch v := cv.(type) {
	case *batch.Int32Vector:
		return expr.Value{Kind: expr.KindInt32, I: int64(v.Values[row])}
	case *batch.Int64Vector:
		return expr.Value{Kind: expr.KindInt64, I: v.Values[row]}
	case *batch.Float64Vector:
		return expr.Value{Kind: expr.KindFloat64, F: v.Values[row]}
	case *batch.StringVector:
		return expr.Value{Kind: expr.KindString, S: v.Values[row]}
	case *batch.BoolVector:
		i := int64(0)
		if v.Values[row] {
			i = 1
		}
		return expr.Value{Kind: expr.KindBool, I: i}
	case *batch.DatetimeVector:
		return expr.Value{Kind: expr.KindDatetime, I: v.Values[row].UnixNano()}
	case *batch.TimespanVector:
		return expr.Value{Kind: expr.KindTimespan, I: int64(v.Values[row])}
	case *batch.DynamicVector:
		return expr.Value{Kind: expr.KindDynamic, S: v.Values[row]}
	}
	return expr.Value{Kind: expr.KindNull}
}

// batchRowValues decodes all cells of a single row in b to []expr.Value.
func batchRowValues(b *batch.Batch, row int) []expr.Value {
	vals := make([]expr.Value, len(b.Columns))
	for col, cv := range b.Columns {
		vals[col] = batchCellValue(cv, row)
	}
	return vals
}

// batchKeyValues decodes the specified key columns of a single row in b.
func batchKeyValues(b *batch.Batch, row int, colIdxs []int) []expr.Value {
	vals := make([]expr.Value, len(colIdxs))
	for i, idx := range colIdxs {
		vals[i] = batchCellValue(b.Columns[idx], row)
	}
	return vals
}
