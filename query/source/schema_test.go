package source

import (
	"os"
	"testing"
)

func TestParseSchema_Requests(t *testing.T) {
	data, err := os.ReadFile("../../testdata/requests/requests.json")
	if err != nil {
		t.Fatalf("read requests.json: %v", err)
	}

	schema, err := parseSchema(data)
	if err != nil {
		t.Fatalf("parseSchema: %v", err)
	}

	want := []ColumnSchema{
		{Name: "timestamp", Type: TypeDatetime},
		{Name: "requestId", Type: TypeString},
		{Name: "userId", Type: TypeString},
		{Name: "requestCode", Type: TypeString},
		{Name: "statusCode", Type: TypeInt32},
		{Name: "durationMs", Type: TypeFloat64},
		{Name: "region", Type: TypeString},
		{Name: "ttl", Type: TypeTimespan},
	}

	if len(schema.Columns) != len(want) {
		t.Fatalf("got %d columns, want %d", len(schema.Columns), len(want))
	}
	for i, w := range want {
		got := schema.Columns[i]
		if got.Name != w.Name || got.Type != w.Type {
			t.Errorf("column[%d]: got {%q, %d}, want {%q, %d}", i, got.Name, got.Type, w.Name, w.Type)
		}
	}
}

func TestParseSchema_Users(t *testing.T) {
	data, err := os.ReadFile("../../testdata/users/users.json")
	if err != nil {
		t.Fatalf("read users.json: %v", err)
	}

	schema, err := parseSchema(data)
	if err != nil {
		t.Fatalf("parseSchema: %v", err)
	}

	// timestamp must always be first
	if len(schema.Columns) == 0 {
		t.Fatal("schema has no columns")
	}
	if schema.Columns[0].Name != "timestamp" || schema.Columns[0].Type != TypeDatetime {
		t.Errorf("column[0]: got {%q, %d}, want {\"timestamp\", TypeDatetime}", schema.Columns[0].Name, schema.Columns[0].Type)
	}
}

func TestParseSchema_UnknownType(t *testing.T) {
	data := []byte(`{"name":"x","orderedColumns":[{"name":"col","type":"uuid"}]}`)
	_, err := parseSchema(data)
	if err == nil {
		t.Fatal("expected error for unknown type, got nil")
	}
}

func TestParseSchema_NoColumns(t *testing.T) {
	data := []byte(`{"name":"x","orderedColumns":[]}`)
	_, err := parseSchema(data)
	if err == nil {
		t.Fatal("expected error for empty columns, got nil")
	}
}

func TestParseSchema_InvalidJSON(t *testing.T) {
	_, err := parseSchema([]byte(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}
