package logical

import (
	"fmt"

	"github.com/kozwoj/gobbler-query/query/ast"
	"github.com/kozwoj/gobbler-query/query/batch"
	"github.com/kozwoj/gobbler-query/query/source"
)

// typedColumn is the internal column representation used during plan validation.
// It extends batch.ColumnMeta with a source.ColumnType so the validator can
// check arithmetic and comparison type compatibility.
type typedColumn struct {
	Name   string
	Origin string
	Type   source.ColumnType
}

type typedSchema []typedColumn

func (ts typedSchema) toColumnMeta() []batch.ColumnMeta {
	meta := make([]batch.ColumnMeta, len(ts))
	for i, col := range ts {
		meta[i] = batch.ColumnMeta{Name: col.Name, Origin: col.Origin, Type: col.Type}
	}
	return meta
}

// InferAndValidate validates the logical plan against the provided source schemas
// and returns the output schema of the plan root.
//
// schemas maps each source type name to its column schema, loaded from the
// {typeName}.json files in the corresponding storage bucket. An error is returned
// on the first validation failure encountered.
func InferAndValidate(node LogicalNode, schemas map[string]*source.Schema) ([]batch.ColumnMeta, error) {
	ts, err := inferNode(node, schemas)
	if err != nil {
		return nil, err
	}
	return ts.toColumnMeta(), nil
}

func inferNode(node LogicalNode, schemas map[string]*source.Schema) (typedSchema, error) {
	switch n := node.(type) {
	case *LogicalSource:
		return inferSource(n, schemas)

	case *LogicalWhere:
		schema, err := inferNode(n.Input, schemas)
		if err != nil {
			return nil, err
		}
		if err := validateBoolExpr(n.Pred, schema); err != nil {
			return nil, fmt.Errorf("where: %w", err)
		}
		return schema, nil

	case *LogicalProject:
		return inferProject(n, schemas)

	case *LogicalSummarize:
		return inferSummarize(n, schemas)

	case *LogicalJoin:
		return inferJoin(n, schemas)

	case *LogicalSort:
		schema, err := inferNode(n.Input, schemas)
		if err != nil {
			return nil, err
		}
		if err := validateSortItems(n.Items, schema); err != nil {
			return nil, err
		}
		return schema, nil

	case *LogicalTake:
		return inferNode(n.Input, schemas)

	case *LogicalCount:
		if _, err := inferNode(n.Input, schemas); err != nil {
			return nil, err
		}
		return typedSchema{{Name: "count_", Origin: "", Type: source.TypeInt64}}, nil

	default:
		return nil, fmt.Errorf("infer: unknown node type %T", node)
	}
}

// ─── Source ───────────────────────────────────────────────────────────────────

func inferSource(n *LogicalSource, schemas map[string]*source.Schema) (typedSchema, error) {
	s, ok := schemas[n.TypeName]
	if !ok {
		return nil, fmt.Errorf("infer: unknown table %q", n.TypeName)
	}
	schema := make(typedSchema, len(s.Columns))
	for i, col := range s.Columns {
		schema[i] = typedColumn{Name: col.Name, Origin: n.TypeName, Type: col.Type}
	}
	return schema, nil
}

// ─── Column lookup ────────────────────────────────────────────────────────────

// findColumn resolves a FieldRef against a typedSchema, returning the matching
// column or an error if not found or ambiguous.
func findColumn(schema typedSchema, ref ast.FieldRef) (typedColumn, error) {
	found := -1
	for i, col := range schema {
		if col.Name != ref.Name {
			continue
		}
		if ref.Table != "" && col.Origin != ref.Table {
			continue
		}
		if found != -1 {
			return typedColumn{}, fmt.Errorf("ambiguous column %q; qualify with table name", ref.Name)
		}
		found = i
	}
	if found == -1 {
		if ref.Table != "" {
			return typedColumn{}, fmt.Errorf("column %q.%q not found", ref.Table, ref.Name)
		}
		return typedColumn{}, fmt.Errorf("column %q not found", ref.Name)
	}
	return schema[found], nil
}

// ─── Where validation ─────────────────────────────────────────────────────────

