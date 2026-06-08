package logical

import (
	"testing"
	"time"

	"github.com/kozwoj/gobbler-query/query/ast"
	"github.com/kozwoj/gobbler-query/query/source"
)

// ─── test helpers ─────────────────────────────────────────────────────────────

func mkSchema(cols ...source.ColumnSchema) *source.Schema {
	return &source.Schema{Columns: cols}
}

func mkCol(name string, t source.ColumnType) source.ColumnSchema {
	return source.ColumnSchema{Name: name, Type: t}
}

// testSchemas is a shared catalog of source schemas used across all Stage-1 tests.
var testSchemas = map[string]*source.Schema{
	"requests": mkSchema(
		mkCol("timestamp", source.TypeDatetime),
		mkCol("statusCode", source.TypeInt32),
		mkCol("region", source.TypeString),
		mkCol("durationMs", source.TypeFloat64),
	),
	"users": mkSchema(
		mkCol("timestamp", source.TypeDatetime),
		mkCol("userId", source.TypeString),
		mkCol("tier", source.TypeString),
	),
}

func src(typeName string) *LogicalSource {
	return &LogicalSource{TypeName: typeName}
}

// ─── Source ───────────────────────────────────────────────────────────────────

func TestInferAndValidate_Source_Known(t *testing.T) {
	meta, err := InferAndValidate(src("requests"), testSchemas)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta) != 4 {
		t.Fatalf("len(meta) = %d, want 4", len(meta))
	}
	if meta[1].Name != "statusCode" || meta[1].Origin != "requests" {
		t.Errorf("meta[1] = %+v, want {statusCode requests}", meta[1])
	}
}

func TestInferAndValidate_Source_Unknown(t *testing.T) {
	_, err := InferAndValidate(src("unknown"), testSchemas)
	if err == nil {
		t.Fatal("expected error for unknown table, got nil")
	}
}

// ─── Take ─────────────────────────────────────────────────────────────────────

func TestInferAndValidate_Take_PassThrough(t *testing.T) {
	node := &LogicalTake{Input: src("requests"), Count: 10}
	meta, err := InferAndValidate(node, testSchemas)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta) != 4 {
		t.Fatalf("len(meta) = %d, want 4 (same as source)", len(meta))
	}
}

// ─── Count ────────────────────────────────────────────────────────────────────

func TestInferAndValidate_Count_OutputSchema(t *testing.T) {
	node := &LogicalCount{Input: src("requests")}
	meta, err := InferAndValidate(node, testSchemas)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta) != 1 || meta[0].Name != "count_" || meta[0].Origin != "" {
		t.Fatalf("meta = %+v, want [{count_ }]", meta)
	}
}

func TestInferAndValidate_Count_UnknownSource_Propagates(t *testing.T) {
	node := &LogicalCount{Input: src("unknown")}
	_, err := InferAndValidate(node, testSchemas)
	if err == nil {
		t.Fatal("expected error for unknown source inside count, got nil")
	}
}

// ─── Sort ─────────────────────────────────────────────────────────────────────

func TestInferAndValidate_Sort_ValidField(t *testing.T) {
	node := &LogicalSort{
		Input: src("requests"),
		Items: []ast.SortItem{{Field: ast.FieldRef{Name: "timestamp"}, Dir: ast.SortDesc}},
	}
	_, err := InferAndValidate(node, testSchemas)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInferAndValidate_Sort_MissingField(t *testing.T) {
	node := &LogicalSort{
		Input: src("requests"),
		Items: []ast.SortItem{{Field: ast.FieldRef{Name: "foo"}}},
	}
	_, err := InferAndValidate(node, testSchemas)
	if err == nil {
		t.Fatal("expected error for unknown sort field, got nil")
	}
}

func TestInferAndValidate_Sort_PassThroughSchema(t *testing.T) {
	node := &LogicalSort{
		Input: src("requests"),
		Items: []ast.SortItem{{Field: ast.FieldRef{Name: "statusCode"}}},
	}
	meta, err := InferAndValidate(node, testSchemas)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta) != 4 {
		t.Fatalf("len(meta) = %d, want 4 (sort does not change schema)", len(meta))
	}
}

