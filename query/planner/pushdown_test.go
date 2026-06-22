package planner

import (
	"testing"
	"time"

	"github.com/kozwoj/gobbler-query/query/ast"
	"github.com/kozwoj/gobbler-query/query/logical"
)

// ─── shared fixtures ──────────────────────────────────────────────────────────

var (
	testSrc = &logical.LogicalSource{TypeName: "requests", Start: time.Time{}, End: time.Time{}}

	// anyPred is the simplest non-nil BoolExpr; its content is irrelevant to
	// the structural analysis performed by analyzePushdown.
	anyPred = &ast.IsNullExpr{Kind: ast.KindIsNull, Field: ast.FieldRef{Name: "statusCode"}}

	// pureItems selects two columns with no renaming.
	pureItems = []ast.ProjectItem{
		{Expr: &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "userId"}}},
		{Expr: &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "statusCode"}}},
	}

	// aliasItems renames a column — not a pure column-select.
	aliasItems = []ast.ProjectItem{
		{Alias: "u", Expr: &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "userId"}}},
	}
)

// nonSrcNode returns a node that is neither LogicalSource, LogicalWhere, nor
// LogicalProject — used to verify that the analysis correctly returns
// pushdownNone when the Source is not directly reachable.
func nonSrcNode() logical.LogicalNode {
	return &logical.LogicalCount{Input: testSrc}
}

// ─── analyzePushdown tests ────────────────────────────────────────────────────

func TestAnalyzePushdown(t *testing.T) {
	cases := []struct {
		name        string
		node        logical.LogicalNode
		wantKind    pushdownKind
		wantSource  bool
		wantWhere   bool
		wantProject bool
	}{
		// ── pushdownNone ────────────────────────────────────────────────────
		{
			name:     "bare source",
			node:     testSrc,
			wantKind: pushdownNone,
		},
		{
			name:     "take node",
			node:     &logical.LogicalTake{Input: testSrc, Count: 10},
			wantKind: pushdownNone,
		},
		{
			name:     "where over non-source non-project",
			node:     &logical.LogicalWhere{Input: nonSrcNode(), Pred: anyPred},
			wantKind: pushdownNone,
		},
		{
			name:     "pure project over non-source",
			node:     &logical.LogicalProject{Input: nonSrcNode(), Items: pureItems},
			wantKind: pushdownNone,
		},
		{
			name:     "aliased project over source",
			node:     &logical.LogicalProject{Input: testSrc, Items: aliasItems},
			wantKind: pushdownNone,
		},
		{
			name: "pure project over where over non-source",
			node: &logical.LogicalProject{
				Input: &logical.LogicalWhere{Input: nonSrcNode(), Pred: anyPred},
				Items: pureItems,
			},
			wantKind: pushdownNone,
		},
		{
			name: "where over pure project over non-source",
			node: &logical.LogicalWhere{
				Input: &logical.LogicalProject{Input: nonSrcNode(), Items: pureItems},
				Pred:  anyPred,
			},
			wantKind: pushdownNone,
		},
		{
			name: "aliased project over where over source",
			node: &logical.LogicalProject{
				Input: &logical.LogicalWhere{Input: testSrc, Pred: anyPred},
				Items: aliasItems,
			},
			wantKind: pushdownNone,
		},
		{
			name: "where over aliased project over source",
			node: &logical.LogicalWhere{
				Input: &logical.LogicalProject{Input: testSrc, Items: aliasItems},
				Pred:  anyPred,
			},
			wantKind: pushdownNone,
		},

		// ── pushdownPred: Where → Source ────────────────────────────────────
		{
			name:       "where over source",
			node:       &logical.LogicalWhere{Input: testSrc, Pred: anyPred},
			wantKind:   pushdownPred,
			wantSource: true,
			wantWhere:  true,
		},

		// ── pushdownCols: Project(pure) → Source ────────────────────────────
		{
			name:        "pure project over source",
			node:        &logical.LogicalProject{Input: testSrc, Items: pureItems},
			wantKind:    pushdownCols,
			wantSource:  true,
			wantProject: true,
		},
		{
			name: "single-column pure project over source",
			node: &logical.LogicalProject{
				Input: testSrc,
				Items: []ast.ProjectItem{
					{Expr: &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "userId"}}},
				},
			},
			wantKind:    pushdownCols,
			wantSource:  true,
			wantProject: true,
		},

		// ── pushdownPredThenCols: Project(pure) → Where → Source ────────────
		{
			name: "pure project over where over source",
			node: &logical.LogicalProject{
				Input: &logical.LogicalWhere{Input: testSrc, Pred: anyPred},
				Items: pureItems,
			},
			wantKind:    pushdownPredThenCols,
			wantSource:  true,
			wantWhere:   true,
			wantProject: true,
		},

		// ── pushdownColsThenPred: Where → Project(pure) → Source ────────────
		{
			name: "where over pure project over source",
			node: &logical.LogicalWhere{
				Input: &logical.LogicalProject{Input: testSrc, Items: pureItems},
				Pred:  anyPred,
			},
			wantKind:    pushdownColsThenPred,
			wantSource:  true,
			wantWhere:   true,
			wantProject: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := analyzePushdown(tc.node)

			if got.Kind != tc.wantKind {
				t.Errorf("Kind: got %v, want %v", got.Kind, tc.wantKind)
			}
			if tc.wantSource && got.Source == nil {
				t.Error("Source: expected non-nil, got nil")
			}
			if !tc.wantSource && got.Source != nil {
				t.Errorf("Source: expected nil, got %+v", got.Source)
			}
			if tc.wantWhere && got.Where == nil {
				t.Error("Where: expected non-nil, got nil")
			}
			if !tc.wantWhere && got.Where != nil {
				t.Errorf("Where: expected nil, got %+v", got.Where)
			}
			if tc.wantProject && got.Project == nil {
				t.Error("Project: expected non-nil, got nil")
			}
			if !tc.wantProject && got.Project != nil {
				t.Errorf("Project: expected nil, got %+v", got.Project)
			}
		})
	}
}

// ─── isPureColumnSelect tests ─────────────────────────────────────────────────

func TestIsPureColumnSelect(t *testing.T) {
	cases := []struct {
		name  string
		items []ast.ProjectItem
		want  bool
	}{
		{
			name:  "nil items",
			items: nil,
			want:  true,
		},
		{
			name:  "empty items",
			items: []ast.ProjectItem{},
			want:  true,
		},
		{
			name:  "single bare ref",
			items: []ast.ProjectItem{{Expr: &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "x"}}}},
			want:  true,
		},
		{
			name:  "multiple bare refs",
			items: pureItems,
			want:  true,
		},
		{
			name:  "aliased ref",
			items: aliasItems,
			want:  false,
		},
		{
			name: "mixed pure and aliased",
			items: []ast.ProjectItem{
				{Expr: &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "x"}}},
				{Alias: "y", Expr: &ast.FieldRefExpr{Ref: ast.FieldRef{Name: "z"}}},
			},
			want: false,
		},
		{
			name: "non-FieldRef expr with no alias",
			items: []ast.ProjectItem{
				{Expr: &ast.IntLit{Value: 1}},
			},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPureColumnSelect(tc.items); got != tc.want {
				t.Errorf("isPureColumnSelect: got %v, want %v", got, tc.want)
			}
		})
	}
}