func validateBoolExpr(e ast.BoolExpr, schema typedSchema) error {
	switch n := e.(type) {
	case *ast.AndExpr:
		if err := validateBoolExpr(n.Left, schema); err != nil {
			return err
		}
		return validateBoolExpr(n.Right, schema)
	case *ast.OrExpr:
		if err := validateBoolExpr(n.Left, schema); err != nil {
			return err
		}
		return validateBoolExpr(n.Right, schema)
	case *ast.NotExpr:
		return validateBoolExpr(n.Expr, schema)
	case *ast.CompareExpr:
		return validateCompare(n, schema)
	case *ast.IsNullExpr:
		_, err := findColumn(schema, n.Field)
		return err
	case *ast.InExpr:
		// field existence check; list element type check deferred to a later stage
		_, err := findColumn(schema, n.Field)
		return err
	case *ast.BetweenExpr:
		// field existence check; bound type check deferred to a later stage
		_, err := findColumn(schema, n.Field)
		return err
	default:
		return fmt.Errorf("unknown BoolExpr type %T", e)
	}
}

func validateCompare(e *ast.CompareExpr, schema typedSchema) error {
	lk, err := scalarKindOf(e.Left, schema)
	if err != nil {
		return err
	}
	rk, err := scalarKindOf(e.Right, schema)
	if err != nil {
		return err
	}
	return checkCompareKinds(lk, rk, e.Op)
}

// ─── Sort validation ──────────────────────────────────────────────────────────

func validateSortItems(items []ast.SortItem, schema typedSchema) error {
	for _, item := range items {
		if _, err := findColumn(schema, item.Field); err != nil {
			return fmt.Errorf("sort by: %w", err)
		}
	}
	return nil
}

// ─── Scalar kind system ───────────────────────────────────────────────────────

// scalarKind is a coarse type classification used to validate comparison
// operand compatibility without needing exact column types.
type scalarKind int

const (
	kindNumeric  scalarKind = iota // TypeInt32, TypeInt64, TypeFloat64; IntLit, FloatLit
	kindString                     // TypeString; StringLit
	kindBool                       // TypeBool; BoolLit
	kindDatetime                   // TypeDatetime; DatetimeLit, AgoExpr
	kindTimespan                   // TypeTimespan
	kindDynamic                    // TypeDynamic
	kindUnknown                    // BinaryExpr/UnaryMinusExpr — type check deferred to Stage 2
)

func (k scalarKind) String() string {
	switch k {
	case kindNumeric:
		return "numeric"
	case kindString:
		return "string"
	case kindBool:
		return "bool"
	case kindDatetime:
		return "datetime"
	case kindTimespan:
		return "timespan"
	case kindDynamic:
		return "dynamic"
	default:
		return "unknown"
	}
}

func kindForColumnType(ct source.ColumnType) scalarKind {
	switch ct {
	case source.TypeInt32, source.TypeInt64, source.TypeFloat64:
		return kindNumeric
	case source.TypeString:
		return kindString
	case source.TypeBool:
		return kindBool
	case source.TypeDatetime:
		return kindDatetime
	case source.TypeTimespan:
		return kindTimespan
	case source.TypeDynamic:
		return kindDynamic
	default:
		return kindUnknown
	}
}

func scalarKindOf(e ast.ScalarExpr, schema typedSchema) (scalarKind, error) {
	switch s := e.(type) {
	case *ast.FieldRefExpr:
		col, err := findColumn(schema, s.Ref)
		if err != nil {
			return kindUnknown, err
		}
		return kindForColumnType(col.Type), nil
	case *ast.IntLit:
		return kindNumeric, nil
	case *ast.FloatLit:
		return kindNumeric, nil
	case *ast.StringLit:
		return kindString, nil
	case *ast.BoolLit:
		return kindBool, nil
	case *ast.DatetimeLit:
		return kindDatetime, nil
	case *ast.AgoExpr:
		return kindDatetime, nil
	case *ast.BinaryExpr, *ast.UnaryMinusExpr:
		ct, err := typeOfScalarExpr(e, schema)
		if err != nil {
			return kindUnknown, err
		}
		return kindForColumnType(ct), nil
	default:
		return kindUnknown, fmt.Errorf("infer: unknown ScalarExpr type %T", e)
	}
}