// ─── Where — field existence ──────────────────────────────────────────────────

func TestInferAndValidate_Where_MissingField(t *testing.T) {
	node := &LogicalWhere{
		Input: src("requests"),
		Pred: &ast.CompareExpr{
			Left:  &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "foo"}},
			Op:    ast.CmpEq,
			Right: &ast.IntLit{Value: 1},
		},
	}
	_, err := InferAndValidate(node, testSchemas)
	if err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
}

func TestInferAndValidate_Where_IsNull_MissingField(t *testing.T) {
	node := &LogicalWhere{
		Input: src("requests"),
		Pred:  &ast.IsNullExpr{Kind: ast.KindIsNull, Field: ast.FieldRef{Name: "foo"}},
	}
	_, err := InferAndValidate(node, testSchemas)
	if err == nil {
		t.Fatal("expected error for unknown field in isnull, got nil")
	}
}

// ─── Where — type compatibility ───────────────────────────────────────────────

func TestInferAndValidate_Where_IntGteIntLit(t *testing.T) {
	node := &LogicalWhere{
		Input: src("requests"),
		Pred: &ast.CompareExpr{
			Left:  &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "statusCode"}},
			Op:    ast.CmpGtEq,
			Right: &ast.IntLit{Value: 400},
		},
	}
	_, err := InferAndValidate(node, testSchemas)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInferAndValidate_Where_StringVsInt_IsError(t *testing.T) {
	// region is TypeString; comparing with IntLit is a type mismatch.
	node := &LogicalWhere{
		Input: src("requests"),
		Pred: &ast.CompareExpr{
			Left:  &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "region"}},
			Op:    ast.CmpGtEq,
			Right: &ast.IntLit{Value: 400},
		},
	}
	_, err := InferAndValidate(node, testSchemas)
	if err == nil {
		t.Fatal("expected type mismatch error, got nil")
	}
}

func TestInferAndValidate_Where_StringContains(t *testing.T) {
	node := &LogicalWhere{
		Input: src("requests"),
		Pred: &ast.CompareExpr{
			Left:  &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "region"}},
			Op:    ast.CmpContains,
			Right: &ast.StringLit{Value: "east"},
		},
	}
	_, err := InferAndValidate(node, testSchemas)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInferAndValidate_Where_ContainsOnNumeric_IsError(t *testing.T) {
	node := &LogicalWhere{
		Input: src("requests"),
		Pred: &ast.CompareExpr{
			Left:  &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "statusCode"}},
			Op:    ast.CmpContains,
			Right: &ast.StringLit{Value: "4"},
		},
	}
	_, err := InferAndValidate(node, testSchemas)
	if err == nil {
		t.Fatal("expected error for contains on numeric column, got nil")
	}
}

func TestInferAndValidate_Where_DatetimeWithAgo(t *testing.T) {
	node := &LogicalWhere{
		Input: src("requests"),
		Pred: &ast.CompareExpr{
			Left:  &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "timestamp"}},
			Op:    ast.CmpGt,
			Right: &ast.AgoExpr{Duration: ast.TimespanLit{Duration: time.Hour}},
		},
	}
	_, err := InferAndValidate(node, testSchemas)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInferAndValidate_Where_AndExpr(t *testing.T) {
	node := &LogicalWhere{
		Input: src("requests"),
		Pred: &ast.AndExpr{
			Left: &ast.CompareExpr{
				Left:  &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "statusCode"}},
				Op:    ast.CmpGtEq,
				Right: &ast.IntLit{Value: 400},
			},
			Right: &ast.CompareExpr{
				Left:  &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "region"}},
				Op:    ast.CmpEq,
				Right: &ast.StringLit{Value: "eastus"},
			},
		},
	}
	_, err := InferAndValidate(node, testSchemas)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInferAndValidate_Where_AndExpr_RightBranchError(t *testing.T) {
	// Right branch references a non-existent field — error must propagate.
	node := &LogicalWhere{
		Input: src("requests"),
		Pred: &ast.AndExpr{
			Left: &ast.CompareExpr{
				Left:  &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "statusCode"}},
				Op:    ast.CmpGtEq,
				Right: &ast.IntLit{Value: 400},
			},
			Right: &ast.CompareExpr{
				Left:  &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "missing"}},
				Op:    ast.CmpEq,
				Right: &ast.StringLit{Value: "x"},
			},
		},
	}
	_, err := InferAndValidate(node, testSchemas)
	if err == nil {
		t.Fatal("expected error for missing field in right branch, got nil")
	}
}

