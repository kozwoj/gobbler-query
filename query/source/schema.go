package source

import (
	"encoding/json"
	"fmt"
)

// ColumnType identifies the data type of a column.
type ColumnType int

const (
	TypeInt32   ColumnType = iota
	TypeFloat64            // Gobbler type "real"
	TypeString
	TypeBool
	TypeDatetime
	TypeTimespan // Go duration string, e.g. "1h10m10s"; stored as time.Duration
	TypeDynamic  // opaque; stored as unquoted JSON string
)

// ColumnSchema describes a single column's name and type.
type ColumnSchema struct {
	Name string
	Type ColumnType
}

// Schema is the parsed representation of a type.json file.
type Schema struct {
	Columns []ColumnSchema
}

// gobblerTypeMap maps Gobbler's type.json type strings to ColumnType.
var gobblerTypeMap = map[string]ColumnType{
	"bool":     TypeBool,
	"datetime": TypeDatetime,
	"dynamic":  TypeDynamic,
	"int":      TypeInt32,
	"real":     TypeFloat64,
	"string":   TypeString,
	"timespan": TypeTimespan,
}

// typeJSON is the structure of Gobbler's type.json file.
type typeJSON struct {
	Name           string `json:"name"`
	OrderedColumns []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"orderedColumns"`
}

// parseSchema unmarshals the contents of a type.json file into a Schema.
func parseSchema(data []byte) (*Schema, error) {
	var tj typeJSON
	if err := json.Unmarshal(data, &tj); err != nil {
		return nil, fmt.Errorf("parseSchema: invalid JSON: %w", err)
	}
	if len(tj.OrderedColumns) == 0 {
		return nil, fmt.Errorf("parseSchema: type.json for %q has no columns", tj.Name)
	}
	cols := make([]ColumnSchema, len(tj.OrderedColumns))
	for i, c := range tj.OrderedColumns {
		ct, ok := gobblerTypeMap[c.Type]
		if !ok {
			return nil, fmt.Errorf("parseSchema: unknown column type %q for column %q", c.Type, c.Name)
		}
		cols[i] = ColumnSchema{Name: c.Name, Type: ct}
	}
	return &Schema{Columns: cols}, nil
}
