package ast

// Stage is one step in the query pipeline.  The concrete types are
// WhereStage, ProjectStage, SummarizeStage, JoinStage, SortStage,
// TakeStage, and CountStage.
type Stage interface {
	stageNode()
}

// ─── Where ────────────────────────────────────────────────────────────────────

// WhereStage keeps only rows for which Expr evaluates to true.
//
//	| where statusCode >= 400 and region == "eastus"
type WhereStage struct {
	Expr BoolExpr
}

// ─── Project ──────────────────────────────────────────────────────────────────

// ProjectStage selects, renames, and/or computes output columns.
//
//	| project timestamp, code = statusCode, dur = endTime - startTime
type ProjectStage struct {
	Items []ProjectItem
}

// ProjectItem is one output column in a project stage.
//
//	timestamp                   → Alias = "", Expr = *FieldRefExpr{...}
//	code = statusCode           → Alias = "code", Expr = *FieldRefExpr{...}
//	dur  = endTime - startTime  → Alias = "dur",  Expr = *BinaryExpr{...}
type ProjectItem struct {
	Alias string     // empty → bare field reference; Expr is guaranteed *FieldRefExpr
	Expr  ScalarExpr // never nil
}

// ─── Summarize ────────────────────────────────────────────────────────────────

// SummarizeStage groups rows and computes aggregate values.
//
//	| summarize total = count(), avg(durationMs) by region, statusCode
type SummarizeStage struct {
	Aggs    []AggItem  // one or more aggregation outputs
	GroupBy []FieldRef // empty when there is no "by" clause
}

// ─── Join ─────────────────────────────────────────────────────────────────────

// JoinStage performs an inner join with a sub-query.
//
//	| join (Users | project userId, tier) on userId
//	| join (Orders) on $left.id == $right.orderId
type JoinStage struct {
	Right *Query    // sub-query forming the right side of the join
	Keys  []JoinKey // one or more key descriptors
}

// ─── Sort ─────────────────────────────────────────────────────────────────────

// SortStage orders the result set.
//
//	| sort by timestamp desc, region asc
type SortStage struct {
	Items []SortItem
}

// SortItem specifies one sort key and its direction.
type SortItem struct {
	Field FieldRef
	Dir   SortDir // defaults to SortAsc when the keyword is omitted
}

// SortDir is the sort direction for a SortItem.
type SortDir int

const (
	SortAsc  SortDir = iota // "asc" or omitted
	SortDesc                // "desc"
)

// ─── Take ─────────────────────────────────────────────────────────────────────

// TakeStage limits the number of output rows.
//
//	| take 100
type TakeStage struct {
	Count int64
}

// ─── Count ────────────────────────────────────────────────────────────────────

// CountStage is syntactic sugar for "| summarize count()".
//
//	| count
type CountStage struct{}

// stage marker methods
func (*WhereStage) stageNode()     {}
func (*ProjectStage) stageNode()   {}
func (*SummarizeStage) stageNode() {}
func (*JoinStage) stageNode()      {}
func (*SortStage) stageNode()      {}
func (*TakeStage) stageNode()      {}
func (*CountStage) stageNode()     {}