func TestInferAndValidate_Where_IsNull_ValidField(t *testing.T) {
	node := &LogicalWhere{
		Input: src("requests"),
		Pred:  &ast.IsNullExpr{Kind: ast.KindIsNull, Field: ast.FieldRef{Name: "region"}},
	}
	_, err := InferAndValidate(node, testSchemas)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ─── Project ─────────────────────────────────────────────────────────────────

func TestInferAndValidate_Project_BareRef(t *testing.T) {
	node := &LogicalProject{
		Input: src("requests"),
		Items: []ast.ProjectItem{
			{Expr: &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "timestamp"}}},
			{Expr: &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "statusCode"}}},
		},
	}
	meta, err := InferAndValidate(node, testSchemas)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta) != 2 {
		t.Fatalf("len = %d, want 2", len(meta))
	}
	if meta[0].Name != "timestamp" || meta[0].Origin != "requests" {
		t.Errorf("meta[0] = %+v, want {timestamp requests}", meta[0])
	}
	if meta[1].Name != "statusCode" || meta[1].Origin != "requests" {
		t.Errorf("meta[1] = %+v, want {statusCode requests}", meta[1])
	}
}

func TestInferAndValidate_Project_BareRef_Missing(t *testing.T) {
	node := &LogicalProject{
		Input: src("requests"),
		Items: []ast.ProjectItem{
			{Expr: &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "missing"}}},
		},
	}
	_, err := InferAndValidate(node, testSchemas)
	if err == nil {
		t.Fatal("expected error for missing field, got nil")
	}
}

func TestInferAndValidate_Project_Rename(t *testing.T) {
	node := &LogicalProject{
		Input: src("requests"),
		Items: []ast.ProjectItem{
			{Alias: "ts", Expr: &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "timestamp"}}},
		},
	}
	meta, err := InferAndValidate(node, testSchemas)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta) != 1 {
		t.Fatalf("len = %d, want 1", len(meta))
	}
	// Rename preserves origin.
	if meta[0].Name != "ts" || meta[0].Origin != "requests" {
		t.Errorf("meta[0] = %+v, want {ts requests}", meta[0])
	}
}

func TestInferAndValidate_Project_Rename_Missing(t *testing.T) {
	node := &LogicalProject{
		Input: src("requests"),
		Items: []ast.ProjectItem{
			{Alias: "x", Expr: &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "missing"}}},
		},
	}
	_, err := InferAndValidate(node, testSchemas)
	if err == nil {
		t.Fatal("expected error for missing field in rename, got nil")
	}
}

func TestInferAndValidate_Project_Compute_OutputName(t *testing.T) {
	// dur = timestamp - timestamp → timespan; computed col gets Origin="".
	node := &LogicalProject{
		Input: src("requests"),
		Items: []ast.ProjectItem{
			{Alias: "dur", Expr: &ast.BinaryExpr{
				Left:  &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "timestamp"}},
				Op:    ast.BinSub,
				Right: &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "timestamp"}},
			}},
		},
	}
	meta, err := InferAndValidate(node, testSchemas)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta) != 1 {
		t.Fatalf("len = %d, want 1", len(meta))
	}
	if meta[0].Name != "dur" || meta[0].Origin != "" {
		t.Errorf("meta[0] = %+v, want {dur }", meta[0])
	}
}

