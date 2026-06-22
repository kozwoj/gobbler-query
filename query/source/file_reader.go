package source

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kozwoj/gobbler-query/query/batch"
)

// FileTableReader reads CSV files from a directory as a single ordered row
// stream. Files are selected by time-window pruning and read in ascending
// entry-timestamp order. Batches cross file boundaries transparently.
//
// Each file is loaded in full with os.ReadFile so that all subsequent
// row-scanning is in-memory with no further I/O.
type FileTableReader struct {
	files       []string // ordered selected file paths (absolute)
	fileIdx     int      // index of the currently loaded file in files
	data        []byte   // contents of the current file, loaded in full
	pos         int      // current scan position within data
	schema      *Schema  // parsed once from {typeName}.json
	typeName    string   // stored as Origin in every ColumnMeta
	batchSize   int
	start       time.Time       // zero = open lower bound
	end         time.Time       // zero = open upper bound
	colBuilders []columnBuilder // scratch buffers, pre-allocated and reused
	done        bool
	opts        *ReaderOptions // optional filter/projection pushdown; nil = no pushdown
}

// NewFileTableReader constructs a FileTableReader.
//   - typeDir: resolved directory path for this type (OutputDir/StorageBucket).
//   - typeName: stored as Origin in every ColumnMeta this reader emits.
//   - start, end: time window bounds (zero value = open bound).
//   - batchSize: maximum rows per batch.
//
// Loads {typeName}.json and opens the first selected file during construction so
// that schema field-count errors are caught before any GetNextBatch call.
func NewFileTableReader(typeDir, typeName string, start, end time.Time, batchSize int, opts *ReaderOptions) (*FileTableReader, error) {
	schemaData, err := os.ReadFile(filepath.Join(typeDir, typeName+".json"))
	if err != nil {
		return nil, fmt.Errorf("NewFileTableReader: %w", err)
	}
	schema, err := parseSchema(schemaData)
	if err != nil {
		return nil, fmt.Errorf("NewFileTableReader: %w", err)
	}

	dirEntries, err := os.ReadDir(typeDir)
	if err != nil {
		return nil, fmt.Errorf("NewFileTableReader: %w", err)
	}
	suffix := "_" + typeName + ".csv"
	var names []string
	for _, de := range dirEntries {
		if !de.IsDir() && strings.HasSuffix(de.Name(), suffix) {
			names = append(names, de.Name())
		}
	}

	selected := selectEntries(names, start, end)
	files := make([]string, len(selected))
	for i, name := range selected {
		files[i] = filepath.Join(typeDir, name)
	}

	r := &FileTableReader{
		files:       files,
		schema:      schema,
		typeName:    typeName,
		batchSize:   batchSize,
		start:       start,
		end:         end,
		colBuilders: newColumnBuilders(schema, batchSize),
		opts:        opts,
	}

	if len(files) == 0 {
		r.done = true
		return r, nil
	}

	if err := r.openCurrentFile(); err != nil {
		return nil, err
	}
	return r, nil
}

// openCurrentFile reads r.files[r.fileIdx] into r.data and validates the
// first row's field count. Sets r.pos = 0 so GetNextBatch rescans from the
// beginning of the file (including the first row).
func (r *FileTableReader) openCurrentFile() error {
	data, err := os.ReadFile(r.files[r.fileIdx])
	if err != nil {
		return fmt.Errorf("open %q: %w", r.files[r.fileIdx], err)
	}
	if len(data) == 0 {
		return r.advanceFile()
	}
	if err := validateFirstRowBytes(data, len(r.schema.Columns)); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(r.files[r.fileIdx]), err)
	}
	r.data = data
	r.pos = 0
	return nil
}

// advanceFile releases the current file buffer and loads the next file.
// Sets r.done = true when all files have been read.
func (r *FileTableReader) advanceFile() error {
	r.data = nil
	r.pos = 0
	r.fileIdx++
	if r.fileIdx >= len(r.files) {
		r.done = true
		return nil
	}
	return r.openCurrentFile()
}

