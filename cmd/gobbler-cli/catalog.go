package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
)

const catalogNounHelp = `gq catalog — manage table registrations

Commands:
  add <table>     register a table (file mode or blob mode)
  remove <table>  deregister a table
  list            print all registered tables
  show <table>    print a single entry's details
  load <file>     merge entries from a snapshot file into the catalog
  export          write the current catalog to a snapshot file

Run 'gq catalog <verb> --help' for full usage of each command.`

func runCatalog(args []string, catalogOverride string) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Println(catalogNounHelp)
		os.Exit(0)
	}
	verb := args[0]
	rest := args[1:]
	switch verb {
	case "add":
		cmdCatalogAdd(rest, catalogOverride)
	case "remove":
		cmdCatalogRemove(rest, catalogOverride)
	case "list":
		cmdCatalogList(rest, catalogOverride)
	case "show":
		cmdCatalogShow(rest, catalogOverride)
	case "load":
		cmdCatalogLoad(rest, catalogOverride)
	case "export":
		cmdCatalogExport(rest, catalogOverride)
	default:
		fmt.Fprintf(os.Stderr, "gq catalog: unknown verb %q\nRun 'gq catalog --help' for usage.\n", verb)
		os.Exit(1)
	}
}

// ── add ───────────────────────────────────────────────────────────────────────

const catalogAddHelp = `gq catalog add — register a table

Usage:
  gq catalog add <table> --dir <path>
  gq catalog add <table> --account <name> --container <name>

Flags:
  --dir <path>         directory containing the table's CSV files (file mode)
  --account <name>     Azure storage account name (blob mode)
  --container <name>   Azure blob container name (blob mode)
`

func cmdCatalogAdd(args []string, catalogOverride string) {
	fs := flag.NewFlagSet("gq catalog add", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() { fmt.Print(catalogAddHelp) }
	dir := fs.String("dir", "", "")
	account := fs.String("account", "", "")
	container := fs.String("container", "", "")

	positionals, flags := separatePosAndFlags(args)
	if err := fs.Parse(flags); err != nil {
		if err == flag.ErrHelp {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "gq catalog add: %v\nRun 'gq catalog add --help' for usage.\n", err)
		os.Exit(1)
	}
	if len(positionals) < 1 {
		fmt.Fprintln(os.Stderr, "gq catalog add: missing <table> argument")
		fmt.Fprintln(os.Stderr, "Run 'gq catalog add --help' for usage.")
		os.Exit(1)
	}

	table := positionals[0]
	var entry CatalogEntry
	entry.Table = table

	switch {
	case *dir != "":
		entry.Mode = "file"
		entry.Dir = *dir
	case *account != "" || *container != "":
		entry.Mode = "blob"
		entry.Account = *account
		entry.Container = *container
	default:
		fmt.Fprintln(os.Stderr, "gq catalog add: specify --dir (file mode) or --account + --container (blob mode)")
		fmt.Fprintln(os.Stderr, "Run 'gq catalog add --help' for usage.")
		os.Exit(1)
	}

	if err := validateEntry(entry); err != nil {
		fmt.Fprintf(os.Stderr, "gq catalog add: %v\n", err)
		os.Exit(1)
	}

	catalogPath, err := resolveCatalogPath(catalogOverride)
	if err != nil {
		fatal(err)
	}
	entries, err := readCatalogFile(catalogPath)
	if err != nil {
		fatal(err)
	}
	for _, e := range entries {
		if e.Table == table {
			fmt.Fprintf(os.Stderr, "gq catalog add: table %q already exists (use 'gq catalog remove %s' first)\n", table, table)
			os.Exit(1)
		}
	}

	if err := writeCatalogFile(catalogPath, append(entries, entry)); err != nil {
		fatal(err)
	}
	fmt.Printf("added table %q to %s\n", table, catalogPath)
}

// ── remove ────────────────────────────────────────────────────────────────────

const catalogRemoveHelp = `gq catalog remove — deregister a table

Usage:
  gq catalog remove <table>
`

func cmdCatalogRemove(args []string, catalogOverride string) {
	fs := flag.NewFlagSet("gq catalog remove", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() { fmt.Print(catalogRemoveHelp) }

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "gq catalog remove: %v\nRun 'gq catalog remove --help' for usage.\n", err)
		os.Exit(1)
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "gq catalog remove: missing <table> argument")
		fmt.Fprintln(os.Stderr, "Run 'gq catalog remove --help' for usage.")
		os.Exit(1)
	}

	table := fs.Arg(0)
	catalogPath, err := resolveCatalogPath(catalogOverride)
	if err != nil {
		fatal(err)
	}
	entries, err := readCatalogFile(catalogPath)
	if err != nil {
		fatal(err)
	}

	var newEntries []CatalogEntry
	found := false
	for _, e := range entries {
		if e.Table == table {
			found = true
		} else {
			newEntries = append(newEntries, e)
		}
	}
	if !found {
		fmt.Fprintf(os.Stderr, "gq catalog remove: table %q not found\n", table)
		os.Exit(1)
	}
	if err := writeCatalogFile(catalogPath, newEntries); err != nil {
		fatal(err)
	}
	fmt.Printf("removed table %q from %s\n", table, catalogPath)
}

// ── list ──────────────────────────────────────────────────────────────────────

const catalogListHelp = `gq catalog list — print all registered tables

Usage:
  gq catalog list
`

