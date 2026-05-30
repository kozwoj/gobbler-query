package logical

import (
	"time"

	"github.com/kozwoj/gobbler-query/query/ast"
)

// LogicalNode is a node in the logical query plan.
// Every node except LogicalSource has an Input child; LogicalJoin has two.
type LogicalNode interface {
	logicalNode()
	Children() []LogicalNode
}

// ─── Source ───────────────────────────────────────────────────────────────────

// LogicalSource is the leaf of every plan tree. It names the data source and
// carries a resolved, concrete time window computed at query-issue time.
// A zero Start means no lower bound; a zero End means no upper bound.
// Both zero indicates a full scan (all available data).
type LogicalSource struct {
	TypeName string
	Start    time.Time // zero = no lower bound
	End      time.Time // zero = no upper bound
}

// ─── Pipeline stages ──────────────────────────────────────────────────────────

// LogicalWhere filters rows using a boolean predicate.
type LogicalWhere struct {
	Input LogicalNode
	Pred  ast.BoolExpr
}

// LogicalProject selects, renames, and/or computes output columns.
type LogicalProject struct {
	Input LogicalNode
	Items []ast.ProjectItem
}

// LogicalSummarize groups rows and computes aggregate values.
type LogicalSummarize struct {
	Input   LogicalNode
	Aggs    []ast.AggItem
	GroupBy []ast.FieldRef
}

// LogicalJoin performs an inner equality join.
// Left is the probe side; Right is the build side (sub-query).
type LogicalJoin struct {
	Left  LogicalNode
	Right LogicalNode
	Keys  []ast.JoinKey
}

// LogicalSort orders the result set.
type LogicalSort struct {
	Input LogicalNode
	Items []ast.SortItem
}

// LogicalTake limits the number of output rows.
type LogicalTake struct {
	Input LogicalNode
	Count int64
}

// LogicalCount is syntactic sugar for summarize count().
type LogicalCount struct {
	Input LogicalNode
}

// ─── Interface implementations ────────────────────────────────────────────────

func (*LogicalSource) logicalNode()    {}
func (*LogicalWhere) logicalNode()     {}
func (*LogicalProject) logicalNode()   {}
func (*LogicalSummarize) logicalNode() {}
func (*LogicalJoin) logicalNode()      {}
func (*LogicalSort) logicalNode()      {}
func (*LogicalTake) logicalNode()      {}
func (*LogicalCount) logicalNode()     {}

func (n *LogicalSource) Children() []LogicalNode    { return nil }
func (n *LogicalWhere) Children() []LogicalNode     { return []LogicalNode{n.Input} }
func (n *LogicalProject) Children() []LogicalNode   { return []LogicalNode{n.Input} }
func (n *LogicalSummarize) Children() []LogicalNode { return []LogicalNode{n.Input} }
func (n *LogicalJoin) Children() []LogicalNode      { return []LogicalNode{n.Left, n.Right} }
func (n *LogicalSort) Children() []LogicalNode      { return []LogicalNode{n.Input} }
func (n *LogicalTake) Children() []LogicalNode      { return []LogicalNode{n.Input} }
func (n *LogicalCount) Children() []LogicalNode     { return []LogicalNode{n.Input} }