// GetNextBatch returns the next dense batch of up to batchSize rows.
// Returns (nil, io.EOF) when the reader is exhausted.
// When opts.Pred is set, batches where all rows are rejected are consumed
// internally and the next candidate batch is tried, matching FilterOp behaviour.
func (r *FileTableReader) GetNextBatch() (*batch.Batch, error) {
	for {
		if r.done {
			return nil, io.EOF
		}

		rows := 0
		for rows < r.batchSize {
			rec := scanRow(r.data, &r.pos, len(r.schema.Columns))
			if rec == nil {
				if advErr := r.advanceFile(); advErr != nil {
					return nil, advErr
				}
				if r.done {
					break
				}
				continue
			}

			// Leading skip: first selected entry only, skip rows before start.
			if !r.start.IsZero() && r.fileIdx == 0 {
				ts, _ := time.Parse(datetimeFormat, string(rec[0]))
				if ts.Before(r.start) {
					continue
				}
			}

			// Trailing stop: last selected entry only, stop when past end.
			if !r.end.IsZero() && r.fileIdx == len(r.files)-1 {
				ts, _ := time.Parse(datetimeFormat, string(rec[0]))
				if ts.After(r.end) {
					r.done = true
					break
				}
			}

			for i, b := range r.colBuilders {
				b.AppendBytes(rec[i])
			}
			rows++
		}

		if rows == 0 {
			return nil, io.EOF
		}

		cols := make([]batch.ColumnVector, len(r.schema.Columns))
		meta := make([]batch.ColumnMeta, len(r.schema.Columns))
		for i, cb := range r.colBuilders {
			cols[i] = cb.FinalizeColumn(rows)
			meta[i] = batch.ColumnMeta{Name: r.schema.Columns[i].Name, Origin: r.typeName, Type: r.schema.Columns[i].Type}
			cb.Reset()
		}
		candidate := &batch.Batch{Length: rows, Schema: meta, Columns: cols}

		if r.opts != nil && r.opts.Pred != nil {
			predBatch := candidate
			if r.opts.PredCols != nil {
				predBatch = projectBatch(candidate, r.opts.PredCols)
			}
			passing := make([]int, 0, rows)
			for row := 0; row < rows; row++ {
				ok, err := r.opts.Pred(predBatch, row)
				if err != nil {
					return nil, err
				}
				if ok {
					passing = append(passing, row)
				}
			}
			if len(passing) == 0 {
				continue // all rows rejected; read next candidate batch
			}
			return compactWithCols(candidate, passing, r.opts.WantCols)
		}

		if r.opts != nil && r.opts.WantCols != nil {
			return projectBatch(candidate, r.opts.WantCols), nil
		}
		return candidate, nil
	}
}

// Close releases the in-memory file buffer.
func (r *FileTableReader) Close() error {
	r.data = nil
	return nil
}

// projectBatch returns a new Batch containing only the columns at wantCols
// indices. The underlying vectors are shared (not copied) — safe because
// column builders produce freshly allocated vectors per batch.
func projectBatch(b *batch.Batch, wantCols []int) *batch.Batch {
	cols := make([]batch.ColumnVector, len(wantCols))
	meta := make([]batch.ColumnMeta, len(wantCols))
	for j, idx := range wantCols {
		cols[j] = b.Columns[idx]
		meta[j] = b.Schema[idx]
	}
	return &batch.Batch{Length: b.Length, Schema: meta, Columns: cols}
}

// compactWithCols returns a new batch containing only the passing rows and
// only the columns at wantCols indices. If wantCols is nil all columns are
// included. Combines the FilterOp compact step and the ProjectOp in one pass.
func compactWithCols(b *batch.Batch, passing []int, wantCols []int) (*batch.Batch, error) {
	indices := wantCols
	if indices == nil {
		indices = make([]int, len(b.Columns))
		for i := range indices {
			indices[i] = i
		}
	}
	n := len(passing)
	cols := make([]batch.ColumnVector, len(indices))
	meta := make([]batch.ColumnMeta, len(indices))
	for j, idx := range indices {
		compacted, err := compactCol(b.Columns[idx], passing)
		if err != nil {
			return nil, err
		}
		cols[j] = compacted
		meta[j] = b.Schema[idx]
	}
	return &batch.Batch{Length: n, Schema: meta, Columns: cols}, nil
}

func compactCol(col batch.ColumnVector, passing []int) (batch.ColumnVector, error) {
	n := len(passing)
	switch v := col.(type) {
	case *batch.Int32Vector:
		vals := make([]int32, n)
		for j, i := range passing {
			vals[j] = v.Values[i]
		}
		return &batch.Int32Vector{Values: vals, Nulls: compactNullBits(v.Nulls, passing)}, nil
	case *batch.Int64Vector:
		vals := make([]int64, n)
		for j, i := range passing {
			vals[j] = v.Values[i]
		}
		return &batch.Int64Vector{Values: vals, Nulls: compactNullBits(v.Nulls, passing)}, nil
	case *batch.Float64Vector:
		vals := make([]float64, n)
		for j, i := range passing {
			vals[j] = v.Values[i]
		}
		return &batch.Float64Vector{Values: vals, Nulls: compactNullBits(v.Nulls, passing)}, nil
	case *batch.StringVector:
		vals := make([]string, n)
		for j, i := range passing {
			vals[j] = v.Values[i]
		}
		return &batch.StringVector{Values: vals, Nulls: compactNullBits(v.Nulls, passing)}, nil
	case *batch.BoolVector:
		vals := make([]bool, n)
		for j, i := range passing {
			vals[j] = v.Values[i]
		}
		return &batch.BoolVector{Values: vals, Nulls: compactNullBits(v.Nulls, passing)}, nil
	case *batch.DatetimeVector:
		vals := make([]time.Time, n)
		for j, i := range passing {
			vals[j] = v.Values[i]
		}
		return &batch.DatetimeVector{Values: vals, Nulls: compactNullBits(v.Nulls, passing)}, nil
	case *batch.TimespanVector:
		vals := make([]time.Duration, n)
		for j, i := range passing {
			vals[j] = v.Values[i]
		}
		return &batch.TimespanVector{Values: vals, Nulls: compactNullBits(v.Nulls, passing)}, nil
	case *batch.DynamicVector:
		vals := make([]string, n)
		for j, i := range passing {
			vals[j] = v.Values[i]
		}
		return &batch.DynamicVector{Values: vals, Nulls: compactNullBits(v.Nulls, passing)}, nil
	default:
		return nil, fmt.Errorf("source: unsupported column type %T", col)
	}
}

