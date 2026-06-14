package api

import (
	"fmt"
	"io"
	"time"

	"github.com/kozwoj/gobbler-query/query/batch"
	"github.com/kozwoj/gobbler-query/query/catalog"
	"github.com/kozwoj/gobbler-query/query/logical"
	"github.com/kozwoj/gobbler-query/query/parser"
	"github.com/kozwoj/gobbler-query/query/planner"
	"github.com/kozwoj/gobbler-query/query/source"
)

const defaultBatchSize = 512

// Result holds the full output of a query as an in-memory table.
type Result struct {
	Schema []batch.ColumnMeta // column names and origins
	Rows   [][]any            // Rows[i][j] = value; nil when null
	Nulls  [][]bool           // Nulls[i][j] = true when cell (i,j) is null
}

// Execute parses, validates, plans, and executes the query string q against
// the catalog cat. It loads source schemas from storage, runs the physical
// operator tree, collects all output rows, and returns them as a Result.
//
// batchSize controls the number of rows per internal batch. Pass 0 to use
// the default (512).
func Execute(q string, cat catalog.Catalog, batchSize int) (*Result, error) {
	if batchSize <= 0 {
		batchSize = defaultBatchSize
	}

	// ── 1. Parse ─────────────────────────────────────────────────────────────
	parsed, err := parser.Parse(q)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}

	// ── 2. Build logical plan ────────────────────────────────────────────────
	now := time.Now()
	logicalPlan, err := logical.Build(parsed, now)
	if err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}

	// ── 3. Load schemas for all referenced tables ────────────────────────────
	schemas, err := loadSchemas(logicalPlan, cat)
	if err != nil {
		return nil, err
	}

	// ── 4. Validate logical plan ─────────────────────────────────────────────
	if _, err := logical.InferAndValidate(logicalPlan, schemas); err != nil {
		return nil, fmt.Errorf("validate: %w", err)
	}

	// ── 5. Build physical plan ───────────────────────────────────────────────
	physicalPlan, err := planner.BuildPhysical(logicalPlan, cat, schemas, batchSize)
	if err != nil {
		return nil, fmt.Errorf("build: %w", err)
	}
	defer physicalPlan.Close()

	// ── 6. Execute: drain all batches ────────────────────────────────────────
	var result Result
	for {
		b, err := physicalPlan.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("execute: %w", err)
		}
		if result.Schema == nil {
			result.Schema = b.Schema
		}
		appendBatchToResult(&result, b)
	}

	// Ensure Schema is always non-nil even for empty results.
	if result.Schema == nil && len(schemas) > 0 {
		result.Schema = []batch.ColumnMeta{}
	}

	return &result, nil
}

// appendBatchToResult extracts rows from b into result.
func appendBatchToResult(r *Result, b *batch.Batch) {
	ncols := len(b.Columns)
	for row := 0; row < b.Length; row++ {
		vals := make([]any, ncols)
		nulls := make([]bool, ncols)
		for col, cv := range b.Columns {
			if cv.IsNull(row) {
				nulls[col] = true
				continue
			}
			vals[col] = extractCell(cv, row)
		}
		r.Rows = append(r.Rows, vals)
		r.Nulls = append(r.Nulls, nulls)
	}
}

// extractCell returns the concrete value at row i of cv.
func extractCell(cv batch.ColumnVector, row int) any {
	switch v := cv.(type) {
	case *batch.Int32Vector:
		return v.Values[row]
	case *batch.Int64Vector:
		return v.Values[row]
	case *batch.Float64Vector:
		return v.Values[row]
	case *batch.StringVector:
		return v.Values[row]
	case *batch.BoolVector:
		return v.Values[row]
	case *batch.DatetimeVector:
		return v.Values[row]
	case *batch.TimespanVector:
		return v.Values[row]
	case *batch.DynamicVector:
		return v.Values[row]
	default:
		return nil
	}
}

// loadSchemas walks the logical plan tree and loads a source.Schema for every
// LogicalSource node encountered. Returns an error if any table is missing
// from the catalog or its schema cannot be loaded.
func loadSchemas(node logical.LogicalNode, cat catalog.Catalog) (map[string]*source.Schema, error) {
	schemas := map[string]*source.Schema{}
	if err := collectSchemas(node, cat, schemas); err != nil {
		return nil, err
	}
	return schemas, nil
}

func collectSchemas(node logical.LogicalNode, cat catalog.Catalog, out map[string]*source.Schema) error {
	if node == nil {
		return nil
	}
	if src, ok := node.(*logical.LogicalSource); ok {
		if _, already := out[src.TypeName]; already {
			return nil
		}
		entry, ok := cat[src.TypeName]
		if !ok {
			return fmt.Errorf("table %q not found in catalog", src.TypeName)
		}
		typeDir, err := entry.Resolve()
		if err != nil {
			return fmt.Errorf("resolve %q: %w", src.TypeName, err)
		}
		schema, err := source.LoadSchema(typeDir, src.TypeName)
		if err != nil {
			return fmt.Errorf("load schema for %q: %w", src.TypeName, err)
		}
		out[src.TypeName] = schema
		return nil
	}
	for _, child := range node.Children() {
		if err := collectSchemas(child, cat, out); err != nil {
			return err
		}
	}
	return nil
}
