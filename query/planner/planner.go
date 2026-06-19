package planner

import (
	"fmt"
	"time"

	"github.com/kozwoj/gobbler-query/query/ast"
	"github.com/kozwoj/gobbler-query/query/batch"
	"github.com/kozwoj/gobbler-query/query/catalog"
	"github.com/kozwoj/gobbler-query/query/expr"
	"github.com/kozwoj/gobbler-query/query/logical"
	"github.com/kozwoj/gobbler-query/query/physical"
	"github.com/kozwoj/gobbler-query/query/source"
)

const defaultBatchSize = 512

// BuildPhysical translates a validated logical plan into a physical operator
// tree ready to execute. Arguments must contain a source.Schema for every table
// referenced in the plan (they are used here for column-type resolution when
// building project and summarize operators).
//
// The caller must call InferAndValidate before BuildPhysical; no additional
// schema validation is performed here.
func BuildPhysical(
	node logical.LogicalNode,
	cat catalog.Catalog,
	schemas map[string]*source.Schema,
	batchSize int,
) (physical.Operator, error) {
	if batchSize <= 0 {
		batchSize = defaultBatchSize
	}
	p := &planner{cat: cat, schemas: schemas, batchSize: batchSize}
	return p.build(node)
}

type planner struct {
	cat       catalog.Catalog
	schemas   map[string]*source.Schema
	batchSize int
}

func (p *planner) build(node logical.LogicalNode) (physical.Operator, error) {
	switch n := node.(type) {
	case *logical.LogicalSource:
		return p.buildSource(n)
	case *logical.LogicalWhere:
		return p.buildWhere(n)
	case *logical.LogicalProject:
		return p.buildProject(n)
	case *logical.LogicalSummarize:
		return p.buildSummarize(n)
	case *logical.LogicalSort:
		return p.buildSort(n)
	case *logical.LogicalTake:
		return p.buildTake(n)
	case *logical.LogicalCount:
		return p.buildCount(n)
	case *logical.LogicalJoin:
		return p.buildJoin(n)
	default:
		return nil, fmt.Errorf("planner: unknown node type %T", node)
	}
}

// ─── Source ───────────────────────────────────────────────────────────────────

func (p *planner) buildSource(n *logical.LogicalSource) (physical.Operator, error) {
	entry, ok := p.cat[n.TypeName]
	if !ok {
		return nil, fmt.Errorf("planner: table %q not in catalog", n.TypeName)
	}
	reader, err := source.NewTableReader(entry, n.Start, n.End, p.batchSize)
	if err != nil {
		return nil, fmt.Errorf("planner: open %q: %w", n.TypeName, err)
	}
	return &physical.SourceOp{Reader: reader}, nil
}

// ─── Where ────────────────────────────────────────────────────────────────────

func (p *planner) buildWhere(n *logical.LogicalWhere) (physical.Operator, error) {
	input, err := p.build(n.Input)
	if err != nil {
		return nil, err
	}
	pred, err := expr.Compile(n.Pred)
	if err != nil {
		return nil, fmt.Errorf("planner: where: %w", err)
	}
	return &physical.FilterOp{Input: input, Pred: pred}, nil
}

// ─── Project ──────────────────────────────────────────────────────────────────

func (p *planner) buildProject(n *logical.LogicalProject) (physical.Operator, error) {
	// Re-run per-item type inference to get output types for CompiledProjectItem.
	// InferAndValidate already validated the plan, so errors here are unexpected.
	inputSchema, err := p.inferTypedSchema(n.Input)
	if err != nil {
		return nil, err
	}

	input, err := p.build(n.Input)
	if err != nil {
		return nil, err
	}

	items := make([]expr.CompiledProjectItem, len(n.Items))
	for i, item := range n.Items {
		name := item.Alias
		origin := ""
		var ct source.ColumnType

		if item.Alias == "" {
			// Bare field reference — name and type come from the input column.
			ref := item.Expr.(*ast.FieldRefExpr).Ref
			col, err := findInTypedSchema(inputSchema, ref)
			if err != nil {
				return nil, fmt.Errorf("planner: project: %w", err)
			}
			name = col.Name
			origin = col.Origin
			ct = col.Type
		} else {
			ct, err = typeOfExpr(item.Expr, inputSchema)
			if err != nil {
				return nil, fmt.Errorf("planner: project %q: %w", item.Alias, err)
			}
			// Preserve origin for simple renames.
			if fre, ok := item.Expr.(*ast.FieldRefExpr); ok {
				if col, err := findInTypedSchema(inputSchema, fre.Ref); err == nil {
					origin = col.Origin
				}
			}
		}

		items[i] = expr.CompiledProjectItem{
			Name:   name,
			Origin: origin,
			Type:   ct,
			Eval:   expr.CompileScalar(item.Expr),
		}
	}
	return &physical.ProjectOp{Input: input, Items: items}, nil
}

