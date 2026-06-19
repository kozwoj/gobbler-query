package source

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kozwoj/gobbler-query/query/batch"
	"github.com/kozwoj/gobbler-query/query/catalog"
)

// TableReader is the interface implemented by FileTableReader and BlobTableReader.
// GetNextBatch returns the next dense batch of rows, returning (nil, io.EOF)
// when the sequence is exhausted. Any other error is a hard failure.
type TableReader interface {
	GetNextBatch() (*batch.Batch, error)
	Close() error
}

// NewTableReader constructs the appropriate TableReader based on entry.Mode.
// entry.TypeName must be set.
func NewTableReader(entry *catalog.TableEntry, start, end time.Time, batchSize int) (TableReader, error) {
	switch entry.Mode {
	case catalog.StorageModeFile:
		typeDir, err := entry.Resolve()
		if err != nil {
			return nil, err
		}
		return NewFileTableReader(typeDir, entry.TypeName, start, end, batchSize)
	case catalog.StorageModeBlob:
		return nil, fmt.Errorf("NewTableReader: blob mode not yet implemented")
	default:
		return nil, fmt.Errorf("NewTableReader: unknown storage mode %d", entry.Mode)
	}
}

// FileTableReader reads CSV files from a directory as a single ordered row
// stream. Files are selected by time-window pruning and read in ascending
// entry-timestamp order. Batches cross file boundaries transparently.
type FileTableReader struct {
	files       []string    // ordered selected file paths (absolute)
	fileIdx     int         // index of the currently open file in files
	file        *os.File    // currently open file handle
	csv         *csv.Reader // wraps file
	schema      *Schema     // parsed once from {typeName}.json
	typeName    string      // stored as Origin in every ColumnMeta
	batchSize   int
	start       time.Time       // zero = open lower bound
	end         time.Time       // zero = open upper bound
	colBuilders []columnBuilder // scratch buffers, pre-allocated and reused
	pendingRow  []string        // first row of the current file, consumed before csv.Read
	done        bool
}

// NewFileTableReader constructs a FileTableReader.
//   - typeDir: resolved directory path for this type (OutputDir/StorageBucket).
//   - typeName: stored as Origin in every ColumnMeta this reader emits.
//   - start, end: time window bounds (zero value = open bound).
//   - batchSize: maximum rows per batch.
//
// Loads {typeName}.json and opens the first selected file during construction so
// that schema field-count errors are caught before any GetNextBatch call.
func NewFileTableReader(typeDir, typeName string, start, end time.Time, batchSize int) (*FileTableReader, error) {
	// Parse schema from {typeName}.json
	schemaData, err := os.ReadFile(filepath.Join(typeDir, typeName+".json"))
	if err != nil {
		return nil, fmt.Errorf("NewFileTableReader: %w", err)
	}
	schema, err := parseSchema(schemaData)
	if err != nil {
		return nil, fmt.Errorf("NewFileTableReader: %w", err)
	}

	// List CSV files for this type name
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

	// Prune to the time window
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
	}

	if len(files) == 0 {
		r.done = true
		return r, nil
	}

	// Open first file — validates field count before any GetNextBatch call
	if err := r.openCurrentFile(); err != nil {
		return nil, err
	}
	return r, nil
}

// openCurrentFile opens r.files[r.fileIdx], reads the first row for field-count
// validation, and stores it in r.pendingRow for GetNextBatch to consume.
func (r *FileTableReader) openCurrentFile() error {
	f, err := os.Open(r.files[r.fileIdx])
	if err != nil {
		return fmt.Errorf("open %q: %w", r.files[r.fileIdx], err)
	}

	csvr := csv.NewReader(f)
	rec, err := csvr.Read()
	if err == io.EOF {
		// Empty file — skip it
		f.Close()
		return r.advanceFile()
	}
	if err != nil {
		f.Close()
		return fmt.Errorf("%s: %w", filepath.Base(r.files[r.fileIdx]), err)
	}
	if err := validateFieldCount(r.schema, rec); err != nil {
		f.Close()
		return fmt.Errorf("%s: %w", filepath.Base(r.files[r.fileIdx]), err)
	}

	r.file = f
	r.csv = csvr
	r.pendingRow = rec
	return nil
}

// advanceFile closes the current file and opens the next one.
// Sets r.done = true when all files have been read.
func (r *FileTableReader) advanceFile() error {
	if r.file != nil {
		r.file.Close()
		r.file = nil
		r.csv = nil
	}
	r.fileIdx++
	if r.fileIdx >= len(r.files) {
		r.done = true
		return nil
	}
	return r.openCurrentFile()
}

// nextRow returns the next CSV record. It drains r.pendingRow first.
func (r *FileTableReader) nextRow() ([]string, error) {
	if r.pendingRow != nil {
		row := r.pendingRow
		r.pendingRow = nil
		return row, nil
	}
	return r.csv.Read()
}

// GetNextBatch returns the next dense batch of up to batchSize rows.
// Returns (nil, io.EOF) when the reader is exhausted.
func (r *FileTableReader) GetNextBatch() (*batch.Batch, error) {
	if r.done {
		return nil, io.EOF
	}

	rows := 0
	for rows < r.batchSize {
		rec, err := r.nextRow()
		if err == io.EOF {
			if advErr := r.advanceFile(); advErr != nil {
				return nil, advErr
			}
			if r.done {
				break
			}
			continue
		}
		if err != nil {
			return nil, err
		}

		// Leading skip: first selected entry only, skip rows before start
		if !r.start.IsZero() && r.fileIdx == 0 {
			ts, _ := time.Parse(datetimeFormat, rec[0])
			if ts.Before(r.start) {
				continue
			}
		}

		// Trailing stop: last selected entry only, stop when past end
		if !r.end.IsZero() && r.fileIdx == len(r.files)-1 {
			ts, _ := time.Parse(datetimeFormat, rec[0])
			if ts.After(r.end) {
				r.done = true
				break
			}
		}

		for i, b := range r.colBuilders {
			b.Append(rec[i])
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
	return &batch.Batch{Length: rows, Schema: meta, Columns: cols}, nil
}

// Close releases the open file handle.
func (r *FileTableReader) Close() error {
	if r.file != nil {
		err := r.file.Close()
		r.file = nil
		r.csv = nil
		return err
	}
	return nil
}

// validateFieldCount returns an error if the CSV record field count does not
// match the schema column count.
func validateFieldCount(schema *Schema, rec []string) error {
	if len(rec) != len(schema.Columns) {
		return fmt.Errorf("field count mismatch: schema has %d columns, row has %d fields",
			len(schema.Columns), len(rec))
	}
	return nil
}
