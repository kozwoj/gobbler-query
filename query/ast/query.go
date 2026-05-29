package ast

// Query is the root node of every parsed GQL query.
//
//	Logs(last 24h)
//	| where statusCode >= 400
//	| project timestamp, region
type Query struct {
	Source Source
	Stages []Stage
}

// Source names the item type and carries the required time window.
type Source struct {
	TypeName   string     // e.g. "Logs" or "alpha-folder"
	TimeWindow TimeWindow // always non-nil after a successful parse
}

// TimeWindow constrains which data files are opened for a source.
// Exactly one concrete type is used per source.
type TimeWindow interface {
	timeWindowNode()
}

// FullScan reads every file in the type's directory or container.
// Written as Logs(*) in query text.
type FullScan struct{}

// RelativeLookback selects files from (now − Duration) onward.
// Written as Logs(last 24h).
type RelativeLookback struct {
	Duration TimespanLit
}

// AbsoluteRange selects files whose timestamps overlap [Start, End].
// Written as Logs(datetime(T1) .. datetime(T2)).
type AbsoluteRange struct {
	Start DatetimeLit
	End   DatetimeLit
}

func (*FullScan) timeWindowNode()         {}
func (*RelativeLookback) timeWindowNode() {}
func (*AbsoluteRange) timeWindowNode()    {}