// ─── Summarize ────────────────────────────────────────────────────────────────

func (p *planner) buildSummarize(n *logical.LogicalSummarize) (physical.Operator, error) {
	inputSchema, err := p.inferTypedSchema(n.Input)
	if err != nil {
		return nil, err
	}

	input, err := p.build(n.Input)
	if err != nil {
		return nil, err
	}

	// Build agg items — output types are re-derived the same way as InferAndValidate.
	aggs := make([]expr.CompiledAggItem, len(n.Aggs))
	for i, item := range n.Aggs {
		outType, err := aggOutputType(item, inputSchema)
		if err != nil {
			return nil, fmt.Errorf("planner: summarize: %w", err)
		}
		aggs[i] = expr.CompileAggItem(item, outType)
	}

	// Build group-by columns.
	groupBy := make([]physical.GroupByCol, len(n.GroupBy))
	for i, ref := range n.GroupBy {
		col, err := findInTypedSchema(inputSchema, ref)
		if err != nil {
			return nil, fmt.Errorf("planner: summarize by: %w", err)
		}
		groupBy[i] = physical.GroupByCol{
			Name:   col.Name,
			Origin: col.Origin,
			Type:   col.Type,
			Eval:   expr.CompileScalar(&ast.FieldRefExpr{Ref: ref}),
		}
	}

	return &physical.HashAggregateOp{
		Input:     input,
		Aggs:      aggs,
		GroupBy:   groupBy,
		BatchSize: p.batchSize,
	}, nil
}

// ─── Sort ─────────────────────────────────────────────────────────────────────

func (p *planner) buildSort(n *logical.LogicalSort) (physical.Operator, error) {
	inputSchema, err := p.inferTypedSchema(n.Input)
	if err != nil {
		return nil, err
	}

	input, err := p.build(n.Input)
	if err != nil {
		return nil, err
	}

	keys := make([]physical.CompiledSortKey, len(n.Items))
	for i, item := range n.Items {
		idx, err := colIndexInTypedSchema(inputSchema, item.Field)
		if err != nil {
			return nil, fmt.Errorf("planner: sort by: %w", err)
		}
		keys[i] = physical.CompiledSortKey{
			ColIdx: idx,
			Desc:   item.Dir == ast.SortDesc,
		}
	}
	return &physical.SortOp{Input: input, Keys: keys, BatchSize: p.batchSize}, nil
}

// ─── Take ─────────────────────────────────────────────────────────────────────

func (p *planner) buildTake(n *logical.LogicalTake) (physical.Operator, error) {
	input, err := p.build(n.Input)
	if err != nil {
		return nil, err
	}
	return &physical.LimitOp{Input: input, Remaining: int(n.Count)}, nil
}

// ─── Join ─────────────────────────────────────────────────────────────────────

