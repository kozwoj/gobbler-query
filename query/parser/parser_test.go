package parser_test

// Comprehensive parser tests, organised by tier.
// Helpers (mustParse, mustParseErr, relLookback) live in parser_timespan_test.go.

import (
	"testing"
	"time"

	"github.com/kozwoj/gobbler-query/query/ast"
)

// ─── Stage-extraction helpers ─────────────────────────────────────────────────

func stageN(t *testing.T, q *ast.Query, i int) ast.Stage {
	t.Helper()
	if i >= len(q.Stages) {
		t.Fatalf("expected at least %d stage(s), got %d", i+1, len(q.Stages))
	}
	return q.Stages[i]
}

func asWhere(t *testing.T, s ast.Stage) *ast.WhereStage {
	t.Helper()
	ws, ok := s.(*ast.WhereStage)
	if !ok {
		t.Fatalf("expected *WhereStage, got %T", s)
	}
	return ws
}

func asProject(t *testing.T, s ast.Stage) *ast.ProjectStage {
	t.Helper()
	ps, ok := s.(*ast.ProjectStage)
	if !ok {
		t.Fatalf("expected *ProjectStage, got %T", s)
	}
	return ps
}

func asSort(t *testing.T, s ast.Stage) *ast.SortStage {
	t.Helper()
	ss, ok := s.(*ast.SortStage)
	if !ok {
		t.Fatalf("expected *SortStage, got %T", s)
	}
	return ss
}

func asTake(t *testing.T, s ast.Stage) *ast.TakeStage {
	t.Helper()
	ts, ok := s.(*ast.TakeStage)
	if !ok {
		t.Fatalf("expected *TakeStage, got %T", s)
	}
	return ts
}

func asCount(t *testing.T, s ast.Stage) *ast.CountStage {
	t.Helper()
	cs, ok := s.(*ast.CountStage)
	if !ok {
		t.Fatalf("expected *CountStage, got %T", s)
	}
	return cs
}

func asSummarize(t *testing.T, s ast.Stage) *ast.SummarizeStage {
	t.Helper()
	ss, ok := s.(*ast.SummarizeStage)
	if !ok {
		t.Fatalf("expected *SummarizeStage, got %T", s)
	}
	return ss
}

func asJoin(t *testing.T, s ast.Stage) *ast.JoinStage {
	t.Helper()
	js, ok := s.(*ast.JoinStage)
	if !ok {
		t.Fatalf("expected *JoinStage, got %T", s)
	}
	return js
}

func asCompare(t *testing.T, be ast.BoolExpr) *ast.CompareExpr {
	t.Helper()
	ce, ok := be.(*ast.CompareExpr)
	if !ok {
		t.Fatalf("expected *CompareExpr, got %T", be)
	}
	return ce
}

func asFieldRef(t *testing.T, se ast.ScalarExpr) ast.FieldRef {
	t.Helper()
	fre, ok := se.(*ast.FieldRefExpr)
	if !ok {
		t.Fatalf("expected *FieldRefExpr, got %T", se)
	}
	return fre.Ref
}

// ─── Tier 1 – Source only ─────────────────────────────────────────────────────

func TestSourceFullScan(t *testing.T) {
	q := mustParse(t, "events(*)")

	if q.Source.TypeName != "events" {
		t.Errorf("TypeName = %q, want %q", q.Source.TypeName, "events")
	}
	if _, ok := q.Source.TimeWindow.(*ast.FullScan); !ok {
		t.Errorf("TimeWindow: expected *FullScan, got %T", q.Source.TimeWindow)
	}
	if len(q.Stages) != 0 {
		t.Errorf("Stages: expected empty, got %d", len(q.Stages))
	}
}

func TestSourceRelativeLookback(t *testing.T) {
	q := mustParse(t, "events(last 7d)")

	if q.Source.TypeName != "events" {
		t.Errorf("TypeName = %q, want %q", q.Source.TypeName, "events")
	}
	got := relLookback(t, q)
	if got != 7*24*time.Hour {
		t.Errorf("Duration = %v, want %v", got, 7*24*time.Hour)
	}
}