// checkCompareKinds validates that left and right are compatible for the
// given comparison operator.
func checkCompareKinds(left, right scalarKind, op ast.CompareOp) error {
	// If either side involves a BinaryExpr/UnaryMinusExpr, defer the type check.
	if left == kindUnknown || right == kindUnknown {
		return nil
	}
	// Dynamic columns are stored as plain strings; allow equality comparison
	// against a string literal: meta == "..." or meta != "...".
	if (left == kindDynamic && right == kindString) || (left == kindString && right == kindDynamic) {
		if op != ast.CmpEq && op != ast.CmpNotEq {
			return fmt.Errorf("operator %v not applicable to dynamic columns", op)
		}
		return nil
	}
	if left != right {
		return fmt.Errorf("comparison type mismatch: cannot compare %s with %s", left, right)
	}
	// Both same kind — check operator applicability.
	switch left {
	case kindBool:
		if op != ast.CmpEq && op != ast.CmpNotEq {
			return fmt.Errorf("operator %v not applicable to bool columns", op)
		}
	case kindDynamic:
		if op != ast.CmpEq && op != ast.CmpNotEq {
			return fmt.Errorf("operator %v not applicable to dynamic columns", op)
		}
	case kindNumeric, kindDatetime, kindTimespan:
		switch op {
		case ast.CmpContains, ast.CmpStartswith, ast.CmpEndswith, ast.CmpTildeEq:
			return fmt.Errorf("operator %v not applicable to %s columns", op, left)
		}
		// kindString: all operators are valid
	}
	return nil
}

// ─── Project ──────────────────────────────────────────────────────────────────

func inferProject(n *LogicalProject, schemas map[string]*source.Schema) (typedSchema, error) {
	inputSchema, err := inferNode(n.Input, schemas)
	if err != nil {
		return nil, err
	}
	out := make(typedSchema, 0, len(n.Items))
	for _, item := range n.Items {
		if item.Alias == "" {
			// Bare FieldRef — the parser guarantees Expr is *ast.FieldRefExpr.
			ref := item.Expr.(*ast.FieldRefExpr).Ref
			col, err := findColumn(inputSchema, ref)
			if err != nil {
				return nil, fmt.Errorf("project: %w", err)
			}
			out = append(out, typedColumn{Name: col.Name, Origin: col.Origin, Type: col.Type})
		} else {
			// Aliased expression: alias = Expr.
			if !containsFieldRef(item.Expr) {
				return nil, fmt.Errorf("project: %q: right-hand side contains no field reference", item.Alias)
			}
			ct, err := typeOfScalarExpr(item.Expr, inputSchema)
			if err != nil {
				return nil, fmt.Errorf("project: %q: %w", item.Alias, err)
			}
			// Preserve origin for simple renames (alias = FieldRef); computed cols get "".
			origin := ""
			if fre, ok := item.Expr.(*ast.FieldRefExpr); ok {
				if col, err := findColumn(inputSchema, fre.Ref); err == nil {
					origin = col.Origin
				}
			}
			out = append(out, typedColumn{Name: item.Alias, Origin: origin, Type: ct})
		}
	}
	return out, nil
}

// ─── Scalar expression type inference ────────────────────────────────────────

// typeOfScalarExpr returns the source.ColumnType of e given the current schema.
// It validates operand compatibility for BinaryExpr and UnaryMinusExpr.
func typeOfScalarExpr(e ast.ScalarExpr, schema typedSchema) (source.ColumnType, error) {
	switch s := e.(type) {
	case *ast.FieldRefExpr:
		col, err := findColumn(schema, s.Ref)
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
		inner, err := typeOfScalarExpr(s.Expr, schema)
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
		return 0, fmt.Errorf("infer: unknown ScalarExpr type %T", e)
	}
}

func typeOfBinaryExpr(e *ast.BinaryExpr, schema typedSchema) (source.ColumnType, error) {
	lt, err := typeOfScalarExpr(e.Left, schema)
	if err != nil {
		return 0, err
	}
	rt, err := typeOfScalarExpr(e.Right, schema)
	if err != nil {
		return 0, err
	}
	return applyBinaryTypeRule(lt, e.Op, rt)
}

