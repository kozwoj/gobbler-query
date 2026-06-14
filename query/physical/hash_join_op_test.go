package physical

import (
	"testing"

	"github.com/kozwoj/gobbler-query/query/batch"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// joinOp builds a HashJoinOp from two fakeOperators using the given key column
// indices. outSchema and outKinds must be the combined left||right schema; use
// joinOpFromSchema when either side may have no batches.
func joinOp(left, right *fakeOperator, leftKeyIdxs, rightKeyIdxs []int) *HashJoinOp {
	return joinOpFromSchemas(
		left, right, leftKeyIdxs, rightKeyIdxs,
		left.batches[0].Schema, left.batches[0].Columns,
		right.batches[0].Schema, right.batches[0].Columns,
	)
}

// joinOpFromSchemas is the full form used when one side may be empty.
func joinOpFromSchemas(
	left, right *fakeOperator,
	leftKeyIdxs, rightKeyIdxs []int,
	ls []batch.ColumnMeta, lc []batch.ColumnVector,
	rs []batch.ColumnMeta, rc []batch.ColumnVector,
) *HashJoinOp {
	outSchema := make([]batch.ColumnMeta, 0, len(ls)+len(rs))
	outSchema = append(outSchema, ls...)
	outSchema = append(outSchema, rs...)

	outKinds := make([]VecKind, 0, len(lc)+len(rc))
	for _, cv := range lc {
		k, _ := vecKindOf(cv)
		outKinds = append(outKinds, k)
	}
	for _, cv := range rc {
		k, _ := vecKindOf(cv)
		outKinds = append(outKinds, k)
	}

	return &HashJoinOp{
		Left:         left,
		Right:        right,
		LeftKeyIdxs:  leftKeyIdxs,
		RightKeyIdxs: rightKeyIdxs,
		OutSchema:    outSchema,
		OutKinds:     outKinds,
		BatchSize:    512,
	}
}

// ─── J1: basic match ─────────────────────────────────────────────────────────

// J1: left and right each have one row matching on the same key.
// Output should have one row with all columns from both sides.
func TestHashJoinOp_J1_BasicMatch(t *testing.T) {
	left := &fakeOperator{batches: []*batch.Batch{
		twoCols("requests", "userId", "statusCode",
			[]string{"u1"}, []int32{200}),
	}}
	right := &fakeOperator{batches: []*batch.Batch{
		twoCols("users", "userId", "tier",
			[]string{"u1"}, []int32{1}),
	}}
	op := joinOp(left, right, []int{0}, []int{0})
	rows, _ := drainAll(t, op)

	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0][0] != "u1" {
		t.Errorf("col 0 (left userId) = %v, want u1", rows[0][0])
	}
	if rows[0][1] != int32(200) {
		t.Errorf("col 1 (left statusCode) = %v, want 200", rows[0][1])
	}
	if rows[0][2] != "u1" {
		t.Errorf("col 2 (right userId) = %v, want u1", rows[0][2])
	}
	if rows[0][3] != int32(1) {
		t.Errorf("col 3 (right tier) = %v, want 1", rows[0][3])
	}
}

// ─── J2: non-matching row dropped ────────────────────────────────────────────

// J2: left has two rows; only one matches a right row. The non-matching left
// row must be dropped (inner join semantics).
func TestHashJoinOp_J2_NonMatchingRowDropped(t *testing.T) {
	left := &fakeOperator{batches: []*batch.Batch{
		strBatch("requests", "userId", "u1", "u2"),
	}}
	right := &fakeOperator{batches: []*batch.Batch{
		strBatch("users", "userId", "u1"),
	}}
	op := joinOp(left, right, []int{0}, []int{0})
	rows, _ := drainAll(t, op)

	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 (u2 should be dropped)", len(rows))
	}
	if rows[0][0] != "u1" {
		t.Errorf("col 0 = %v, want u1", rows[0][0])
	}
}

// ─── J3: one-to-many ─────────────────────────────────────────────────────────

// J3: one left row matches two right rows → two output rows.
func TestHashJoinOp_J3_OneToMany(t *testing.T) {
	left := &fakeOperator{batches: []*batch.Batch{
		strBatch("requests", "userId", "u1"),
	}}
	right := &fakeOperator{batches: []*batch.Batch{
		strBatch("users", "userId", "u1", "u1"),
	}}
	op := joinOp(left, right, []int{0}, []int{0})
	rows, _ := drainAll(t, op)

	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 (one-to-many)", len(rows))
	}
	for i, r := range rows {
		if r[0] != "u1" || r[1] != "u1" {
			t.Errorf("row %d: %v, want [u1 u1]", i, r)
		}
	}
}