func TestSourceAbsoluteRange(t *testing.T) {
	q := mustParse(t, "events(datetime(2026-01-01 00:00:00.000)..datetime(2026-02-01 00:00:00.000))")

	ar, ok := q.Source.TimeWindow.(*ast.AbsoluteRange)
	if !ok {
		t.Fatalf("TimeWindow: expected *AbsoluteRange, got %T", q.Source.TimeWindow)
	}
	wantStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	if !ar.Start.Value.Equal(wantStart) {
		t.Errorf("Start = %v, want %v", ar.Start.Value, wantStart)
	}
	if !ar.End.Value.Equal(wantEnd) {
		t.Errorf("End = %v, want %v", ar.End.Value, wantEnd)
	}
}

// ─── Tier 2 – Single simple stages ───────────────────────────────────────────

func TestCountStage(t *testing.T) {
	q := mustParse(t, "events(*) | count")

	if len(q.Stages) != 1 {
		t.Fatalf("Stages: want 1, got %d", len(q.Stages))
	}
	asCount(t, stageN(t, q, 0))
}

func TestTakeStage(t *testing.T) {
	q := mustParse(t, "events(*) | take 100")

	ts := asTake(t, stageN(t, q, 0))
	if ts.Count != 100 {
		t.Errorf("Count = %d, want 100", ts.Count)
	}
}

func TestSortStageDefaultAsc(t *testing.T) {
	q := mustParse(t, "events(last 7d) | sort by timestamp")

	ss := asSort(t, stageN(t, q, 0))
	if len(ss.Items) != 1 {
		t.Fatalf("Items: want 1, got %d", len(ss.Items))
	}
	item := ss.Items[0]
	if item.Field.Name != "timestamp" {
		t.Errorf("Field = %q, want %q", item.Field.Name, "timestamp")
	}
	if item.Dir != ast.SortAsc {
		t.Errorf("Dir = %v, want SortAsc", item.Dir)
	}
}

func TestSortStageDesc(t *testing.T) {
	q := mustParse(t, "events(last 7d) | sort by timestamp desc")

	ss := asSort(t, stageN(t, q, 0))
	if ss.Items[0].Dir != ast.SortDesc {
		t.Errorf("Dir = %v, want SortDesc", ss.Items[0].Dir)
	}
}

func TestSortStageMultiKey(t *testing.T) {
	q := mustParse(t, "events(last 7d) | sort by timestamp asc, level desc")

	ss := asSort(t, stageN(t, q, 0))
	if len(ss.Items) != 2 {
		t.Fatalf("Items: want 2, got %d", len(ss.Items))
	}
	if ss.Items[0].Field.Name != "timestamp" || ss.Items[0].Dir != ast.SortAsc {
		t.Errorf("Items[0] = {%q, %v}, want {timestamp, SortAsc}", ss.Items[0].Field.Name, ss.Items[0].Dir)
	}
	if ss.Items[1].Field.Name != "level" || ss.Items[1].Dir != ast.SortDesc {
		t.Errorf("Items[1] = {%q, %v}, want {level, SortDesc}", ss.Items[1].Field.Name, ss.Items[1].Dir)
	}
}

func TestProjectBareFields(t *testing.T) {
	q := mustParse(t, "events(last 7d) | project timestamp, message")

	ps := asProject(t, stageN(t, q, 0))
	if len(ps.Items) != 2 {
		t.Fatalf("Items: want 2, got %d", len(ps.Items))
	}
	for i, want := range []string{"timestamp", "message"} {
		item := ps.Items[i]
		if item.Alias != "" {
			t.Errorf("Items[%d].Alias = %q, want empty", i, item.Alias)
		}
		ref := asFieldRef(t, item.Expr)
		if ref.Name != want {
			t.Errorf("Items[%d].Expr field = %q, want %q", i, ref.Name, want)
		}
	}
}

func TestProjectAliasedArithmetic(t *testing.T) {
	q := mustParse(t, "events(last 7d) | project elapsed = endTime - startTime")

	ps := asProject(t, stageN(t, q, 0))
	if len(ps.Items) != 1 {
		t.Fatalf("Items: want 1, got %d", len(ps.Items))
	}
	item := ps.Items[0]
	if item.Alias != "elapsed" {
		t.Errorf("Alias = %q, want %q", item.Alias, "elapsed")
	}
	bin, ok := item.Expr.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("Expr: expected *BinaryExpr, got %T", item.Expr)
	}
	if bin.Op != ast.BinSub {
		t.Errorf("Op = %v, want BinSub", bin.Op)
	}
	if asFieldRef(t, bin.Left).Name != "endTime" {
		t.Errorf("Left = %v, want endTime", bin.Left)
	}
	if asFieldRef(t, bin.Right).Name != "startTime" {
		t.Errorf("Right = %v, want startTime", bin.Right)
	}
}