func (p *planner) buildJoin(n *logical.LogicalJoin) (physical.Operator, error) {
	leftSchema, err := p.inferTypedSchema(n.Left)
	if err != nil {
		return nil, err
	}
	rightSchema, err := p.inferTypedSchema(n.Right)
	if err != nil {
		return nil, err
	}

	// Resolve each join key to column indices in the left and right schemas.
	leftKeyIdxs := make([]int, len(n.Keys))
	rightKeyIdxs := make([]int, len(n.Keys))
	for i, key := range n.Keys {
		switch k := key.(type) {
		case *ast.SameNameKey:
			lIdx, err := colIndexInTypedSchema(leftSchema, ast.FieldRef{Name: k.Name})
			if err != nil {
				return nil, fmt.Errorf("planner: join key %q not found in left schema: %w", k.Name, err)
			}
			rIdx, err := colIndexInTypedSchema(rightSchema, ast.FieldRef{Name: k.Name})
			if err != nil {
				return nil, fmt.Errorf("planner: join key %q not found in right schema: %w", k.Name, err)
			}
			leftKeyIdxs[i] = lIdx
			rightKeyIdxs[i] = rIdx
		case *ast.ExplicitKey:
			lIdx, err := colIndexInTypedSchema(leftSchema, ast.FieldRef{Name: k.Left})
			if err != nil {
				return nil, fmt.Errorf("planner: join $left.%s not found: %w", k.Left, err)
			}
			rIdx, err := colIndexInTypedSchema(rightSchema, ast.FieldRef{Name: k.Right})
			if err != nil {
				return nil, fmt.Errorf("planner: join $right.%s not found: %w", k.Right, err)
			}
			leftKeyIdxs[i] = lIdx
			rightKeyIdxs[i] = rIdx
		default:
			return nil, fmt.Errorf("planner: unknown join key type %T", key)
		}
	}

	// Build the output schema: left columns || right columns.
	outCols := make([]inferredCol, 0, len(leftSchema)+len(rightSchema))
	outCols = append(outCols, leftSchema...)
	outCols = append(outCols, rightSchema...)

	outSchema := make([]batch.ColumnMeta, len(outCols))
	outKinds := make([]physical.VecKind, len(outCols))
	for i, col := range outCols {
		outSchema[i] = batch.ColumnMeta{Name: col.Name, Origin: col.Origin, Type: col.Type}
		outKinds[i] = physical.VecKindFromColumnType(col.Type)
	}

	left, err := p.build(n.Left)
	if err != nil {
		return nil, err
	}
	right, err := p.build(n.Right)
	if err != nil {
		return nil, err
	}

	return &physical.HashJoinOp{
		Left:         left,
		Right:        right,
		LeftKeyIdxs:  leftKeyIdxs,
		RightKeyIdxs: rightKeyIdxs,
		OutSchema:    outSchema,
		OutKinds:     outKinds,
		BatchSize:    p.batchSize,
	}, nil
}

// ─── Count ────────────────────────────────────────────────────────────────────

