package planner

import (
	"fmt"

	"github.com/kozwoj/gobbler-query/query/ast"
	"github.com/kozwoj/gobbler-query/query/logical"
)

// pushdownKind classifies the source-adjacent logical pattern that can be
// folded into a single source.ReaderOptions call, eliminating one or more
// physical operators.
type pushdownKind int

const (
	pushdownNone         pushdownKind = iota // no recognised pushdown pattern
	pushdownPred                             // LogicalWhere → LogicalSource
	pushdownCols                             // pure-column-select LogicalProject → LogicalSource
	pushdownPredThenCols                     // pure-column-select LogicalProject → LogicalWhere → LogicalSource
	pushdownColsThenPred                     // LogicalWhere → pure-column-select LogicalProject → LogicalSource
)

func (k pushdownKind) String() string {
	switch k {
	case pushdownNone:
		return "pushdownNone"
	case pushdownPred:
		return "pushdownPred"
	case pushdownCols:
		return "pushdownCols"
	case pushdownPredThenCols:
		return "pushdownPredThenCols"
	case pushdownColsThenPred:
		return "pushdownColsThenPred"
	default:
		return fmt.Sprintf("pushdownKind(%d)", int(k))
	}
}

// pushdownPlan is the result of analyzePushdown. It carries the matched AST
// nodes without compiling any expressions; compilation is deferred to the
// build steps (Steps 7–10) that act on the result.
type pushdownPlan struct {
	Kind    pushdownKind
	Source  *logical.LogicalSource  // always set when Kind != pushdownNone
	Where   *logical.LogicalWhere   // set for pushdownPred, pushdownPredThenCols, pushdownColsThenPred
	Project *logical.LogicalProject // set for pushdownCols, pushdownPredThenCols, pushdownColsThenPred
}

// analyzePushdown inspects the logical subtree rooted at n and returns a
// pushdownPlan classifying which source-reader pushdown optimisation applies.
// It is a pure structural analysis — no expressions are compiled and no errors
// are returned. Steps 7–10 of the planner call this and dispatch on Kind.
func analyzePushdown(n logical.LogicalNode) pushdownPlan {
	switch outer := n.(type) {

	case *logical.LogicalWhere:
		switch inner := outer.Input.(type) {
		case *logical.LogicalSource:
			// Where → Source
			return pushdownPlan{Kind: pushdownPred, Source: inner, Where: outer}

		case *logical.LogicalProject:
			// Where → Project → Source  (only if project is a pure column-select)
			if isPureColumnSelect(inner.Items) {
				if src, ok := inner.Input.(*logical.LogicalSource); ok {
					return pushdownPlan{
						Kind:    pushdownColsThenPred,
						Source:  src,
						Where:   outer,
						Project: inner,
					}
				}
			}
		}

	case *logical.LogicalProject:
		switch inner := outer.Input.(type) {
		case *logical.LogicalSource:
			// Project → Source  (only if project is a pure column-select)
			if isPureColumnSelect(outer.Items) {
				return pushdownPlan{Kind: pushdownCols, Source: inner, Project: outer}
			}

		case *logical.LogicalWhere:
			// Project → Where → Source  (only if project is a pure column-select)
			if isPureColumnSelect(outer.Items) {
				if src, ok := inner.Input.(*logical.LogicalSource); ok {
					return pushdownPlan{
						Kind:    pushdownPredThenCols,
						Source:  src,
						Where:   inner,
						Project: outer,
					}
				}
			}
		}
	}

	return pushdownPlan{Kind: pushdownNone}
}

// isPureColumnSelect reports whether every project item is a bare FieldRefExpr
// with no alias. Such a project selects a subset of input columns without
// renaming or computing any of them, which makes pushdown safe.
func isPureColumnSelect(items []ast.ProjectItem) bool {
	for _, item := range items {
		if item.Alias != "" {
			return false
		}
		if _, ok := item.Expr.(*ast.FieldRefExpr); !ok {
			return false
		}
	}
	return true
}