// ─── Tier 3 – Where atomic predicates ────────────────────────────────────────

func TestWhereEqString(t *testing.T) {
	q := mustParse(t, `events(last 7d) | where level == "Error"`)

	ce := asCompare(t, asWhere(t, stageN(t, q, 0)).Expr)
	if asFieldRef(t, ce.Left).Name != "level" {
		t.Errorf("Left.Name = %v", ce.Left)
	}
	if ce.Op != ast.CmpEq {
		t.Errorf("Op = %v, want CmpEq", ce.Op)
	}
	sl, ok := ce.Right.(*ast.StringLit)
	if !ok || sl.Value != "Error" {
		t.Errorf("Right = %v, want StringLit{Error}", ce.Right)
	}
}

func TestWhereGtEqInt(t *testing.T) {
	q := mustParse(t, "events(last 7d) | where statusCode >= 400")

	ce := asCompare(t, asWhere(t, stageN(t, q, 0)).Expr)
	if asFieldRef(t, ce.Left).Name != "statusCode" {
		t.Errorf("Left.Name = %v", ce.Left)
	}
	if ce.Op != ast.CmpGtEq {
		t.Errorf("Op = %v, want CmpGtEq", ce.Op)
	}
	il, ok := ce.Right.(*ast.IntLit)
	if !ok || il.Value != 400 {
		t.Errorf("Right = %v, want IntLit{400}", ce.Right)
	}
}

func TestWhereContains(t *testing.T) {
	q := mustParse(t, `events(last 7d) | where message contains "timeout"`)

	ce := asCompare(t, asWhere(t, stageN(t, q, 0)).Expr)
	if ce.Op != ast.CmpContains {
		t.Errorf("Op = %v, want CmpContains", ce.Op)
	}
}

func TestWhereStartswith(t *testing.T) {
	q := mustParse(t, `events(last 7d) | where message startswith "ERR"`)

	ce := asCompare(t, asWhere(t, stageN(t, q, 0)).Expr)
	if ce.Op != ast.CmpStartswith {
		t.Errorf("Op = %v, want CmpStartswith", ce.Op)
	}
}

func TestWhereTildeEq(t *testing.T) {
	q := mustParse(t, `events(last 7d) | where name =~ "admin"`)

	ce := asCompare(t, asWhere(t, stageN(t, q, 0)).Expr)
	if ce.Op != ast.CmpTildeEq {
		t.Errorf("Op = %v, want CmpTildeEq", ce.Op)
	}
}

func TestWhereIsnull(t *testing.T) {
	q := mustParse(t, "events(last 7d) | where isnull(userId)")

	isn, ok := asWhere(t, stageN(t, q, 0)).Expr.(*ast.IsNullExpr)
	if !ok {
		t.Fatalf("Expr: expected *IsNullExpr, got %T", asWhere(t, stageN(t, q, 0)).Expr)
	}
	if isn.Kind != ast.KindIsNull {
		t.Errorf("Kind = %v, want KindIsNull", isn.Kind)
	}
	if isn.Field.Name != "userId" {
		t.Errorf("Field = %q, want %q", isn.Field.Name, "userId")
	}
}

func TestWhereIsnotnull(t *testing.T) {
	q := mustParse(t, "events(last 7d) | where isnotnull(userId)")

	isn, ok := asWhere(t, stageN(t, q, 0)).Expr.(*ast.IsNullExpr)
	if !ok {
		t.Fatalf("Expr: expected *IsNullExpr, got %T", asWhere(t, stageN(t, q, 0)).Expr)
	}
	if isn.Kind != ast.KindIsNotNull {
		t.Errorf("Kind = %v, want KindIsNotNull", isn.Kind)
	}
}

func TestWhereIsempty(t *testing.T) {
	q := mustParse(t, "events(last 7d) | where isempty(message)")

	isn, ok := asWhere(t, stageN(t, q, 0)).Expr.(*ast.IsNullExpr)
	if !ok {
		t.Fatalf("Expr: expected *IsNullExpr, got %T", asWhere(t, stageN(t, q, 0)).Expr)
	}
	if isn.Kind != ast.KindIsEmpty {
		t.Errorf("Kind = %v, want KindIsEmpty", isn.Kind)
	}
	if isn.Field.Name != "message" {
		t.Errorf("Field = %q, want %q", isn.Field.Name, "message")
	}
}

