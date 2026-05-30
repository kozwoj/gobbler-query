package logical

import (
	"fmt"
	"time"

	"github.com/kozwoj/gobbler-query/query/ast"
)

// Build converts a parsed AST query into a logical plan.
// now must be the time at which the query was issued; it is used to resolve
// RelativeLookback windows (e.g. "last 24h") into concrete time.Time bounds.
// The caller is responsible for passing time.Now() at query-receipt time so
// that the window does not shift as the query executes.
func Build(q *ast.Query, now time.Time) (LogicalNode, error) {
	b := &builder{now: now}
	return b.buildQuery(q)
}

// ─── internal builder ─────────────────────────────────────────────────────────

type builder struct {
	now time.Time
}

func (b *builder) buildQuery(q *ast.Query) (LogicalNode, error) {
	start, end := resolveWindow(q.Source.TimeWindow, b.now)
	var node LogicalNode = &LogicalSource{
		TypeName: q.Source.TypeName,
		Start:    start,
		End:      end,
	}

	for _, stage := range q.Stages {
		var err error
		node, err = b.buildStage(node, stage)
		if err != nil {
			return nil, err
		}
	}
	return node, nil
}

func (b *builder) buildStage(input LogicalNode, stage ast.Stage) (LogicalNode, error) {
	switch s := stage.(type) {

	case *ast.WhereStage:
		return &LogicalWhere{Input: input, Pred: s.Expr}, nil

	case *ast.ProjectStage:
		return &LogicalProject{Input: input, Items: s.Items}, nil

	case *ast.SummarizeStage:
		return &LogicalSummarize{Input: input, Aggs: s.Aggs, GroupBy: s.GroupBy}, nil

	case *ast.JoinStage:
		right, err := b.buildQuery(s.Right)
		if err != nil {
			return nil, err
		}
		return &LogicalJoin{Left: input, Right: right, Keys: s.Keys}, nil

	case *ast.SortStage:
		return &LogicalSort{Input: input, Items: s.Items}, nil

	case *ast.TakeStage:
		return &LogicalTake{Input: input, Count: s.Count}, nil

	case *ast.CountStage:
		return &LogicalCount{Input: input}, nil

	default:
		return nil, fmt.Errorf("logical: unknown stage type %T", stage)
	}
}

// ─── time window resolution ───────────────────────────────────────────────────

// resolveWindow converts an ast.TimeWindow into a concrete (start, end) pair.
// Both values are zero for a FullScan (no bounds).
func resolveWindow(w ast.TimeWindow, now time.Time) (start, end time.Time) {
	switch tw := w.(type) {
	case *ast.FullScan:
		return time.Time{}, time.Time{}
	case *ast.RelativeLookback:
		return now.Add(-tw.Duration.Duration), now
	case *ast.AbsoluteRange:
		return tw.Start.Value, tw.End.Value
	default:
		// unreachable if the parser is correct; treat as full scan
		return time.Time{}, time.Time{}
	}
}
