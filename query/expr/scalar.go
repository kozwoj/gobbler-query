package expr

import (
	"github.com/kozwoj/gobbler-query/query/ast"
	"github.com/kozwoj/gobbler-query/query/batch"
	"github.com/kozwoj/gobbler-query/query/source"
)

// ScalarEval evaluates a compiled scalar expression for one row of a batch.
// Returns the typed Value for that row, or an error.
// A null result is represented as Value{Kind: KindNull}.
type ScalarEval func(b *batch.Batch, row int) (Value, error)

// CompileScalar wraps an ast.ScalarExpr into a ScalarEval.
// Column resolution and type checking happen at evaluation time.
func CompileScalar(e ast.ScalarExpr) ScalarEval {
	return func(b *batch.Batch, row int) (Value, error) {
		return evalScalar(e, b, row)
	}
}

// CompiledProjectItem describes one output column produced by a project stage.
// Name, Origin, and Type are determined by InferAndValidate before plan construction.
// The planner converts Type to the appropriate vecKind when building the ProjectOp.
type CompiledProjectItem struct {
	Name   string
	Origin string            // "" for computed columns
	Type   source.ColumnType // used to build correctly-typed all-null vectors
	Eval   ScalarEval
}