func TestWhereIn(t *testing.T) {
	q := mustParse(t, `events(last 7d) | where level in ("Error", "Warning")`)

	inx, ok := asWhere(t, stageN(t, q, 0)).Expr.(*ast.InExpr)
	if !ok {
		t.Fatalf("Expr: expected *InExpr, got %T", asWhere(t, stageN(t, q, 0)).Expr)
	}
	if inx.Field.Name != "level" {
		t.Errorf("Field = %q, want %q", inx.Field.Name, "level")
	}
	if inx.Negated {
		t.Errorf("Negated = true, want false")
	}
	if len(inx.Values) != 2 {
		t.Fatalf("Values: want 2, got %d", len(inx.Values))
	}
	for i, want := range []string{"Error", "Warning"} {
		sl, ok := inx.Values[i].(*ast.StringLit)
		if !ok || sl.Value != want {
			t.Errorf("Values[%d] = %v, want StringLit{%q}", i, inx.Values[i], want)
		}
	}
}

func TestWhereNotIn(t *testing.T) {
	q := mustParse(t, "events(last 7d) | where code !in (200, 201, 204)")

	inx, ok := asWhere(t, stageN(t, q, 0)).Expr.(*ast.InExpr)
	if !ok {
		t.Fatalf("Expr: expected *InExpr, got %T", asWhere(t, stageN(t, q, 0)).Expr)
	}
	if inx.Field.Name != "code" {
		t.Errorf("Field = %q, want %q", inx.Field.Name, "code")
	}
	if !inx.Negated {
		t.Errorf("Negated = false, want true")
	}
	if len(inx.Values) != 3 {
		t.Fatalf("Values: want 3, got %d", len(inx.Values))
	}
	for i, want := range []int64{200, 201, 204} {
		il, ok := inx.Values[i].(*ast.IntLit)
		if !ok || il.Value != want {
			t.Errorf("Values[%d] = %v, want IntLit{%d}", i, inx.Values[i], want)
		}
	}
}

func TestWhereBetween(t *testing.T) {
	q := mustParse(t, "events(last 7d) | where durationMs between (100 .. 500)")

	bx, ok := asWhere(t, stageN(t, q, 0)).Expr.(*ast.BetweenExpr)
	if !ok {
		t.Fatalf("Expr: expected *BetweenExpr, got %T", asWhere(t, stageN(t, q, 0)).Expr)
	}
	if bx.Field.Name != "durationMs" {
		t.Errorf("Field = %q, want %q", bx.Field.Name, "durationMs")
	}
	lo, ok := bx.Lo.(*ast.IntLit)
	if !ok || lo.Value != 100 {
		t.Errorf("Lo = %v, want IntLit{100}", bx.Lo)
	}
	hi, ok := bx.Hi.(*ast.IntLit)
	if !ok || hi.Value != 500 {
		t.Errorf("Hi = %v, want IntLit{500}", bx.Hi)
	}
}

func TestWhereAgo(t *testing.T) {
	q := mustParse(t, "events(last 7d) | where ts > ago(1h)")

	ce := asCompare(t, asWhere(t, stageN(t, q, 0)).Expr)
	if asFieldRef(t, ce.Left).Name != "ts" {
		t.Errorf("Left.Name = %v", ce.Left)
	}
	if ce.Op != ast.CmpGt {
		t.Errorf("Op = %v, want CmpGt", ce.Op)
	}
	ago, ok := ce.Right.(*ast.AgoExpr)
	if !ok {
		t.Fatalf("Right: expected *AgoExpr, got %T", ce.Right)
	}
	if ago.Duration.Duration != time.Hour {
		t.Errorf("ago duration = %v, want 1h", ago.Duration.Duration)
	}
}

// ─── Tier 4 – Boolean structure & precedence ──────────────────────────────────

func TestBoolAnd(t *testing.T) {
	q := mustParse(t, `events(last 7d) | where level == "Error" and region == "eastus"`)

	and, ok := asWhere(t, stageN(t, q, 0)).Expr.(*ast.AndExpr)
	if !ok {
		t.Fatalf("Expr: expected *AndExpr, got %T", asWhere(t, stageN(t, q, 0)).Expr)
	}
	if asCompare(t, and.Left).Op != ast.CmpEq {
		t.Errorf("Left: expected CompareExpr(==), got %T", and.Left)
	}
	if asCompare(t, and.Right).Op != ast.CmpEq {
		t.Errorf("Right: expected CompareExpr(==), got %T", and.Right)
	}
}

