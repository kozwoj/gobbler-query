package physical

import (
	"fmt"
	"strings"
)

// groupKey encodes the values of the specified columns in a row into a
// deterministic, collision-resistant string suitable for use as a map key.
//
// Encoding per column:
//   - null:     "N|"
//   - non-null: "<Go type name>:<value byte length>:<fmt.Sprintf value>|"
//
// The type prefix prevents collisions between equal-looking values of different
// types (e.g. int32(1) vs int64(1) vs string("1")).
// The byte-length prefix prevents concatenation ambiguity: without it,
// [string("a"), string("bc")] and [string("ab"), string("c")] would produce
// the same key once concatenated.
// The "|" field separator is redundant given the length prefix, but makes
// encoded keys easier to read during debugging.
func groupKey(row []any, nulls []bool, colIndices []int) string {
	var sb strings.Builder
	for _, idx := range colIndices {
		if nulls[idx] {
			sb.WriteString("N|")
		} else {
			v := fmt.Sprintf("%v", row[idx])
			fmt.Fprintf(&sb, "%T:%d:%s|", row[idx], len(v), v)
		}
	}
	return sb.String()
}