// applyBinaryTypeRule returns the result type of (lt op rt), or an error
// if no valid type rule applies.
func applyBinaryTypeRule(lt source.ColumnType, op ast.BinaryOp, rt source.ColumnType) (source.ColumnType, error) {
	// int ± */ int → int64
	if isIntegral(lt) && isIntegral(rt) {
		return source.TypeInt64, nil
	}
	// float ± */ float → float64
	if lt == source.TypeFloat64 && rt == source.TypeFloat64 {
		return source.TypeFloat64, nil
	}
	// mixed numeric (int + float) → float64
	if isNumeric(lt) && isNumeric(rt) {
		return source.TypeFloat64, nil
	}
	// datetime - datetime → timespan
	if lt == source.TypeDatetime && rt == source.TypeDatetime && op == ast.BinSub {
		return source.TypeTimespan, nil
	}
	// datetime ± timespan → datetime
	if lt == source.TypeDatetime && rt == source.TypeTimespan &&
		(op == ast.BinAdd || op == ast.BinSub) {
		return source.TypeDatetime, nil
	}
	// timespan ± timespan → timespan
	if lt == source.TypeTimespan && rt == source.TypeTimespan &&
		(op == ast.BinAdd || op == ast.BinSub) {
		return source.TypeTimespan, nil
	}
	// timespan */ numeric → timespan
	if lt == source.TypeTimespan && isNumeric(rt) &&
		(op == ast.BinMul || op == ast.BinDiv) {
		return source.TypeTimespan, nil
	}
	return 0, fmt.Errorf("no type rule for %v %v %v", lt, op, rt)
}

func isIntegral(ct source.ColumnType) bool {
	return ct == source.TypeInt32 || ct == source.TypeInt64
}

func isNumeric(ct source.ColumnType) bool {
	return ct == source.TypeInt32 || ct == source.TypeInt64 || ct == source.TypeFloat64
}

// containsFieldRef reports whether e contains at least one FieldRefExpr.
// Used to reject pure-literal project items (e.g. project n = 42).
func containsFieldRef(e ast.ScalarExpr) bool {
	switch s := e.(type) {
	case *ast.FieldRefExpr:
		return true
	case *ast.BinaryExpr:
		return containsFieldRef(s.Left) || containsFieldRef(s.Right)
	case *ast.UnaryMinusExpr:
		return containsFieldRef(s.Expr)
	default:
		return false
	}
}

// ─── Summarize ────────────────────────────────────────────────────────────────

func inferSummarize(n *LogicalSummarize, schemas map[string]*source.Schema) (typedSchema, error) {
	inputSchema, err := inferNode(n.Input, schemas)
	if err != nil {
		return nil, err
	}

	out := make(typedSchema, 0, len(n.Aggs)+len(n.GroupBy))

	for _, item := range n.Aggs {
		col, err := validateAggItem(item, inputSchema)
		if err != nil {
			return nil, fmt.Errorf("summarize: %w", err)
		}
		out = append(out, col)
	}

	for _, ref := range n.GroupBy {
		col, err := findColumn(inputSchema, ref)
		if err != nil {
			return nil, fmt.Errorf("summarize by: %w", err)
		}
		out = append(out, typedColumn{Name: col.Name, Origin: col.Origin, Type: col.Type})
	}

	return out, nil
}