func TestBoolOr(t *testing.T) {
	q := mustParse(t, `events(last 7d) | where level == "Error" or level == "Warning"`)

	or, ok := asWhere(t, stageN(t, q, 0)).Expr.(*ast.OrExpr)
	if !ok {
		t.Fatalf("Expr: expected *OrExpr, got %T", asWhere(t, stageN(t, q, 0)).Expr)
	}
	asCompare(t, or.Left)
	asCompare(t, or.Right)
}

func TestBoolNot(t *testing.T) {
	q := mustParse(t, "events(last 7d) | where not isnull(userId)")

	not, ok := asWhere(t, stageN(t, q, 0)).Expr.(*ast.NotExpr)
	if !ok {
		t.Fatalf("Expr: expected *NotExpr, got %T", asWhere(t, stageN(t, q, 0)).Expr)
	}
	if _, ok := not.Expr.(*ast.IsNullExpr); !ok {
		t.Errorf("NotExpr.Expr: expected *IsNullExpr, got %T", not.Expr)
	}
}

func TestBoolParens(t *testing.T) {
	// Parens force or to bind before and.
	q := mustParse(t, `events(last 7d) | where (level == "Error" or level == "Critical") and region == "eastus"`)

	// Outer: and
	and, ok := asWhere(t, stageN(t, q, 0)).Expr.(*ast.AndExpr)
	if !ok {
		t.Fatalf("Expr: expected *AndExpr, got %T", asWhere(t, stageN(t, q, 0)).Expr)
	}
	// Left of and: or (from the parens)
	if _, ok := and.Left.(*ast.OrExpr); !ok {
		t.Errorf("AndExpr.Left: expected *OrExpr, got %T", and.Left)
	}
	// Right of and: region == "eastus"
	asCompare(t, and.Right)
}

func TestAndBindsTighterThanOr(t *testing.T) {
	// Without parens: "a and b or c" must parse as "(a and b) or c".
	q := mustParse(t, `events(last 7d) | where level == "Error" and region == "eastus" or level == "Warning"`)

	// Outer must be or
	or, ok := asWhere(t, stageN(t, q, 0)).Expr.(*ast.OrExpr)
	if !ok {
		t.Fatalf("Expr: expected *OrExpr, got %T", asWhere(t, stageN(t, q, 0)).Expr)
	}
	// Left of or must be and
	if _, ok := or.Left.(*ast.AndExpr); !ok {
		t.Errorf("OrExpr.Left: expected *AndExpr (and binds tighter), got %T", or.Left)
	}
	// Right of or must be a comparison
	asCompare(t, or.Right)
}

// ─── Tier 5 – Summarize ───────────────────────────────────────────────────────

func TestSummarizeBareCount(t *testing.T) {
	q := mustParse(t, "events(last 7d) | summarize count()")

	ss := asSummarize(t, stageN(t, q, 0))
	if len(ss.Aggs) != 1 {
		t.Fatalf("Aggs: want 1, got %d", len(ss.Aggs))
	}
	agg := ss.Aggs[0]
	if agg.Alias != "" {
		t.Errorf("Alias = %q, want empty", agg.Alias)
	}
	if agg.Call.Func != ast.AggCount {
		t.Errorf("Func = %v, want AggCount", agg.Call.Func)
	}
	if agg.Call.Field != nil {
		t.Errorf("Field = %v, want nil", agg.Call.Field)
	}
	if len(ss.GroupBy) != 0 {
		t.Errorf("GroupBy: want empty, got %d", len(ss.GroupBy))
	}
}

func TestSummarizeCountByRegion(t *testing.T) {
	q := mustParse(t, "events(last 7d) | summarize count() by region")

	ss := asSummarize(t, stageN(t, q, 0))
	if len(ss.GroupBy) != 1 || ss.GroupBy[0].Name != "region" {
		t.Errorf("GroupBy = %v, want [{Name:region}]", ss.GroupBy)
	}
}

