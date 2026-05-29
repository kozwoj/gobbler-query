package ast

// AggFunc identifies the aggregation function in an AggCall.
type AggFunc int

const (
	AggCount  AggFunc = iota // count() — takes no argument
	AggSum                   // sum(field)
	AggAvg                   // avg(field)
	AggMin                   // min(field)
	AggMax                   // max(field)
	AggDcount                // dcount(field) — distinct count
)

// AggCall is an invocation of an aggregation function.
// Field is nil when Func == AggCount.
type AggCall struct {
	Func  AggFunc
	Field *FieldRef // nil for count(); non-nil for sum/avg/min/max/dcount
}

// AggItem is one output column produced by a summarize stage.
//
//	count()             → Alias = ""
//	total = count()     → Alias = "total"
//	avg(durationMs)     → Alias = ""
//	p50 = avg(duration) → Alias = "p50"
type AggItem struct {
	Alias string // empty when the column is bare (no alias)
	Call  AggCall
}
