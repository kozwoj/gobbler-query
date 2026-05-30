package logical_test

import (
	"testing"
	"time"

	"github.com/kozwoj/gobbler-query/query/ast"
	"github.com/kozwoj/gobbler-query/query/logical"
	"github.com/kozwoj/gobbler-query/query/parser"
)

// ─── Helpers ──────────────────────────────────────────────────────────────────

// fixedNow is the query-issue time used across all tests.
var fixedNow = time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

func mustBuild(t *testing.T, src string) logical.LogicalNode {
	t.Helper()
	q, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	node, err := logical.Build(q, fixedNow)
	if err != nil {
		t.Fatalf("Build(%q): %v", src, err)
	}
	return node
}

func asSource(t *testing.T, node logical.LogicalNode) *logical.LogicalSource {
	t.Helper()
	n, ok := node.(*logical.LogicalSource)
	if !ok {
		t.Fatalf("expected *LogicalSource, got %T", node)
	}
	return n
}

func asWhere(t *testing.T, node logical.LogicalNode) *logical.LogicalWhere {
	t.Helper()
	n, ok := node.(*logical.LogicalWhere)
	if !ok {
		t.Fatalf("expected *LogicalWhere, got %T", node)
	}
	return n
}

func asProject(t *testing.T, node logical.LogicalNode) *logical.LogicalProject {
	t.Helper()
	n, ok := node.(*logical.LogicalProject)
	if !ok {
		t.Fatalf("expected *LogicalProject, got %T", node)
	}
	return n
}

func asSummarize(t *testing.T, node logical.LogicalNode) *logical.LogicalSummarize {
	t.Helper()
	n, ok := node.(*logical.LogicalSummarize)
	if !ok {
		t.Fatalf("expected *LogicalSummarize, got %T", node)
	}
	return n
}

func asJoin(t *testing.T, node logical.LogicalNode) *logical.LogicalJoin {
	t.Helper()
	n, ok := node.(*logical.LogicalJoin)
	if !ok {
		t.Fatalf("expected *LogicalJoin, got %T", node)
	}
	return n
}

func asSort(t *testing.T, node logical.LogicalNode) *logical.LogicalSort {
	t.Helper()
	n, ok := node.(*logical.LogicalSort)
	if !ok {
		t.Fatalf("expected *LogicalSort, got %T", node)
	}
	return n
}

func asTake(t *testing.T, node logical.LogicalNode) *logical.LogicalTake {
	t.Helper()
	n, ok := node.(*logical.LogicalTake)
	if !ok {
		t.Fatalf("expected *LogicalTake, got %T", node)
	}
	return n
}

func asCount(t *testing.T, node logical.LogicalNode) *logical.LogicalCount {
	t.Helper()
	n, ok := node.(*logical.LogicalCount)
	if !ok {
		t.Fatalf("expected *LogicalCount, got %T", node)
	}
	return n
}

// child returns the single child of a node, failing if there is not exactly one.
func child(t *testing.T, node logical.LogicalNode) logical.LogicalNode {
	t.Helper()
	ch := node.Children()
	if len(ch) != 1 {
		t.Fatalf("%T: expected 1 child, got %d", node, len(ch))
	}
	return ch[0]
}

// ─── LB: Source time window resolution ───────────────────────────────────────

// LB1: FullScan produces zero Start and End.
func TestLB1_FullScan(t *testing.T) {
	src := asSource(t, mustBuild(t, "events(*)"))

	if src.TypeName != "events" {
		t.Errorf("TypeName = %q, want %q", src.TypeName, "events")
	}
	if !src.Start.IsZero() {
		t.Errorf("Start = %v, want zero", src.Start)
	}
	if !src.End.IsZero() {
		t.Errorf("End = %v, want zero", src.End)
	}
	if src.Children() != nil {
		t.Errorf("LogicalSource.Children() should return nil")
	}
}

// LB2: RelativeLookback resolves against the fixed now passed to Build.
func TestLB2_RelativeLookback(t *testing.T) {
	src := asSource(t, mustBuild(t, "events(last 24h)"))

	wantStart := fixedNow.Add(-24 * time.Hour)
	if !src.Start.Equal(wantStart) {
		t.Errorf("Start = %v, want %v", src.Start, wantStart)
	}
	if !src.End.Equal(fixedNow) {
		t.Errorf("End = %v, want %v", src.End, fixedNow)
	}
}