func TestSummarizeAliasedCountByTwoFields(t *testing.T) {
	q := mustParse(t, "events(last 7d) | summarize total = count() by region, level")

	ss := asSummarize(t, stageN(t, q, 0))
	if len(ss.Aggs) != 1 || ss.Aggs[0].Alias != "total" {
		t.Errorf("Aggs[0].Alias = %q, want %q", ss.Aggs[0].Alias, "total")
	}
	if len(ss.GroupBy) != 2 {
		t.Fatalf("GroupBy: want 2, got %d", len(ss.GroupBy))
	}
	for i, want := range []string{"region", "level"} {
		if ss.GroupBy[i].Name != want {
			t.Errorf("GroupBy[%d].Name = %q, want %q", i, ss.GroupBy[i].Name, want)
		}
	}
}

func TestSummarizeAvgByRegion(t *testing.T) {
	q := mustParse(t, "events(last 7d) | summarize avg(durationMs) by region")

	ss := asSummarize(t, stageN(t, q, 0))
	agg := ss.Aggs[0]
	if agg.Call.Func != ast.AggAvg {
		t.Errorf("Func = %v, want AggAvg", agg.Call.Func)
	}
	if agg.Call.Field == nil || agg.Call.Field.Name != "durationMs" {
		t.Errorf("Field = %v, want durationMs", agg.Call.Field)
	}
}

func TestSummarizeMultiAgg(t *testing.T) {
	q := mustParse(t, "events(last 7d) | summarize maxDur = max(durationMs), minDur = min(durationMs) by region")

	ss := asSummarize(t, stageN(t, q, 0))
	if len(ss.Aggs) != 2 {
		t.Fatalf("Aggs: want 2, got %d", len(ss.Aggs))
	}
	cases := []struct {
		alias string
		fn    ast.AggFunc
		field string
	}{
		{"maxDur", ast.AggMax, "durationMs"},
		{"minDur", ast.AggMin, "durationMs"},
	}
	for i, tc := range cases {
		agg := ss.Aggs[i]
		if agg.Alias != tc.alias {
			t.Errorf("Aggs[%d].Alias = %q, want %q", i, agg.Alias, tc.alias)
		}
		if agg.Call.Func != tc.fn {
			t.Errorf("Aggs[%d].Func = %v, want %v", i, agg.Call.Func, tc.fn)
		}
		if agg.Call.Field == nil || agg.Call.Field.Name != tc.field {
			t.Errorf("Aggs[%d].Field = %v, want %q", i, agg.Call.Field, tc.field)
		}
	}
}

func TestSummarizeDcount(t *testing.T) {
	q := mustParse(t, "events(last 7d) | summarize dcount(userId) by service")

	ss := asSummarize(t, stageN(t, q, 0))
	if ss.Aggs[0].Call.Func != ast.AggDcount {
		t.Errorf("Func = %v, want AggDcount", ss.Aggs[0].Call.Func)
	}
	if ss.Aggs[0].Call.Field == nil || ss.Aggs[0].Call.Field.Name != "userId" {
		t.Errorf("Field = %v, want userId", ss.Aggs[0].Call.Field)
	}
	if len(ss.GroupBy) != 1 || ss.GroupBy[0].Name != "service" {
		t.Errorf("GroupBy = %v, want [service]", ss.GroupBy)
	}
}

// ─── Tier 6 – Multi-stage pipelines ──────────────────────────────────────────

func TestPipelineThreeStages(t *testing.T) {
	// where → project → take
	q := mustParse(t, `events(last 7d) | where level == "Error" | project timestamp, message | take 50`)

	if len(q.Stages) != 3 {
		t.Fatalf("Stages: want 3, got %d", len(q.Stages))
	}
	asWhere(t, stageN(t, q, 0))
	asProject(t, stageN(t, q, 1))
	ts := asTake(t, stageN(t, q, 2))
	if ts.Count != 50 {
		t.Errorf("TakeStage.Count = %d, want 50", ts.Count)
	}
}

func TestPipelineWhereSortTake(t *testing.T) {
	// where → sort → take
	q := mustParse(t, `events(last 7d) | where level == "Error" | sort by timestamp desc | take 10`)

	if len(q.Stages) != 3 {
		t.Fatalf("Stages: want 3, got %d", len(q.Stages))
	}
	asWhere(t, stageN(t, q, 0))
	ss := asSort(t, stageN(t, q, 1))
	if ss.Items[0].Dir != ast.SortDesc {
		t.Errorf("sort dir = %v, want SortDesc", ss.Items[0].Dir)
	}
	ts := asTake(t, stageN(t, q, 2))
	if ts.Count != 10 {
		t.Errorf("TakeStage.Count = %d, want 10", ts.Count)
	}
}