// ─── J4: multi-key join ───────────────────────────────────────────────────────

// J4: join on two keys simultaneously. Only the row matching on both keys
// should appear in the output.
func TestHashJoinOp_J4_MultiKey(t *testing.T) {
	// Left: (userId, region): (u1, east), (u1, west)
	left := &fakeOperator{batches: []*batch.Batch{{
		Length: 2,
		Schema: []batch.ColumnMeta{{Name: "userId"}, {Name: "region"}},
		Columns: []batch.ColumnVector{
			&batch.StringVector{Values: []string{"u1", "u1"}},
			&batch.StringVector{Values: []string{"east", "west"}},
		},
	}}}
	// Right: (userId, region): (u1, east) only
	right := &fakeOperator{batches: []*batch.Batch{{
		Length: 1,
		Schema: []batch.ColumnMeta{{Name: "userId"}, {Name: "region"}},
		Columns: []batch.ColumnVector{
			&batch.StringVector{Values: []string{"u1"}},
			&batch.StringVector{Values: []string{"east"}},
		},
	}}}
	op := joinOp(left, right, []int{0, 1}, []int{0, 1})
	rows, _ := drainAll(t, op)

	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 (only east matches)", len(rows))
	}
	if rows[0][1] != "east" {
		t.Errorf("region col = %v, want east", rows[0][1])
	}
}

// ─── J5: empty right side ────────────────────────────────────────────────────

// J5: right side is empty → inner join produces no output rows.
func TestHashJoinOp_J5_EmptyRight(t *testing.T) {
	leftBatch := strBatch("requests", "userId", "u1", "u2")
	rightSchema := []batch.ColumnMeta{{Name: "userId", Origin: "users"}}
	rightCols := []batch.ColumnVector{&batch.StringVector{}}

	left := &fakeOperator{batches: []*batch.Batch{leftBatch}}
	right := &fakeOperator{batches: []*batch.Batch{}} // empty
	op := joinOpFromSchemas(left, right, []int{0}, []int{0},
		leftBatch.Schema, leftBatch.Columns,
		rightSchema, rightCols)
	rows, _ := drainAll(t, op)

	if len(rows) != 0 {
		t.Fatalf("got %d rows, want 0 (empty right → no matches)", len(rows))
	}
}

// ─── J6: empty left side ─────────────────────────────────────────────────────

// J6: left side is empty → output is immediately EOF.
func TestHashJoinOp_J6_EmptyLeft(t *testing.T) {
	rightBatch := strBatch("users", "userId", "u1")
	leftSchema := []batch.ColumnMeta{{Name: "userId", Origin: "requests"}}
	leftCols := []batch.ColumnVector{&batch.StringVector{}}

	left := &fakeOperator{batches: []*batch.Batch{}} // empty
	right := &fakeOperator{batches: []*batch.Batch{rightBatch}}
	op := joinOpFromSchemas(left, right, []int{0}, []int{0},
		leftSchema, leftCols,
		rightBatch.Schema, rightBatch.Columns)
	rows, _ := drainAll(t, op)

	if len(rows) != 0 {
		t.Fatalf("got %d rows, want 0 (empty left → no output)", len(rows))
	}
}

// ─── J7: schema has correct column count ─────────────────────────────────────

// J7: output schema must be left columns || right columns.
func TestHashJoinOp_J7_OutputSchemaIsLeftThenRight(t *testing.T) {
	left := &fakeOperator{batches: []*batch.Batch{
		twoCols("requests", "userId", "statusCode",
			[]string{"u1"}, []int32{200}),
	}}
	right := &fakeOperator{batches: []*batch.Batch{
		twoCols("users", "userId", "tier",
			[]string{"u1"}, []int32{1}),
	}}
	op := joinOp(left, right, []int{0}, []int{0})

	b, err := op.Next()
	if err != nil {
		t.Fatalf("Next() error: %v", err)
	}
	if len(b.Schema) != 4 {
		t.Fatalf("schema len = %d, want 4", len(b.Schema))
	}
	wantNames := []string{"userId", "statusCode", "userId", "tier"}
	for i, want := range wantNames {
		if b.Schema[i].Name != want {
			t.Errorf("schema[%d].Name = %q, want %q", i, b.Schema[i].Name, want)
		}
	}
	wantOrigins := []string{"requests", "requests", "users", "users"}
	for i, want := range wantOrigins {
		if b.Schema[i].Origin != want {
			t.Errorf("schema[%d].Origin = %q, want %q", i, b.Schema[i].Origin, want)
		}
	}
}