func TestInferAndValidate_Project_Compute_TypeMismatch(t *testing.T) {
	// region (string) - statusCode (int) → no type rule → error.
	node := &LogicalProject{
		Input: src("requests"),
		Items: []ast.ProjectItem{
			{Alias: "x", Expr: &ast.BinaryExpr{
				Left:  &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "region"}},
				Op:    ast.BinSub,
				Right: &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "statusCode"}},
			}},
		},
	}
	_, err := InferAndValidate(node, testSchemas)
	if err == nil {
		t.Fatal("expected type error for string - int, got nil")
	}
}

func TestInferAndValidate_Project_PureLiteral(t *testing.T) {
	node := &LogicalProject{
		Input: src("requests"),
		Items: []ast.ProjectItem{
			{Alias: "n", Expr: &ast.IntLit{Value: 42}},
		},
	}
	_, err := InferAndValidate(node, testSchemas)
	if err == nil {
		t.Fatal("expected error for pure literal project item, got nil")
	}
}

func TestInferAndValidate_Project_Compute_MissingField(t *testing.T) {
	node := &LogicalProject{
		Input: src("requests"),
		Items: []ast.ProjectItem{
			{Alias: "x", Expr: &ast.BinaryExpr{
				Left:  &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "missing"}},
				Op:    ast.BinAdd,
				Right: &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "statusCode"}},
			}},
		},
	}
	_, err := InferAndValidate(node, testSchemas)
	if err == nil {
		t.Fatal("expected error for missing field in compute expression, got nil")
	}
}

func TestInferAndValidate_Project_ChainedWithWhere(t *testing.T) {
	// project ts = timestamp | where ts > ago(1h)  — ts is in projected schema.
	proj := &LogicalProject{
		Input: src("requests"),
		Items: []ast.ProjectItem{
			{Alias: "ts", Expr: &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "timestamp"}}},
		},
	}
	node := &LogicalWhere{
		Input: proj,
		Pred: &ast.CompareExpr{
			Left:  &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "ts"}},
			Op:    ast.CmpGt,
			Right: &ast.AgoExpr{Duration: ast.TimespanLit{Duration: time.Hour}},
		},
	}
	_, err := InferAndValidate(node, testSchemas)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInferAndValidate_Project_ChainedWhere_DroppedField(t *testing.T) {
	// After project timestamp, statusCode is dropped; where on statusCode must fail.
	proj := &LogicalProject{
		Input: src("requests"),
		Items: []ast.ProjectItem{
			{Expr: &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "timestamp"}}},
		},
	}
	node := &LogicalWhere{
		Input: proj,
		Pred: &ast.CompareExpr{
			Left:  &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "statusCode"}},
			Op:    ast.CmpGtEq,
			Right: &ast.IntLit{Value: 400},
		},
	}
	_, err := InferAndValidate(node, testSchemas)
	if err == nil {
		t.Fatal("expected error for field dropped by project, got nil")
	}
}

// ─── Summarize ────────────────────────────────────────────────────────────────

func aggCount(alias string) ast.AggItem {
	return ast.AggItem{Alias: alias, Call: ast.AggCall{Func: ast.AggCount}}
}

func aggField(fn ast.AggFunc, alias, field string) ast.AggItem {
	ref := ast.FieldRef{Name: field}
	return ast.AggItem{Alias: alias, Call: ast.AggCall{Func: fn, Field: &ref}}
}