func (p *planner) buildCount(n *logical.LogicalCount) (physical.Operator, error) {
	input, err := p.build(n.Input)
	if err != nil {
		return nil, err
	}
	return &physical.CountOp{Input: input}, nil
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// inferredCol is a minimal typed column used within the planner.
type inferredCol struct {
	Name   string
	Origin string
	Type   source.ColumnType
}

// inferTypedSchema re-derives the output column types for a logical node using
// the same rules as logical.InferAndValidate, but returns the richer typed form
// rather than []batch.ColumnMeta.
func (p *planner) inferTypedSchema(node logical.LogicalNode) ([]inferredCol, error) {
	switch n := node.(type) {
	case *logical.LogicalSource:
		s, ok := p.schemas[n.TypeName]
		if !ok {
			return nil, fmt.Errorf("planner: unknown table %q", n.TypeName)
		}
		out := make([]inferredCol, len(s.Columns))
		for i, col := range s.Columns {
			out[i] = inferredCol{Name: col.Name, Origin: n.TypeName, Type: col.Type}
		}
		return out, nil

	case *logical.LogicalWhere:
		return p.inferTypedSchema(n.Input)

	case *logical.LogicalProject:
		return p.inferProjectSchema(n)

	case *logical.LogicalSummarize:
		return p.inferSummarizeSchema(n)

	case *logical.LogicalSort:
		return p.inferTypedSchema(n.Input)

	case *logical.LogicalTake:
		return p.inferTypedSchema(n.Input)

	case *logical.LogicalCount:
		return []inferredCol{{Name: "count_", Type: source.TypeInt64}}, nil

	case *logical.LogicalJoin:
		return p.inferJoinSchema(n)

	default:
		return nil, fmt.Errorf("planner: inferTypedSchema: unknown node %T", node)
	}
}

func (p *planner) inferJoinSchema(n *logical.LogicalJoin) ([]inferredCol, error) {
	left, err := p.inferTypedSchema(n.Left)
	if err != nil {
		return nil, err
	}
	right, err := p.inferTypedSchema(n.Right)
	if err != nil {
		return nil, err
	}
	out := make([]inferredCol, 0, len(left)+len(right))
	out = append(out, left...)
	out = append(out, right...)
	return out, nil
}

func (p *planner) inferProjectSchema(n *logical.LogicalProject) ([]inferredCol, error) {
	inputSchema, err := p.inferTypedSchema(n.Input)
	if err != nil {
		return nil, err
	}
	out := make([]inferredCol, len(n.Items))
	for i, item := range n.Items {
		if item.Alias == "" {
			ref := item.Expr.(*ast.FieldRefExpr).Ref
			col, err := findInTypedSchema(inputSchema, ref)
			if err != nil {
				return nil, err
			}
			out[i] = inferredCol{Name: col.Name, Origin: col.Origin, Type: col.Type}
		} else {
			ct, err := typeOfExpr(item.Expr, inputSchema)
			if err != nil {
				return nil, err
			}
			origin := ""
			if fre, ok := item.Expr.(*ast.FieldRefExpr); ok {
				if col, err := findInTypedSchema(inputSchema, fre.Ref); err == nil {
					origin = col.Origin
				}
			}
			out[i] = inferredCol{Name: item.Alias, Origin: origin, Type: ct}
		}
	}
	return out, nil
}

func (p *planner) inferSummarizeSchema(n *logical.LogicalSummarize) ([]inferredCol, error) {
	inputSchema, err := p.inferTypedSchema(n.Input)
	if err != nil {
		return nil, err
	}
	out := make([]inferredCol, 0, len(n.Aggs)+len(n.GroupBy))
	for _, item := range n.Aggs {
		ct, err := aggOutputType(item, inputSchema)
		if err != nil {
			return nil, err
		}
		name := item.Alias
		if name == "" {
			name = defaultAggName(item)
		}
		out = append(out, inferredCol{Name: name, Type: ct})
	}
	for _, ref := range n.GroupBy {
		col, err := findInTypedSchema(inputSchema, ref)
		if err != nil {
			return nil, err
		}
		out = append(out, inferredCol{Name: col.Name, Origin: col.Origin, Type: col.Type})
	}
	return out, nil
}

// findInTypedSchema looks up a FieldRef in []inferredCol.
func findInTypedSchema(schema []inferredCol, ref ast.FieldRef) (inferredCol, error) {
	found := -1
	for i, col := range schema {
		if col.Name != ref.Name {
			continue
		}
		if ref.Table != "" && col.Origin != ref.Table {
			continue
		}
		if found != -1 {
			return inferredCol{}, fmt.Errorf("ambiguous column %q", ref.Name)
		}
		found = i
	}
	if found == -1 {
		if ref.Table != "" {
			return inferredCol{}, fmt.Errorf("column %q.%q not found", ref.Table, ref.Name)
		}
		return inferredCol{}, fmt.Errorf("column %q not found", ref.Name)
	}
	return schema[found], nil
}

// colIndexInTypedSchema returns the 0-based index of ref in schema.
func colIndexInTypedSchema(schema []inferredCol, ref ast.FieldRef) (int, error) {
	found := -1
	for i, col := range schema {
		if col.Name != ref.Name {
			continue
		}
		if ref.Table != "" && col.Origin != ref.Table {
			continue
		}
		if found != -1 {
			return -1, fmt.Errorf("ambiguous column %q", ref.Name)
		}
		found = i
	}
	if found == -1 {
		return -1, fmt.Errorf("column %q not found", ref.Name)
	}
	return found, nil
}

// typeOfExpr derives the output ColumnType of a ScalarExpr given a typed schema.
func typeOfExpr(e ast.ScalarExpr, schema []inferredCol) (source.ColumnType, error) {
	switch s := e.(type) {
	case *ast.FieldRefExpr:
		col, err := findInTypedSchema(schema, s.Ref)
		if err != nil {
			return 0, err
		}
		return col.Type, nil
	case *ast.IntLit:
		return source.TypeInt64, nil
	case *ast.FloatLit:
		return source.TypeFloat64, nil
	case *ast.StringLit:
		return source.TypeString, nil
	case *ast.BoolLit:
		return source.TypeBool, nil
	case *ast.DatetimeLit:
		return source.TypeDatetime, nil
	case *ast.AgoExpr:
		return source.TypeDatetime, nil
	case *ast.BinaryExpr:
		return typeOfBinaryExpr(s, schema)
	case *ast.UnaryMinusExpr:
		inner, err := typeOfExpr(s.Expr, schema)
		if err != nil {
			return 0, err
		}
		switch inner {
		case source.TypeInt32, source.TypeInt64, source.TypeFloat64, source.TypeTimespan:
			return inner, nil
		default:
			return 0, fmt.Errorf("unary minus not applicable to %v", inner)
		}
	default:
		return 0, fmt.Errorf("unknown ScalarExpr type %T", e)
	}
}

func typeOfBinaryExpr(e *ast.BinaryExpr, schema []inferredCol) (source.ColumnType, error) {
	lt, err := typeOfExpr(e.Left, schema)
	if err != nil {
		return 0, err
	}
	rt, err := typeOfExpr(e.Right, schema)
	if err != nil {
		return 0, err
	}
	return applyBinaryTypeRule(lt, e.Op, rt)
}

// applyBinaryTypeRule mirrors the rule table in logical/infer.go.
func applyBinaryTypeRule(lt source.ColumnType, op ast.BinaryOp, rt source.ColumnType) (source.ColumnType, error) {
	isNumeric := func(t source.ColumnType) bool {
		return t == source.TypeInt32 || t == source.TypeInt64 || t == source.TypeFloat64
	}
	promoteNumeric := func(a, b source.ColumnType) source.ColumnType {
		if a == source.TypeFloat64 || b == source.TypeFloat64 {
			return source.TypeFloat64
		}
		if a == source.TypeInt64 || b == source.TypeInt64 {
			return source.TypeInt64
		}
		return source.TypeInt32
	}

	switch op {
	case ast.BinAdd, ast.BinSub:
		switch {
		case isNumeric(lt) && isNumeric(rt):
			return promoteNumeric(lt, rt), nil
		case lt == source.TypeDatetime && rt == source.TypeDatetime && op == ast.BinSub:
			return source.TypeTimespan, nil
		case lt == source.TypeDatetime && rt == source.TypeTimespan:
			return source.TypeDatetime, nil
		case lt == source.TypeTimespan && rt == source.TypeDatetime && op == ast.BinAdd:
			return source.TypeDatetime, nil
		case lt == source.TypeTimespan && rt == source.TypeTimespan:
			return source.TypeTimespan, nil
		default:
			return 0, fmt.Errorf("operator %v not applicable to %v and %v", op, lt, rt)
		}
	case ast.BinMul, ast.BinDiv:
		switch {
		case isNumeric(lt) && isNumeric(rt):
			return promoteNumeric(lt, rt), nil
		case lt == source.TypeTimespan && isNumeric(rt):
			return source.TypeTimespan, nil
		case isNumeric(lt) && rt == source.TypeTimespan && op == ast.BinMul:
			return source.TypeTimespan, nil
		default:
			return 0, fmt.Errorf("operator %v not applicable to %v and %v", op, lt, rt)
		}
	default:
		return 0, fmt.Errorf("unknown binary operator %v", op)
	}
}

// aggOutputType returns the output ColumnType for one AggItem given the input schema.
func aggOutputType(item ast.AggItem, schema []inferredCol) (source.ColumnType, error) {
	switch item.Call.Func {
	case ast.AggCount, ast.AggDcount:
		return source.TypeInt64, nil
	case ast.AggAvg:
		return source.TypeFloat64, nil
	case ast.AggSum:
		if item.Call.Field == nil {
			return 0, fmt.Errorf("sum requires a field argument")
		}
		col, err := findInTypedSchema(schema, *item.Call.Field)
		if err != nil {
			return 0, err
		}
		if col.Type == source.TypeInt32 {
			return source.TypeInt64, nil
		}
		return col.Type, nil
	case ast.AggMin, ast.AggMax:
		if item.Call.Field == nil {
			return 0, fmt.Errorf("min/max requires a field argument")
		}
		col, err := findInTypedSchema(schema, *item.Call.Field)
		if err != nil {
			return 0, err
		}
		return col.Type, nil
	default:
		return 0, fmt.Errorf("unknown agg function %v", item.Call.Func)
	}
}

func defaultAggName(item ast.AggItem) string {
	switch item.Call.Func {
	case ast.AggCount:
		return "count_"
	case ast.AggSum:
		return "sum_" + item.Call.Field.Name
	case ast.AggAvg:
		return "avg_" + item.Call.Field.Name
	case ast.AggMin:
		return "min_" + item.Call.Field.Name
	case ast.AggMax:
		return "max_" + item.Call.Field.Name
	case ast.AggDcount:
		return "dcount_" + item.Call.Field.Name
	default:
		return "agg_"
	}
}

// now is used when resolving relative time windows at plan-build time.
// It is a package-level variable so tests can override it.
var now = time.Now
