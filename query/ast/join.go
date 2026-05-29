package ast

// JoinKey describes how to match a row from the left side of a join to a row
// on the right side.  Two forms are supported.
type JoinKey interface {
	joinKeyNode()
}

// SameNameKey is the shorthand form where both sides share the same column name.
//
//	| join (...) on userId
type SameNameKey struct {
	Name string // column name present on both sides
}

// ExplicitKey gives distinct column names for the left and right sides.
//
//	| join (...) on $left.id == $right.orderId
type ExplicitKey struct {
	Left  string // left-side column name  (after $left.)
	Right string // right-side column name (after $right.)
}

func (*SameNameKey) joinKeyNode() {}
func (*ExplicitKey) joinKeyNode() {}
