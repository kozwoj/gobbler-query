package expr

import (
	"encoding/binary"
	"math"
)

// ValueKind identifies which field of a Value is active.
type ValueKind uint8

const (
	KindNull     ValueKind = iota
	KindInt32              // I holds the int32 value (sign-extended to int64)
	KindInt64              // I holds the int64 value
	KindFloat64            // F holds the float64 value
	KindBool               // I holds 0 (false) or 1 (true)
	KindString             // S holds the string value
	KindDatetime           // I holds UnixNano as int64
	KindTimespan           // I holds time.Duration (nanoseconds) as int64
	KindDynamic            // S holds the raw JSON string
)

// Value is a single typed scalar used in compute paths (eval, compare,
// aggregation, group-key encoding). It is NOT used for row storage.
//
// Layout (40 bytes on amd64/arm64):
//
//	Kind  1 byte  (+ 7 bytes padding before I)
//	I     8 bytes  int64 — used by Int32, Int64, Bool, Datetime, Timespan
//	F     8 bytes  float64 — used by Float64
//	S    16 bytes  string header — used by String, Dynamic
type Value struct {
	Kind ValueKind
	I    int64
	F    float64
	S    string
}

// AppendValueKey appends an injective binary encoding of v to buf and
// returns the extended slice. Two values are equal iff their encodings
// are equal, so the result is safe to use as a map key (via string cast).
func AppendValueKey(buf []byte, v Value) []byte {
	buf = append(buf, byte(v.Kind))
	switch v.Kind {
	case KindNull:
		// kind byte alone is sufficient

	case KindInt32, KindInt64, KindBool, KindDatetime, KindTimespan:
		buf = binary.LittleEndian.AppendUint64(buf, uint64(v.I))

	case KindFloat64:
		buf = binary.LittleEndian.AppendUint64(buf, math.Float64bits(v.F))

	case KindString, KindDynamic:
		buf = binary.LittleEndian.AppendUint32(buf, uint32(len(v.S)))
		buf = append(buf, v.S...)
	}
	return buf
}

// CmpValue compares two non-null Values of compatible kinds.
// Returns -1, 0, or +1. Used by min/max accumulators and sort comparators.
func CmpValue(a, b Value) int {
	switch a.Kind {
	case KindInt32, KindInt64, KindDatetime, KindTimespan:
		if a.I < b.I {
			return -1
		} else if a.I > b.I {
			return 1
		}
	case KindFloat64:
		if a.F < b.F {
			return -1
		} else if a.F > b.F {
			return 1
		}
	case KindString, KindDynamic:
		if a.S < b.S {
			return -1
		} else if a.S > b.S {
			return 1
		}
	case KindBool:
		if a.I < b.I {
			return -1
		} else if a.I > b.I {
			return 1
		}
	}
	return 0
}