// LB3: AbsoluteRange extracts the concrete datetime bounds.
func TestLB3_AbsoluteRange(t *testing.T) {
	src := asSource(t, mustBuild(t,
		"events(datetime(2026-01-01 00:00:00.000)..datetime(2026-02-01 00:00:00.000))"))

	wantStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	if !src.Start.Equal(wantStart) {
		t.Errorf("Start = %v, want %v", src.Start, wantStart)
	}
	if !src.End.Equal(wantEnd) {
		t.Errorf("End = %v, want %v", src.End, wantEnd)
	}
}

// ─── P: Pipeline stage chains ─────────────────────────────────────────────────

// P1: where → project → take builds a correctly linked chain.
//
//	requests(last 1h) | where statusCode >= 400 | project timestamp, region | take 100
func TestP1_WhereThenProjectThenTake(t *testing.T) {
	root := mustBuild(t,
		`requests(last 1h) | where statusCode >= 400 | project timestamp, region | take 100`)

	take := asTake(t, root)
	if take.Count != 100 {
		t.Errorf("Take.Count = %d, want 100", take.Count)
	}

	proj := asProject(t, child(t, take))
	if len(proj.Items) != 2 {
		t.Errorf("Project.Items len = %d, want 2", len(proj.Items))
	}

	wh := asWhere(t, child(t, proj))
	if _, ok := wh.Pred.(*ast.CompareExpr); !ok {
		t.Errorf("Where.Pred: expected *CompareExpr, got %T", wh.Pred)
	}

	src := asSource(t, child(t, wh))
	if src.TypeName != "requests" {
		t.Errorf("Source.TypeName = %q, want %q", src.TypeName, "requests")
	}
	if !src.End.Equal(fixedNow) {
		t.Errorf("Source.End = %v, want %v", src.End, fixedNow)
	}
}

// P2: Summarize with group-by fields.
//
//	events(*) | where level == "Error" | summarize total = count(), avg(durationMs) by region
func TestP2_SummarizeWithGroupBy(t *testing.T) {
	root := mustBuild(t,
		`events(*) | where level == "Error" | summarize total = count(), avg(durationMs) by region`)

	sum := asSummarize(t, root)

	if len(sum.Aggs) != 2 {
		t.Errorf("Summarize.Aggs len = %d, want 2", len(sum.Aggs))
	}
	if sum.Aggs[0].Alias != "total" {
		t.Errorf("Aggs[0].Alias = %q, want %q", sum.Aggs[0].Alias, "total")
	}
	if sum.Aggs[0].Call.Func != ast.AggCount {
		t.Errorf("Aggs[0].Func = %v, want AggCount", sum.Aggs[0].Call.Func)
	}
	if sum.Aggs[1].Call.Func != ast.AggAvg {
		t.Errorf("Aggs[1].Func = %v, want AggAvg", sum.Aggs[1].Call.Func)
	}
	if len(sum.GroupBy) != 1 || sum.GroupBy[0].Name != "region" {
		t.Errorf("GroupBy = %v, want [{region}]", sum.GroupBy)
	}

	asWhere(t, child(t, sum))
	asSource(t, child(t, child(t, sum)))
}

// P3: Count stage wraps the source chain.
//
//	events(last 1h) | where level == "Error" | count
func TestP3_Count(t *testing.T) {
	root := mustBuild(t, `events(last 1h) | where level == "Error" | count`)

	cnt := asCount(t, root)
	asWhere(t, child(t, cnt))
	asSource(t, child(t, child(t, cnt)))
}

// P4: Sort + Take.
//
//	events(last 24h) | sort by timestamp desc, region | take 50
func TestP4_SortThenTake(t *testing.T) {
	root := mustBuild(t, `events(last 24h) | sort by timestamp desc, region | take 50`)

	take := asTake(t, root)
	if take.Count != 50 {
		t.Errorf("Take.Count = %d, want 50", take.Count)
	}

	srt := asSort(t, child(t, take))
	if len(srt.Items) != 2 {
		t.Errorf("Sort.Items len = %d, want 2", len(srt.Items))
	}
	if srt.Items[0].Dir != ast.SortDesc {
		t.Errorf("Items[0].Dir = %v, want SortDesc", srt.Items[0].Dir)
	}
	if srt.Items[1].Dir != ast.SortAsc {
		t.Errorf("Items[1].Dir = %v, want SortAsc", srt.Items[1].Dir)
	}
}

// ─── J: Join ──────────────────────────────────────────────────────────────────

