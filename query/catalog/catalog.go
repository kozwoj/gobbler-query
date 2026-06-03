package catalog

import (
	"fmt"
	"path/filepath"
)

// StorageMode identifies whether a table's data lives on the local filesystem
// or in Azure Blob Storage.
type StorageMode int

const (
	StorageModeFile StorageMode = iota
	StorageModeBlob
)

// TableEntry describes where one query table's data lives in storage.
// StorageBucket is pre-resolved from the item definition's "folder" property
// (or the type name when "folder" is unset). The engine never sees that
// indirection — it works only with the resolved name.
type TableEntry struct {
	TypeName      string // query-visible table name (equals key in Catalog)
	StorageBucket string // subdirectory name (file) or container name (blob)
	Mode          StorageMode

	// File mode only.
	OutputDir string

	// Blob mode only.
	AccountName string
	AccountKey  string // never serialised into the logical or physical plan
}

// Resolve returns the fully-qualified path (file mode) or URL (blob mode)
// for this entry's storage bucket.
func (e *TableEntry) Resolve() (string, error) {
	switch e.Mode {
	case StorageModeFile:
		return filepath.Join(e.OutputDir, e.StorageBucket), nil
	case StorageModeBlob:
		return fmt.Sprintf("https://%s.blob.core.windows.net/%s",
			e.AccountName, e.StorageBucket), nil
	default:
		return "", fmt.Errorf("unknown storage mode %d", e.Mode)
	}
}

// Catalog maps query-visible table names to their storage locations.
// Key: item type name (the "name" field in the item definition).
// Value: pre-resolved storage entry.
type Catalog map[string]*TableEntry
