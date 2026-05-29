package ast

import "time"

// ─── Field reference ──────────────────────────────────────────────────────────

// FieldRef is a column reference with an optional table or join-alias qualifier.
//
//	statusCode          → Table = "",      Name = "statusCode"
//	alpha.statusCode    → Table = "alpha", Name = "statusCode"
type FieldRef struct {
	Table string // empty when unqualified
	Name  string
}

// ─── Timespan ─────────────────────────────────────────────────────────────────

// TimespanLit is a resolved duration constant, e.g. 24h or 1d12h30m.
// The Duration field holds the exact Go duration so the parser handles all
// Gobbler timespan forms (d/h/m/s/w primary units, optional Go-duration tail,
// fractional magnitudes such as 2.5h, compound forms such as 1d12h30m).
type TimespanLit struct {
	Duration time.Duration
}

// ─── Boolean expressions ──────────────────────────────────────────────────────

// BoolExpr is any expression that evaluates to a boolean.
// Concrete types: OrExpr, AndExpr, NotExpr, CompareExpr,
// InExpr, BetweenExpr, IsNullExpr.
type BoolExpr interface {
	boolExprNode()
}

// OrExpr is true when either operand is true.
//
//	a or b or c  →  OrExpr{OrExpr{a, b}, c}  (left-associative)
type OrExpr struct {
	Left  BoolExpr
	Right BoolExpr
}

// AndExpr is true when both operands are true.
//
//	a and b and c  →  AndExpr{AndExpr{a, b}, c}  (left-associative)
type AndExpr struct {
	Left  BoolExpr
	Right BoolExpr
}

// NotExpr negates its operand.
//
//	not isempty(region)
type NotExpr struct {
	Expr BoolExpr
}

// CompareExpr compares two scalar expressions.
//
//	statusCode >= 400
//	region =~ "eastus"
//	message contains "timeout"
type CompareExpr struct {
	Left  ScalarExpr
	Op    CompareOp
	Right ScalarExpr
}

// CompareOp is the operator in a CompareExpr.
type CompareOp int

const (
	CmpEq         CompareOp = iota // ==
	CmpNotEq                       // !=
	CmpLt                          // <
	CmpLtEq                        // <=
	CmpGt                          // >
	CmpGtEq                        // >=
	CmpTildeEq                     // =~  (case-insensitive equality)
	CmpContains                    // contains
	CmpStartswith                  // startswith
	CmpEndswith                    // endswith
)

// InExpr tests whether a field value is (or is not) in a literal list.
//
//	level in ("Error", "Warning")
//	code !in (200, 201, 204)
type InExpr struct {
	Field   FieldRef
	Negated bool      // true for "!in"
	Values  []Literal // the parenthesised list; len >= 1
}

// BetweenExpr tests whether a field value falls within an inclusive literal range.
//
//	durationMs between (100 .. 500)
type BetweenExpr struct {
	Field FieldRef
	Lo    Literal
	Hi    Literal
}

// IsNullExpr tests for null or empty values.
//
//	isnull(userId)    isnotnull(userId)    isempty(message)
type IsNullExpr struct {
	Kind  IsNullKind
	Field FieldRef
}

// IsNullKind identifies which null-test function is used.
type IsNullKind int

const (
	KindIsNull    IsNullKind = iota // isnull(f)
	KindIsNotNull                   // isnotnull(f)
	KindIsEmpty                     // isempty(f)
)

// bool expr marker methods
func (*OrExpr) boolExprNode()      {}
func (*AndExpr) boolExprNode()     {}
func (*NotExpr) boolExprNode()     {}
func (*CompareExpr) boolExprNode() {}
func (*InExpr) boolExprNode()      {}
func (*BetweenExpr) boolExprNode() {}
func (*IsNullExpr) boolExprNode()  {}

// ─── Scalar expressions ───────────────────────────────────────────────────────

// ScalarExpr is any expression that evaluates to a scalar value.
// Concrete types: BinaryExpr, UnaryMinusExpr, FieldRefExpr,
// and all Literal types (IntLit, FloatLit, StringLit, BoolLit, DatetimeLit, AgoExpr).
type ScalarExpr interface {
	scalarExprNode()
}

// BinaryExpr is a left-associative arithmetic expression.
//
//	endTime - startTime
//	quantity * price
type BinaryExpr struct {
	Left  ScalarExpr
	Op    BinaryOp
	Right ScalarExpr
}

// BinaryOp is the arithmetic operator in a BinaryExpr.
type BinaryOp int

const (
	BinAdd BinaryOp = iota // +
	BinSub                 // -
	BinMul                 // *
	BinDiv                 // /
)

// UnaryMinusExpr negates a scalar expression.
//
//	-offset
type UnaryMinusExpr struct {
	Expr ScalarExpr
}

// FieldRefExpr wraps a FieldRef for use as a scalar expression.
type FieldRefExpr struct {
	Ref FieldRef
}

// scalar expr marker methods
func (*BinaryExpr) scalarExprNode()     {}
func (*UnaryMinusExpr) scalarExprNode() {}
func (*FieldRefExpr) scalarExprNode()   {}

// ─── Literals ─────────────────────────────────────────────────────────────────

// Literal is a constant value that can appear in IN lists, BETWEEN bounds,
// and as a primary scalar expression.  All Literal types also satisfy ScalarExpr.
type Literal interface {
	ScalarExpr
	literalNode()
}

// IntLit is an integer constant, e.g. 42.
type IntLit struct{ Value int64 }

// FloatLit is a floating-point constant, e.g. 3.14.
type FloatLit struct{ Value float64 }

// StringLit is a double-quoted string constant, e.g. "eastus".
type StringLit struct{ Value string }

// BoolLit is true or false.
type BoolLit struct{ Value bool }

// DatetimeLit is a datetime(...) constant with a pre-parsed value.
// The Value is always in UTC because Gobbler timestamps carry no timezone.
type DatetimeLit struct{ Value time.Time }

// AgoExpr is the ago(Nh) time helper — a first-class literal representing
// (now − Duration).  It satisfies both Literal and ScalarExpr.
//
//	ago(1h)   ago(7d)
type AgoExpr struct {
	Duration TimespanLit
}

// literal / scalar marker methods
func (*IntLit) scalarExprNode()      {}
func (*FloatLit) scalarExprNode()    {}
func (*StringLit) scalarExprNode()   {}
func (*BoolLit) scalarExprNode()     {}
func (*DatetimeLit) scalarExprNode() {}
func (*AgoExpr) scalarExprNode()     {}

func (*IntLit) literalNode()      {}
func (*FloatLit) literalNode()    {}
func (*StringLit) literalNode()   {}
func (*BoolLit) literalNode()     {}
func (*DatetimeLit) literalNode() {}
func (*AgoExpr) literalNode()     {}