// J1: Full join pipeline — left side filtered; right side is a projected sub-query;
// result is projected, sorted, and limited.
//
//	requests(last 1h)
//	| where statusCode >= 400
//	| join (users(*) | project userId, tier) on userId
//	| project timestamp, userId, tier, statusCode
//	| sort by timestamp desc
//	| take 50
func TestJ1_JoinPipeline(t *testing.T) {
	root := mustBuild(t, `
		requests(last 1h)
		| where statusCode >= 400
		| join (users(*) | project userId, tier) on userId
		| project timestamp, userId, tier, statusCode
		| sort by timestamp desc
		| take 50`)

	// root: take
	take := asTake(t, root)
	if take.Count != 50 {
		t.Errorf("Take.Count = %d, want 50", take.Count)
	}

	// sort
	srt := asSort(t, child(t, take))
	if len(srt.Items) != 1 || srt.Items[0].Field.Name != "timestamp" {
		t.Errorf("Sort key = %v, want timestamp", srt.Items[0].Field.Name)
	}
	if srt.Items[0].Dir != ast.SortDesc {
		t.Errorf("Sort dir = %v, want SortDesc", srt.Items[0].Dir)
	}

	// outer project: 4 columns
	proj := asProject(t, child(t, srt))
	if len(proj.Items) != 4 {
		t.Errorf("Project.Items len = %d, want 4", len(proj.Items))
	}

	// join
	join := asJoin(t, child(t, proj))
	if len(join.Keys) != 1 {
		t.Fatalf("Join.Keys len = %d, want 1", len(join.Keys))
	}
	key, ok := join.Keys[0].(*ast.SameNameKey)
	if !ok {
		t.Fatalf("Join.Keys[0]: expected *SameNameKey, got %T", join.Keys[0])
	}
	if key.Name != "userId" {
		t.Errorf("JoinKey.Name = %q, want %q", key.Name, "userId")
	}
	if len(join.Children()) != 2 {
		t.Fatalf("Join.Children() len = %d, want 2", len(join.Children()))
	}

	// left branch: where → source(requests, last 1h)
	leftWhere := asWhere(t, join.Left)
	leftSrc := asSource(t, child(t, leftWhere))
	if leftSrc.TypeName != "requests" {
		t.Errorf("Left source TypeName = %q, want %q", leftSrc.TypeName, "requests")
	}
	if !leftSrc.End.Equal(fixedNow) {
		t.Errorf("Left source End = %v, want %v", leftSrc.End, fixedNow)
	}
	if !leftSrc.Start.Equal(fixedNow.Add(-time.Hour)) {
		t.Errorf("Left source Start = %v, want %v", leftSrc.Start, fixedNow.Add(-time.Hour))
	}

	// right branch: project → source(users, full scan)
	rightProj := asProject(t, join.Right)
	if len(rightProj.Items) != 2 {
		t.Errorf("Right project Items len = %d, want 2", len(rightProj.Items))
	}
	rightSrc := asSource(t, child(t, rightProj))
	if rightSrc.TypeName != "users" {
		t.Errorf("Right source TypeName = %q, want %q", rightSrc.TypeName, "users")
	}
	if !rightSrc.Start.IsZero() || !rightSrc.End.IsZero() {
		t.Errorf("Right source (FullScan) should have zero bounds, got Start=%v End=%v",
			rightSrc.Start, rightSrc.End)
	}
}

// J2: Both sides use RelativeLookback — both must resolve against the same now.
//
//	requests(last 1h) | join (errors(last 30m)) on requestId
func TestJ2_JoinBothSidesResolveSameNow(t *testing.T) {
	root := mustBuild(t, `requests(last 1h) | join (errors(last 30m)) on requestId`)

	join := asJoin(t, root)

	leftSrc := asSource(t, join.Left)
	rightSrc := asSource(t, join.Right)

	// Both End values must be identical (same now snapshot).
	if !leftSrc.End.Equal(rightSrc.End) {
		t.Errorf("Left End %v != Right End %v — different now snapshots", leftSrc.End, rightSrc.End)
	}
	if !leftSrc.End.Equal(fixedNow) {
		t.Errorf("End = %v, want fixedNow %v", leftSrc.End, fixedNow)
	}

	wantLeftStart := fixedNow.Add(-time.Hour)
	wantRightStart := fixedNow.Add(-30 * time.Minute)
	if !leftSrc.Start.Equal(wantLeftStart) {
		t.Errorf("Left Start = %v, want %v", leftSrc.Start, wantLeftStart)
	}
	if !rightSrc.Start.Equal(wantRightStart) {
		t.Errorf("Right Start = %v, want %v", rightSrc.Start, wantRightStart)
	}
}
