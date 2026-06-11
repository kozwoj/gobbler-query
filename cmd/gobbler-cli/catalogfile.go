package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kozwoj/gobbler-query/query/catalog"
)

// CatalogEntry is the on-disk representation of one table in the catalog file
// and in snapshot files shared across machines.
type CatalogEntry struct {
	Table     string `json:"table"`
	Mode      string `json:"mode"`                // "file" or "blob"
	Dir       string `json:"dir,omitempty"`       // file mode only
	Account   string `json:"account,omitempty"`   // blob mode only
	Container string `json:"container,omitempty"` // blob mode only
}

// resolveCatalogPath returns the path of the active catalog file.
//   - override (from --catalog) takes highest precedence
//   - .gobbler.json in the current directory is next
//   - <home>/.gobbler/catalog.json is the default (os.UserHomeDir)
func resolveCatalogPath(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if _, err := os.Stat(".gobbler.json"); err == nil {
		return filepath.Abs(".gobbler.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".gobbler", "catalog.json"), nil
}

// readCatalogFile reads and parses the JSON catalog at path.
// Returns nil (not an error) if the file does not exist.
func readCatalogFile(path string) ([]CatalogEntry, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read catalog %s: %w", path, err)
	}
	var entries []CatalogEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse catalog %s: %w", path, err)
	}
	return entries, nil
}

// writeCatalogFile serialises entries to path, creating parent directories as
// needed. Uses a write-to-temp-then-rename strategy for atomicity.
func writeCatalogFile(path string, entries []CatalogEntry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create catalog directory: %w", err)
	}
	if entries == nil {
		entries = []CatalogEntry{}
	}
	data, err := marshalEntries(entries)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write catalog: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write catalog: %w", err)
	}
	return nil
}

// marshalEntries serialises entries as indented JSON with a trailing newline.
func marshalEntries(entries []CatalogEntry) ([]byte, error) {
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// buildRuntimeCatalog converts CatalogEntry values into a runtime
// catalog.Catalog. Account keys for blob tables are read from
// GOBBLER_KEY_<ACCOUNT> environment variables. If wantTables is non-nil,
// only those table names are included.
func buildRuntimeCatalog(entries []CatalogEntry, wantTables map[string]bool) (catalog.Catalog, error) {
	cat := make(catalog.Catalog)
	for _, e := range entries {
		if wantTables != nil && !wantTables[e.Table] {
			continue
		}
		te, err := entryToTableEntry(e)
		if err != nil {
			return nil, err
		}
		cat[e.Table] = te
	}
	return cat, nil
}

// entryToTableEntry converts a CatalogEntry to a runtime catalog.TableEntry.
// For file mode, the dir field is split into OutputDir and StorageBucket so
// that TableEntry.Resolve() returns the original dir path.
func entryToTableEntry(e CatalogEntry) (*catalog.TableEntry, error) {
	switch strings.ToLower(e.Mode) {
	case "file":
		clean := filepath.Clean(e.Dir)
		return &catalog.TableEntry{
			TypeName:      e.Table,
			Mode:          catalog.StorageModeFile,
			OutputDir:     filepath.Dir(clean),
			StorageBucket: filepath.Base(clean),
		}, nil
	case "blob":
		return &catalog.TableEntry{
			TypeName:      e.Table,
			Mode:          catalog.StorageModeBlob,
			AccountName:   e.Account,
			StorageBucket: e.Container,
			AccountKey:    os.Getenv("GOBBLER_KEY_" + strings.ToUpper(e.Account)),
		}, nil
	default:
		return nil, fmt.Errorf("table %q: unknown mode %q", e.Table, e.Mode)
	}
}

// validateEntry checks that a CatalogEntry has all required fields.
func validateEntry(e CatalogEntry) error {
	if e.Table == "" {
		return fmt.Errorf("entry missing required field \"table\"")
	}
	switch strings.ToLower(e.Mode) {
	case "file":
		if e.Dir == "" {
			return fmt.Errorf("table %q: mode=file requires dir", e.Table)
		}
	case "blob":
		if e.Account == "" {
			return fmt.Errorf("table %q: mode=blob requires account", e.Table)
		}
		if e.Container == "" {
			return fmt.Errorf("table %q: mode=blob requires container", e.Table)
		}
	case "":
		return fmt.Errorf("table %q: missing required field \"mode\"", e.Table)
	default:
		return fmt.Errorf("table %q: unknown mode %q (must be \"file\" or \"blob\")", e.Table, e.Mode)
	}
	return nil
}