func TestPipelineWhereSummarize(t *testing.T) {
	// where → summarize
	q := mustParse(t, `events(last 7d) | where level == "Error" and region == "eastus" | summarize count() by service`)

	if len(q.Stages) != 2 {
		t.Fatalf("Stages: want 2, got %d", len(q.Stages))
	}
	asWhere(t, stageN(t, q, 0))
	ss := asSummarize(t, stageN(t, q, 1))
	if len(ss.GroupBy) != 1 || ss.GroupBy[0].Name != "service" {
		t.Errorf("GroupBy = %v, want [service]", ss.GroupBy)
	}
}

// ─── Tier 7 – Join ────────────────────────────────────────────────────────────

func TestJoinSameNameKey(t *testing.T) {
	q := mustParse(t, "events(last 7d) | join (users(*) | project userId, name) on userId")

	js := asJoin(t, stageN(t, q, 0))

	// Sub-query
	if js.Right.Source.TypeName != "users" {
		t.Errorf("Right.Source.TypeName = %q, want %q", js.Right.Source.TypeName, "users")
	}
	if _, ok := js.Right.Source.TimeWindow.(*ast.FullScan); !ok {
		t.Errorf("Right.TimeWindow: expected *FullScan, got %T", js.Right.Source.TimeWindow)
	}
	if len(js.Right.Stages) != 1 {
		t.Fatalf("Right.Stages: want 1, got %d", len(js.Right.Stages))
	}
	asProject(t, js.Right.Stages[0])

	// Key
	if len(js.Keys) != 1 {
		t.Fatalf("Keys: want 1, got %d", len(js.Keys))
	}
	snk, ok := js.Keys[0].(*ast.SameNameKey)
	if !ok {
		t.Fatalf("Keys[0]: expected *SameNameKey, got %T", js.Keys[0])
	}
	if snk.Name != "userId" {
		t.Errorf("Keys[0].Name = %q, want %q", snk.Name, "userId")
	}
}

func TestJoinExplicitKey(t *testing.T) {
	q := mustParse(t, "events(last 7d) | join (users(*)) on $left.userId == $right.id")

	js := asJoin(t, stageN(t, q, 0))
	if len(js.Keys) != 1 {
		t.Fatalf("Keys: want 1, got %d", len(js.Keys))
	}
	ek, ok := js.Keys[0].(*ast.ExplicitKey)
	if !ok {
		t.Fatalf("Keys[0]: expected *ExplicitKey, got %T", js.Keys[0])
	}
	if ek.Left != "userId" {
		t.Errorf("Left = %q, want %q", ek.Left, "userId")
	}
	if ek.Right != "id" {
		t.Errorf("Right = %q, want %q", ek.Right, "id")
	}
}

func TestJoinMultipleKeys(t *testing.T) {
	q := mustParse(t, "events(last 7d) | join (orders(last 7d)) on userId, orderId")

	js := asJoin(t, stageN(t, q, 0))
	if len(js.Keys) != 2 {
		t.Fatalf("Keys: want 2, got %d", len(js.Keys))
	}
	for i, want := range []string{"userId", "orderId"} {
		snk, ok := js.Keys[i].(*ast.SameNameKey)
		if !ok {
			t.Fatalf("Keys[%d]: expected *SameNameKey, got %T", i, js.Keys[i])
		}
		if snk.Name != want {
			t.Errorf("Keys[%d].Name = %q, want %q", i, snk.Name, want)
		}
	}
}

// ─── Tier 8 – Error cases ─────────────────────────────────────────────────────

func TestErrorMissingTimeWindow(t *testing.T) {
	mustParseErr(t, "events", "")
}

func TestErrorUnknownStage(t *testing.T) {
	mustParseErr(t, "events(last 7d) | bogus", "stage keyword")
}

func TestErrorNegativeTake(t *testing.T) {
	// Lexer produces TokenMinus then TokenInt; parseTakeStage expects TokenInt first.
	mustParseErr(t, "events(last 7d) | take -1", "")
}

func TestErrorScalarWhereNeedsBool(t *testing.T) {
	mustParseErr(t, "events(last 7d) | where x + y", "boolean expression")
}

func TestErrorNonFieldRefLeftOfIn(t *testing.T) {
	mustParseErr(t, `events(last 7d) | where (x + y) in ("a")`, "field reference")
}
