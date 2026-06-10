package physical

import (
	"testing"
	"time"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// kr builds a row + null slice for groupKey calls.
// Pass nil as a value to mark that column null.
func kr(vals ...any) ([]any, []bool) {
	nulls := make([]bool, len(vals))
	for i, v := range vals {
		if v == nil {
			nulls[i] = true
		}
	}
	return vals, nulls
}

func key(row []any, nulls []bool, cols ...int) string {
	return groupKey(row, nulls, cols)
}

// ─── determinism ─────────────────────────────────────────────────────────────

func TestGroupKey_SameInput_SameKey(t *testing.T) {
	r, n := kr(int32(1), "east")
	if key(r, n, 0, 1) != key(r, n, 0, 1) {
		t.Error("same input must produce same key")
	}
}

// ─── type disambiguation ─────────────────────────────────────────────────────

func TestGroupKey_Int32VsInt64_Different(t *testing.T) {
	r32, n32 := kr(int32(1))
	r64, n64 := kr(int64(1))
	if key(r32, n32, 0) == key(r64, n64, 0) {
		t.Error("int32(1) and int64(1) must produce different keys")
	}
}

func TestGroupKey_Int64VsString_Different(t *testing.T) {
	ri, ni := kr(int64(1))
	rs, ns := kr("1")
	if key(ri, ni, 0) == key(rs, ns, 0) {
		t.Error("int64(1) and string(\"1\") must produce different keys")
	}
}

func TestGroupKey_BoolVsInt_Different(t *testing.T) {
	rb, nb := kr(true)
	ri, ni := kr(int32(1))
	if key(rb, nb, 0) == key(ri, ni, 0) {
		t.Error("bool(true) and int32(1) must produce different keys")
	}
}

// ─── concatenation ambiguity ─────────────────────────────────────────────────

func TestGroupKey_StringPairAmbiguity(t *testing.T) {
	// Without length encoding, ["a","bc"] and ["ab","c"] would collide.
	r1, n1 := kr("a", "bc")
	r2, n2 := kr("ab", "c")
	if key(r1, n1, 0, 1) == key(r2, n2, 0, 1) {
		t.Error(`["a","bc"] and ["ab","c"] must produce different keys`)
	}
}

// ─── null handling ────────────────────────────────────────────────────────────

func TestGroupKey_NullVsNonNull_Different(t *testing.T) {
	rNull, nNull := kr(nil)
	rVal, nVal := kr("N") // the string "N" could look like null without type prefix
	if key(rNull, nNull, 0) == key(rVal, nVal, 0) {
		t.Error(`null and string("N") must produce different keys`)
	}
}

func TestGroupKey_BothNull_Equal(t *testing.T) {
	r1, n1 := kr(nil)
	r2, n2 := kr(nil)
	if key(r1, n1, 0) != key(r2, n2, 0) {
		t.Error("two null values must produce equal keys")
	}
}

func TestGroupKey_NullInFirstCol_Different(t *testing.T) {
	r1, n1 := kr(nil, "east")
	r2, n2 := kr("east", nil)
	if key(r1, n1, 0, 1) == key(r2, n2, 0, 1) {
		t.Error("null in col 0 vs null in col 1 must differ")
	}
}

// ─── column selection ─────────────────────────────────────────────────────────

func TestGroupKey_SelectsOnlySpecifiedCols(t *testing.T) {
	// Two rows differ only in col 1 (not selected): keys must be equal.
	r1, n1 := kr("same", "x")
	r2, n2 := kr("same", "y")
	if key(r1, n1, 0) != key(r2, n2, 0) {
		t.Error("rows that match on the selected column must produce equal keys")
	}
}

func TestGroupKey_DifferentSelectedCols_Different(t *testing.T) {
	r1, n1 := kr("a", "b")
	r2, n2 := kr("b", "a")
	if key(r1, n1, 0, 1) == key(r2, n2, 0, 1) {
		t.Error("swapped values must produce different keys")
	}
}

// ─── all supported types ─────────────────────────────────────────────────────

func TestGroupKey_AllTypes_Deterministic(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	row, nulls := kr(
		int32(1),
		int64(2),
		float64(3.14),
		"hello",
		true,
		now,
		5*time.Second,
	)
	cols := []int{0, 1, 2, 3, 4, 5, 6}
	k1 := groupKey(row, nulls, cols)
	k2 := groupKey(row, nulls, cols)
	if k1 != k2 {
		t.Error("all-types key must be deterministic")
	}
}

func TestGroupKey_DifferentDatetimes_Different(t *testing.T) {
	t1 := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
	r1, n1 := kr(t1)
	r2, n2 := kr(t2)
	if key(r1, n1, 0) == key(r2, n2, 0) {
		t.Error("different datetimes must produce different keys")
	}
}

func TestGroupKey_DifferentTimespans_Different(t *testing.T) {
	r1, n1 := kr(1 * time.Second)
	r2, n2 := kr(2 * time.Second)
	if key(r1, n1, 0) == key(r2, n2, 0) {
		t.Error("different timespans must produce different keys")
	}
}
