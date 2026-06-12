package physical

import (
	"io"

	"github.com/kozwoj/gobbler-query/query/batch"
	"github.com/kozwoj/gobbler-query/query/expr"
	"github.com/kozwoj/gobbler-query/query/source"
)

// HashAggregateOp is a blocking operator that implements the summarize stage.
// It drains its entire input on the first Next() call, groups rows by the
// group-by column values, accumulates one set of aggregators per group, then
// emits the result rows in insertion order, one batch at a time.
//
// Output schema: agg columns first (in Aggs order), then group-by columns
// (in GroupBy order) — matching the schema produced by inferSummarize.
type HashAggregateOp struct {
	Input     Operator
	Aggs      []expr.CompiledAggItem // aggregation output columns
	GroupBy   []GroupByCol           // group-by columns
	BatchSize int                    // rows per output batch; defaults to defaultSortBatchSize

	// populated on first Next()
	result *materializedRows
	offset int
}

// GroupByCol describes one group-by column: its index in the input schema and
// the eval function to extract its value from a batch row.
type GroupByCol struct {
	Name   string
	Origin string
	Type   source.ColumnType
	Eval   expr.ScalarEval
}

// aggGroup holds the per-group accumulators and the captured group-by values.
type aggGroup struct {
	accs    []expr.AggAccumulator
	byVals  []any
	byNulls []bool
}

func (op *HashAggregateOp) Next() (*batch.Batch, error) {
	if op.result == nil {
		if err := op.aggregate(); err != nil {
			return nil, err
		}
	}
	if op.offset >= len(op.result.Rows) {
		return nil, io.EOF
	}
	bs := op.BatchSize
	if bs <= 0 {
		bs = defaultSortBatchSize
	}
	end := op.offset + bs
	if end > len(op.result.Rows) {
		end = len(op.result.Rows)
	}
	b := op.result.buildBatchFromRows(op.offset, end)
	op.offset = end
	return b, nil
}

func (op *HashAggregateOp) Close() error {
	op.result = nil
	return op.Input.Close()
}

// aggregate drains the input, groups rows, and builds op.result.
func (op *HashAggregateOp) aggregate() error {
	// groupOrder preserves insertion order so output is deterministic.
	groupOrder := []string{}
	groups := map[string]*aggGroup{}

	for {
		b, err := op.Input.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		for row := 0; row < b.Length; row++ {
			// Evaluate group-by columns for this row.
			byVals := make([]any, len(op.GroupBy))
			byNulls := make([]bool, len(op.GroupBy))
			for i, gc := range op.GroupBy {
				v, null, err := gc.Eval(b, row)
				if err != nil {
					return err
				}
				byNulls[i] = null
				if !null {
					byVals[i] = v
				}
			}

			// Build the map key from group-by values.
			key := groupKey(byVals, byNulls, seqIndices(len(op.GroupBy)))

			// Look up or create the group.
			g, ok := groups[key]
			if !ok {
				accs := make([]expr.AggAccumulator, len(op.Aggs))
				for i, a := range op.Aggs {
					accs[i] = a.NewAcc()
				}
				g = &aggGroup{accs: accs, byVals: byVals, byNulls: byNulls}
				groups[key] = g
				groupOrder = append(groupOrder, key)
			}

			// Feed each accumulator.
			for i, a := range op.Aggs {
				var val any
				var null bool
				if a.Eval == nil {
					// count() — always ingest a non-null sentinel
					val, null = nil, false
				} else {
					var err error
					val, null, err = a.Eval(b, row)
					if err != nil {
						return err
					}
				}
				g.accs[i].Ingest(val, null)
			}
		}
	}

	// Build the result materializedRows from the groups.
	m := &materializedRows{}
	m.Schema = op.buildOutputSchema()
	ncols := len(op.Aggs) + len(op.GroupBy)
	m.ColKinds = op.buildColKinds()

	for _, key := range groupOrder {
		g := groups[key]
		row := make([]any, ncols)
		nulls := make([]bool, ncols)

		// Agg columns first.
		for i, acc := range g.accs {
			v, null := acc.Result()
			nulls[i] = null
			if !null {
				row[i] = v
			}
		}

		// Group-by columns after.
		offset := len(op.Aggs)
		for i, v := range g.byVals {
			nulls[offset+i] = g.byNulls[i]
			if !g.byNulls[i] {
				row[offset+i] = v
			}
		}

		m.Rows = append(m.Rows, row)
		m.Nulls = append(m.Nulls, nulls)
	}

	op.result = m
	return nil
}

// buildOutputSchema returns the batch.ColumnMeta slice for the output:
// agg columns first, then group-by columns.
func (op *HashAggregateOp) buildOutputSchema() []batch.ColumnMeta {
	schema := make([]batch.ColumnMeta, 0, len(op.Aggs)+len(op.GroupBy))
	for _, a := range op.Aggs {
		schema = append(schema, batch.ColumnMeta{Name: a.Name})
	}
	for _, g := range op.GroupBy {
		schema = append(schema, batch.ColumnMeta{Name: g.Name, Origin: g.Origin})
	}
	return schema
}

// buildColKinds returns the VecKind slice for agg then group-by columns.
func (op *HashAggregateOp) buildColKinds() []VecKind {
	kinds := make([]VecKind, 0, len(op.Aggs)+len(op.GroupBy))
	for _, a := range op.Aggs {
		kinds = append(kinds, VecKindFromColumnType(a.Type))
	}
	for _, g := range op.GroupBy {
		kinds = append(kinds, VecKindFromColumnType(g.Type))
	}
	return kinds
}

// seqIndices returns [0, 1, ..., n-1].
func seqIndices(n int) []int {
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	return idx
}
