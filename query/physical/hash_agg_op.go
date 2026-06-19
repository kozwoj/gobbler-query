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
	result *rowStore
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
	accs   []expr.AggAccumulator
	byVals []expr.Value // null represented as Value{Kind: KindNull}
}

func (op *HashAggregateOp) Next() (*batch.Batch, error) {
	if op.result == nil {
		if err := op.aggregate(); err != nil {
			return nil, err
		}
	}
	if op.offset >= op.result.rowCount() {
		return nil, io.EOF
	}
	bs := op.BatchSize
	if bs <= 0 {
		bs = defaultSortBatchSize
	}
	end := op.offset + bs
	if end > op.result.rowCount() {
		end = op.result.rowCount()
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
			byVals := make([]expr.Value, len(op.GroupBy))
			for i, gc := range op.GroupBy {
				v, err := gc.Eval(b, row)
				if err != nil {
					return err
				}
				byVals[i] = v
			}

			// Build the map key from group-by values.
			key := groupKeyFromValues(byVals)

			// Look up or create the group.
			g, ok := groups[key]
			if !ok {
				accs := make([]expr.AggAccumulator, len(op.Aggs))
				for i, a := range op.Aggs {
					accs[i] = a.NewAcc()
				}
				g = &aggGroup{accs: accs, byVals: byVals}
				groups[key] = g
				groupOrder = append(groupOrder, key)
			}

			// Feed each accumulator.
			for i, a := range op.Aggs {
				var v expr.Value
				if a.Eval != nil {
					var err error
					v, err = a.Eval(b, row)
					if err != nil {
						return err
					}
				}
				// For count(), v stays zero-value (KindNull == 0), which is fine:
				// countAcc.Ingest ignores the value entirely.
				g.accs[i].Ingest(v)
			}
		}
	}

	// Build the result rowStore from the groups.
	m := &rowStore{
		schema: op.buildOutputSchema(),
		kinds:  op.buildColKinds(),
	}
	ncols := len(op.Aggs) + len(op.GroupBy)
	vals := make([]expr.Value, ncols)

	for _, key := range groupOrder {
		g := groups[key]

		// Agg columns first.
		for i, acc := range g.accs {
			vals[i] = acc.Result()
		}

		// Group-by columns after.
		offset := len(op.Aggs)
		for i, v := range g.byVals {
			vals[offset+i] = v
		}

		m.appendValues(vals)
	}

	op.result = m
	return nil
}

// buildOutputSchema returns the batch.ColumnMeta slice for the output:
// agg columns first, then group-by columns.
func (op *HashAggregateOp) buildOutputSchema() []batch.ColumnMeta {
	schema := make([]batch.ColumnMeta, 0, len(op.Aggs)+len(op.GroupBy))
	for _, a := range op.Aggs {
		schema = append(schema, batch.ColumnMeta{Name: a.Name, Type: a.Type})
	}
	for _, g := range op.GroupBy {
		schema = append(schema, batch.ColumnMeta{Name: g.Name, Origin: g.Origin, Type: g.Type})
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