func cmdCatalogList(args []string, catalogOverride string) {
	fs := flag.NewFlagSet("gq catalog list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() { fmt.Print(catalogListHelp) }

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			os.Exit(0)
		}
		os.Exit(1)
	}

	catalogPath, err := resolveCatalogPath(catalogOverride)
	if err != nil {
		fatal(err)
	}
	entries, err := readCatalogFile(catalogPath)
	if err != nil {
		fatal(err)
	}

	if len(entries) == 0 {
		fmt.Println("(no tables registered)")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "TABLE\tMODE\tLOCATION")
	for _, e := range entries {
		loc := e.Dir
		if e.Mode == "blob" {
			loc = e.Account + "/" + e.Container
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", e.Table, e.Mode, loc)
	}
	w.Flush()
}

// ── show ──────────────────────────────────────────────────────────────────────

const catalogShowHelp = `gq catalog show — print a single entry's details

Usage:
  gq catalog show <table>
`

func cmdCatalogShow(args []string, catalogOverride string) {
	fs := flag.NewFlagSet("gq catalog show", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() { fmt.Print(catalogShowHelp) }

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "gq catalog show: %v\nRun 'gq catalog show --help' for usage.\n", err)
		os.Exit(1)
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "gq catalog show: missing <table> argument")
		fmt.Fprintln(os.Stderr, "Run 'gq catalog show --help' for usage.")
		os.Exit(1)
	}

	table := fs.Arg(0)
	catalogPath, err := resolveCatalogPath(catalogOverride)
	if err != nil {
		fatal(err)
	}
	entries, err := readCatalogFile(catalogPath)
	if err != nil {
		fatal(err)
	}

	for _, e := range entries {
		if e.Table != table {
			continue
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintf(w, "Table:\t%s\n", e.Table)
		fmt.Fprintf(w, "Mode:\t%s\n", e.Mode)
		if e.Mode == "file" {
			fmt.Fprintf(w, "Directory:\t%s\n", e.Dir)
		} else {
			fmt.Fprintf(w, "Account:\t%s\n", e.Account)
			fmt.Fprintf(w, "Container:\t%s\n", e.Container)
		}
		w.Flush()
		return
	}

	fmt.Fprintf(os.Stderr, "gq catalog show: table %q not found\n", table)
	os.Exit(1)
}

// ── load ──────────────────────────────────────────────────────────────────────

const catalogLoadHelp = `gq catalog load — merge entries from a snapshot file into the catalog

Usage:
  gq catalog load <file>

If any table name in the snapshot already exists in the catalog, the entire
load fails and the catalog file is left unchanged. Use 'gq catalog remove'
to clear a conflicting entry first.
`

func cmdCatalogLoad(args []string, catalogOverride string) {
	fs := flag.NewFlagSet("gq catalog load", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() { fmt.Print(catalogLoadHelp) }

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "gq catalog load: %v\nRun 'gq catalog load --help' for usage.\n", err)
		os.Exit(1)
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "gq catalog load: missing <file> argument")
		fmt.Fprintln(os.Stderr, "Run 'gq catalog load --help' for usage.")
		os.Exit(1)
	}

	file := fs.Arg(0)
	incoming, err := readCatalogFile(file)
	if err != nil {
		fatal(err)
	}
	if incoming == nil {
		fmt.Fprintf(os.Stderr, "gq catalog load: file not found: %s\n", file)
		os.Exit(1)
	}

	// Validate all incoming entries before touching the catalog.
	for _, e := range incoming {
		if err := validateEntry(e); err != nil {
			fmt.Fprintf(os.Stderr, "gq catalog load: %v\n", err)
			os.Exit(1)
		}
	}

	catalogPath, err := resolveCatalogPath(catalogOverride)
	if err != nil {
		fatal(err)
	}
	existing, err := readCatalogFile(catalogPath)
	if err != nil {
		fatal(err)
	}

	existingNames := make(map[string]bool, len(existing))
	for _, e := range existing {
		existingNames[e.Table] = true
	}
	for _, e := range incoming {
		if existingNames[e.Table] {
			fmt.Fprintf(os.Stderr, "gq catalog load: table %q already exists (use 'gq catalog remove %s' first)\n", e.Table, e.Table)
			os.Exit(1)
		}
	}

	if err := writeCatalogFile(catalogPath, append(existing, incoming...)); err != nil {
		fatal(err)
	}
	fmt.Printf("loaded %d table(s) into %s\n", len(incoming), catalogPath)
}

// ── export ────────────────────────────────────────────────────────────────────

const catalogExportHelp = `gq catalog export — write the current catalog to a snapshot file

Usage:
  gq catalog export [--out <file>]

Without --out, the snapshot JSON is written to stdout.
The exported file can be loaded with 'gq catalog load'.

Flags:
  --out <file>   write output to file instead of stdout
`

func cmdCatalogExport(args []string, catalogOverride string) {
	fs := flag.NewFlagSet("gq catalog export", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() { fmt.Print(catalogExportHelp) }
	out := fs.String("out", "", "")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "gq catalog export: %v\nRun 'gq catalog export --help' for usage.\n", err)
		os.Exit(1)
	}

	catalogPath, err := resolveCatalogPath(catalogOverride)
	if err != nil {
		fatal(err)
	}
	entries, err := readCatalogFile(catalogPath)
	if err != nil {
		fatal(err)
	}
	if entries == nil {
		entries = []CatalogEntry{}
	}

	data, err := marshalEntries(entries)
	if err != nil {
		fatal(err)
	}

	if *out == "" {
		_, _ = os.Stdout.Write(data)
		return
	}
	if err := os.WriteFile(*out, data, 0600); err != nil {
		fatal(err)
	}
	fmt.Printf("exported %d table(s) to %s\n", len(entries), *out)
}