func compactNullBits(nulls []uint64, passing []int) []uint64 {
	if len(nulls) == 0 {
		return nil
	}
	n := len(passing)
	result := make([]uint64, (n+63)/64)
	for j, i := range passing {
		if nulls[i/64]>>(uint(i)%64)&1 == 1 {
			result[j/64] |= 1 << (uint(j) % 64)
		}
	}
	return result
}

// validateFieldCount returns an error if the CSV record field count does not
// match the schema column count. Used by BlobTableReader which still uses
// csv.Reader.
func validateFieldCount(schema *Schema, rec []string) error {
	if len(rec) != len(schema.Columns) {
		return fmt.Errorf("field count mismatch: schema has %d columns, row has %d fields",
			len(schema.Columns), len(rec))
	}
	return nil
}

// validateFirstRowBytes counts fields in the first line of data and returns an
// error if the count does not match nCols.
func validateFirstRowBytes(data []byte, nCols int) error {
	count := 0
	quoted := false
	for i := 0; i < len(data); i++ {
		switch data[i] {
		case '"':
			quoted = !quoted
		case ',':
			if !quoted {
				count++
			}
		case '\n', '\r':
			if !quoted {
				count++ // include the last field
				if count != nCols {
					return fmt.Errorf("field count mismatch: schema has %d columns, row has %d fields", nCols, count)
				}
				return nil
			}
		}
	}
	// EOF without a trailing newline.
	count++
	if count != nCols {
		return fmt.Errorf("field count mismatch: schema has %d columns, row has %d fields", nCols, count)
	}
	return nil
}

// scanRow reads the next CSV row from data starting at *pos, advances *pos
// past the row's terminating newline, and returns a slice of length nCols
// whose entries are sub-slices of data (zero allocation for unquoted fields).
// Returns nil when *pos >= len(data).
func scanRow(data []byte, pos *int, nCols int) [][]byte {
	p := *pos
	if p >= len(data) {
		return nil
	}
	fields := make([][]byte, nCols)
	for col := 0; col < nCols; col++ {
		if p >= len(data) || data[p] == '\n' || data[p] == '\r' {
			break
		}
		if data[p] == '"' {
			field, after := scanQuotedField(data, p)
			fields[col] = field
			p = after
		} else {
			start := p
			for p < len(data) && data[p] != ',' && data[p] != '\n' && data[p] != '\r' {
				p++
			}
			fields[col] = data[start:p]
		}
		if p < len(data) && data[p] == ',' {
			p++
		}
	}
	// Advance past the end of the line.
	for p < len(data) && data[p] != '\n' {
		p++
	}
	if p < len(data) {
		p++ // skip '\n'
	}
	*pos = p
	return fields
}

// scanQuotedField scans a double-quoted CSV field from data[pos:] where pos
// points at the opening '"'. Returns the unescaped field ("" → ") and the
// position immediately after the closing '"'.
func scanQuotedField(data []byte, pos int) (field []byte, next int) {
	pos++ // skip opening '"'
	start := pos
	hasEscape := false
	for pos < len(data) {
		if data[pos] == '"' {
			if pos+1 < len(data) && data[pos+1] == '"' {
				hasEscape = true
				pos += 2
				continue
			}
			break // closing quote
		}
		pos++
	}
	raw := data[start:pos]
	if pos < len(data) {
		pos++ // skip closing '"'
	}
	if !hasEscape {
		return raw, pos // zero-alloc: slice into data
	}
	// Unescape '""' → '"'.
	out := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); i++ {
		out = append(out, raw[i])
		if raw[i] == '"' && i+1 < len(raw) && raw[i+1] == '"' {
			i++
		}
	}
	return out, pos
}