func TestInferAndValidate_Summarize_Count_DefaultName(t *testing.T) {
	node := &LogicalSummarize{
		Input: src("requests"),
		Aggs:  []ast.AggItem{aggCount("")},
	}
	meta, err := InferAndValidate(node, testSchemas)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta) != 1 || meta[0].Name != "count_" || meta[0].Origin != "" {
		t.Errorf("meta = %+v, want [{count_ }]", meta)
	}
}

func TestInferAndValidate_Summarize_Count_WithAlias(t *testing.T) {
	node := &LogicalSummarize{
		Input: src("requests"),
		Aggs:  []ast.AggItem{aggCount("total")},
	}
	meta, err := InferAndValidate(node, testSchemas)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta) != 1 || meta[0].Name != "total" {
		t.Errorf("meta = %+v, want [{total }]", meta)
	}
}

func TestInferAndValidate_Summarize_Sum_Numeric(t *testing.T) {
	node := &LogicalSummarize{
		Input: src("requests"),
		Aggs:  []ast.AggItem{aggField(ast.AggSum, "", "statusCode")},
	}
	meta, err := InferAndValidate(node, testSchemas)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(meta) != 1 || meta[0].Name != "sum_statusCode" || meta[0].Origin != "" {
		t.Errorf("meta = %+v, want [{sum_statusCode }]", meta)
	}
}

func TestInferAndValidate_Summarize_Sum_String_IsError(t *testing.T) {
	node := &LogicalSummarize{
		Input: src("requests"),
		Aggs:  []ast.AggItem{aggField(ast.AggSum, "", "region")},
	}
	_, err := InferAndValidate(node, testSchemas)
	if err == nil {
		t.Fatal("expected error for sum(string), got nil")
	}
}

func TestInferAndValidate_Summarize_Avg_OutputIsFloat64(t *testing.T) {
	node := &LogicalSummarize{
		Input: src("requests"),
		Aggs:  []ast.AggItem{aggField(ast.AggAvg, "mean", "durationMs")},
	}
	meta, err := InferAndValidate(node, testSchemas)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(meta) != 1 || meta[0].Name != "mean" {
		t.Errorf("meta = %+v, want [{mean }]", meta)
	}
}

