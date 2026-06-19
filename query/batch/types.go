package batch

// ColumnType identifies the data type of a column as it flows through the
// pipeline. The zero value (TypeInt32) is intentional — callers that do not
// set the type explicitly get a defined, non-garbage default.
type ColumnType int

const (
	TypeInt32   ColumnType = iota // 0
	TypeFloat64                   // 1 — Gobbler source type "real"
	TypeString                    // 2
	TypeBool                      // 3
	TypeDatetime                  // 4
	TypeTimespan                  // 5 — Go duration string, e.g. "1h10m10s"
	TypeDynamic                   // 6 — opaque; stored as unquoted JSON string
	TypeInt64                     // 7 — not a native Gobbler source type; produced by arithmetic and aggregation
)