func validateAggItem(item ast.AggItem, schema typedSchema) (typedColumn, error) {
	switch item.Call.Func {
	case ast.AggCount:
		name := item.Alias
		if name == "" {
			name = "count_"
		}
		return typedColumn{Name: name, Origin: "", Type: source.TypeInt64}, nil

	case ast.AggSum:
		col, err := findColumn(schema, *item.Call.Field)
		if err != nil {
			return typedColumn{}, err
		}
		if !isNumeric(col.Type) {
			return typedColumn{}, fmt.Errorf("sum(%s): field must be numeric, got %v", col.Name, col.Type)
		}
		name := item.Alias
		if name == "" {
			name = "sum_" + col.Name
		}
		outType := col.Type
		if outType == source.TypeInt32 {
			outType = source.TypeInt64
		}
		return typedColumn{Name: name, Origin: "", Type: outType}, nil

	case ast.AggAvg:
		col, err := findColumn(schema, *item.Call.Field)
		if err != nil {
			return typedColumn{}, err
		}
		if !isNumeric(col.Type) {
			return typedColumn{}, fmt.Errorf("avg(%s): field must be numeric, got %v", col.Name, col.Type)
		}
		name := item.Alias
		if name == "" {
			name = "avg_" + col.Name
		}
		return typedColumn{Name: name, Origin: "", Type: source.TypeFloat64}, nil

	case ast.AggMin, ast.AggMax:
		col, err := findColumn(schema, *item.Call.Field)
		if err != nil {
			return typedColumn{}, err
		}
		if !isNumeric(col.Type) && col.Type != source.TypeDatetime && col.Type != source.TypeTimespan {
			return typedColumn{}, fmt.Errorf("%v(%s): field must be numeric, datetime, or timespan, got %v",
				item.Call.Func, col.Name, col.Type)
		}
		funcName := "min"
		if item.Call.Func == ast.AggMax {
			funcName = "max"
		}
		name := item.Alias
		if name == "" {
			name = funcName + "_" + col.Name
		}
		return typedColumn{Name: name, Origin: "", Type: col.Type}, nil

	case ast.AggDcount:
		col, err := findColumn(schema, *item.Call.Field)
		if err != nil {
			return typedColumn{}, err
		}
		name := item.Alias
		if name == "" {
			name = "dcount_" + col.Name
		}
		return typedColumn{Name: name, Origin: "", Type: source.TypeInt64}, nil

	default:
		return typedColumn{}, fmt.Errorf("unknown aggregation function %v", item.Call.Func)
	}
}

// ─── Join ─────────────────────────────────────────────────────────────────────

func inferJoin(n *LogicalJoin, schemas map[string]*source.Schema) (typedSchema, error) {
	leftSchema, err := inferNode(n.Left, schemas)
	if err != nil {
		return nil, err
	}
	rightSchema, err := inferNode(n.Right, schemas)
	if err != nil {
		return nil, err
	}

	// dedupedRight tracks right-side column names that are removed because
	// a SameNameKey means the left copy is kept instead.
	dedupedRight := make(map[string]bool)

	for _, key := range n.Keys {
		switch k := key.(type) {
		case *ast.SameNameKey:
			leftCol, err := findColumn(leftSchema, ast.FieldRef{Name: k.Name})
			if err != nil {
				return nil, fmt.Errorf("join on %s: left side: %w", k.Name, err)
			}
			rightCol, err := findColumn(rightSchema, ast.FieldRef{Name: k.Name})
			if err != nil {
				return nil, fmt.Errorf("join on %s: right side: %w", k.Name, err)
			}
			if kindForColumnType(leftCol.Type) != kindForColumnType(rightCol.Type) {
				return nil, fmt.Errorf("join on %s: type mismatch: left is %v, right is %v",
					k.Name, leftCol.Type, rightCol.Type)
			}
			dedupedRight[k.Name] = true

		case *ast.ExplicitKey:
			leftCol, err := findColumn(leftSchema, ast.FieldRef{Name: k.Left})
			if err != nil {
				return nil, fmt.Errorf("join on $left.%s: %w", k.Left, err)
			}
			rightCol, err := findColumn(rightSchema, ast.FieldRef{Name: k.Right})
			if err != nil {
				return nil, fmt.Errorf("join on $right.%s: %w", k.Right, err)
			}
			if kindForColumnType(leftCol.Type) != kindForColumnType(rightCol.Type) {
				return nil, fmt.Errorf("join on $left.%s == $right.%s: type mismatch: left is %v, right is %v",
					k.Left, k.Right, leftCol.Type, rightCol.Type)
			}

		default:
			return nil, fmt.Errorf("join: unknown key type %T", key)
		}
	}

	// Output = all left columns + right columns not deduped.
	out := make(typedSchema, len(leftSchema), len(leftSchema)+len(rightSchema))
	copy(out, leftSchema)
	for _, col := range rightSchema {
		if dedupedRight[col.Name] {
			continue
		}
		out = append(out, col)
	}
	return out, nil
}