func TestInferAndValidate_Summarize_Min_Datetime(t *testing.T) {
	node := &LogicalSummarize{
		Input: src("requests"),
		Aggs:  []ast.AggItem{aggField(ast.AggMin, "", "timestamp")},
	}
	_, err := InferAndValidate(node, testSchemas)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInferAndValidate_Summarize_Max_String_IsError(t *testing.T) {
	node := &LogicalSummarize{
		Input: src("requests"),
		Aggs:  []ast.AggItem{aggField(ast.AggMax, "", "region")},
	}
	_, err := InferAndValidate(node, testSchemas)
	if err == nil {
		t.Fatal("expected error for max(string), got nil")
	}
}

func TestInferAndValidate_Summarize_Dcount_AnyType(t *testing.T) {
	node := &LogicalSummarize{
		Input: src("requests"),
		Aggs:  []ast.AggItem{aggField(ast.AggDcount, "", "region")},
	}
	meta, err := InferAndValidate(node, testSchemas)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(meta) != 1 || meta[0].Name != "dcount_region" {
		t.Errorf("meta = %+v, want [{dcount_region }]", meta)
	}
}

func TestInferAndValidate_Summarize_MissingField_IsError(t *testing.T) {
	node := &LogicalSummarize{
		Input: src("requests"),
		Aggs:  []ast.AggItem{aggField(ast.AggSum, "", "missing")},
	}
	_, err := InferAndValidate(node, testSchemas)
	if err == nil {
		t.Fatal("expected error for unknown field in agg, got nil")
	}
}

func TestInferAndValidate_Summarize_GroupBy_Valid(t *testing.T) {
	node := &LogicalSummarize{
		Input:   src("requests"),
		Aggs:    []ast.AggItem{aggCount("")},
		GroupBy: []ast.FieldRef{{Name: "region"}, {Name: "statusCode"}},
	}
	meta, err := InferAndValidate(node, testSchemas)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// count_ + region + statusCode
	if len(meta) != 3 {
		t.Fatalf("len(meta) = %d, want 3", len(meta))
	}
	if meta[1].Name != "region" || meta[2].Name != "statusCode" {
		t.Errorf("group-by columns wrong: %+v", meta[1:])
	}
}

func TestInferAndValidate_Summarize_GroupBy_MissingField_IsError(t *testing.T) {
	node := &LogicalSummarize{
		Input:   src("requests"),
		Aggs:    []ast.AggItem{aggCount("")},
		GroupBy: []ast.FieldRef{{Name: "missing"}},
	}
	_, err := InferAndValidate(node, testSchemas)
	if err == nil {
		t.Fatal("expected error for unknown group-by field, got nil")
	}
}

// ─── Join ─────────────────────────────────────────────────────────────────────

func TestInferAndValidate_Join_SameNameKey_Valid(t *testing.T) {
	// requests (timestamp, statusCode, region, durationMs)
	// join users (timestamp, userId, tier) on userId → userId not in requests!
	// Use a sub-query that projects userId first.
	// Actually: join users on a key that exists in both. Let's add userId to a
	// projected requests and join against users.
	// Simpler: test with two sources that share a common key name.
	// requests has no userId; use testSchemas "users" on right and a projected
	// requests that has a userId column — but that gets complex.
	// Instead, add a "logins" schema sharing userId with users.
	logins := map[string]*source.Schema{
		"logins": mkSchema(
			mkCol("timestamp", source.TypeDatetime),
			mkCol("userId", source.TypeString),
			mkCol("action", source.TypeString),
		),
		"users": mkSchema(
			mkCol("timestamp", source.TypeDatetime),
			mkCol("userId", source.TypeString),
			mkCol("tier", source.TypeString),
		),
	}
	node := &LogicalJoin{
		Left:  &LogicalSource{TypeName: "logins"},
		Right: &LogicalSource{TypeName: "users"},
		Keys:  []ast.JoinKey{&ast.SameNameKey{Name: "userId"}},
	}
	meta, err := InferAndValidate(node, logins)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// logins: timestamp, userId, action (3 cols)
	// users:  timestamp, userId, tier (3 cols) — userId deduped
	// output: timestamp(logins), userId(logins), action, timestamp(users), tier = 5 cols
	if len(meta) != 5 {
		t.Fatalf("len(meta) = %d, want 5; meta = %+v", len(meta), meta)
	}
}

func TestInferAndValidate_Join_SameNameKey_MissingInLeft_IsError(t *testing.T) {
	schemas := map[string]*source.Schema{
		"requests": mkSchema(mkCol("timestamp", source.TypeDatetime), mkCol("statusCode", source.TypeInt32)),
		"users":    mkSchema(mkCol("timestamp", source.TypeDatetime), mkCol("userId", source.TypeString)),
	}
	node := &LogicalJoin{
		Left:  &LogicalSource{TypeName: "requests"},
		Right: &LogicalSource{TypeName: "users"},
		Keys:  []ast.JoinKey{&ast.SameNameKey{Name: "userId"}},
	}
	_, err := InferAndValidate(node, schemas)
	if err == nil {
		t.Fatal("expected error for key missing from left schema, got nil")
	}
}

func TestInferAndValidate_Join_SameNameKey_MissingInRight_IsError(t *testing.T) {
	schemas := map[string]*source.Schema{
		"requests": mkSchema(mkCol("timestamp", source.TypeDatetime), mkCol("userId", source.TypeString)),
		"users":    mkSchema(mkCol("timestamp", source.TypeDatetime), mkCol("tier", source.TypeString)),
	}
	node := &LogicalJoin{
		Left:  &LogicalSource{TypeName: "requests"},
		Right: &LogicalSource{TypeName: "users"},
		Keys:  []ast.JoinKey{&ast.SameNameKey{Name: "userId"}},
	}
	_, err := InferAndValidate(node, schemas)
	if err == nil {
		t.Fatal("expected error for key missing from right schema, got nil")
	}
}

func TestInferAndValidate_Join_SameNameKey_TypeMismatch_IsError(t *testing.T) {
	schemas := map[string]*source.Schema{
		"left":  mkSchema(mkCol("id", source.TypeInt32)),
		"right": mkSchema(mkCol("id", source.TypeString)),
	}
	node := &LogicalJoin{
		Left:  &LogicalSource{TypeName: "left"},
		Right: &LogicalSource{TypeName: "right"},
		Keys:  []ast.JoinKey{&ast.SameNameKey{Name: "id"}},
	}
	_, err := InferAndValidate(node, schemas)
	if err == nil {
		t.Fatal("expected type mismatch error for join key, got nil")
	}
}

func TestInferAndValidate_Join_ExplicitKey_Valid(t *testing.T) {
	schemas := map[string]*source.Schema{
		"orders": mkSchema(mkCol("customerId", source.TypeString), mkCol("amount", source.TypeFloat64)),
		"users":  mkSchema(mkCol("userId", source.TypeString), mkCol("tier", source.TypeString)),
	}
	node := &LogicalJoin{
		Left:  &LogicalSource{TypeName: "orders"},
		Right: &LogicalSource{TypeName: "users"},
		Keys:  []ast.JoinKey{&ast.ExplicitKey{Left: "customerId", Right: "userId"}},
	}
	meta, err := InferAndValidate(node, schemas)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// orders: customerId, amount (2) + users: userId, tier (2) — no dedup for explicit key
	if len(meta) != 4 {
		t.Fatalf("len(meta) = %d, want 4", len(meta))
	}
}

func TestInferAndValidate_Join_ExplicitKey_LeftMissing_IsError(t *testing.T) {
	schemas := map[string]*source.Schema{
		"orders": mkSchema(mkCol("amount", source.TypeFloat64)),
		"users":  mkSchema(mkCol("userId", source.TypeString)),
	}
	node := &LogicalJoin{
		Left:  &LogicalSource{TypeName: "orders"},
		Right: &LogicalSource{TypeName: "users"},
		Keys:  []ast.JoinKey{&ast.ExplicitKey{Left: "customerId", Right: "userId"}},
	}
	_, err := InferAndValidate(node, schemas)
	if err == nil {
		t.Fatal("expected error for missing left key column, got nil")
	}
}

func TestInferAndValidate_Join_ExplicitKey_RightMissing_IsError(t *testing.T) {
	schemas := map[string]*source.Schema{
		"orders": mkSchema(mkCol("customerId", source.TypeString)),
		"users":  mkSchema(mkCol("tier", source.TypeString)),
	}
	node := &LogicalJoin{
		Left:  &LogicalSource{TypeName: "orders"},
		Right: &LogicalSource{TypeName: "users"},
		Keys:  []ast.JoinKey{&ast.ExplicitKey{Left: "customerId", Right: "userId"}},
	}
	_, err := InferAndValidate(node, schemas)
	if err == nil {
		t.Fatal("expected error for missing right key column, got nil")
	}
}

func TestInferAndValidate_Join_ExplicitKey_TypeMismatch_IsError(t *testing.T) {
	schemas := map[string]*source.Schema{
		"orders": mkSchema(mkCol("customerId", source.TypeInt32)),
		"users":  mkSchema(mkCol("userId", source.TypeString)),
	}
	node := &LogicalJoin{
		Left:  &LogicalSource{TypeName: "orders"},
		Right: &LogicalSource{TypeName: "users"},
		Keys:  []ast.JoinKey{&ast.ExplicitKey{Left: "customerId", Right: "userId"}},
	}
	_, err := InferAndValidate(node, schemas)
	if err == nil {
		t.Fatal("expected type mismatch error for explicit key, got nil")
	}
}
