package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/kozwoj/gobbler-query/api"
	"github.com/kozwoj/gobbler-query/query/ast"
	"github.com/kozwoj/gobbler-query/query/parser"
)

const queryNounHelp = "\x1b[1;31mgq query — execute GQL queries\x1b[0m" + `

Commands:
  run   execute a GQL query

Run 'gq query <verb> --help' for full usage of each command.`

const queryRunHelp = "\x1b[1;31mgq query run — execute a GQL query\x1b[0m" + `

Usage:
  gq query run "<gql>" [flags]
  gq query run --file <path.gql> [flags]

Flags:
  --file <path>                   read query from file instead of inline argument
  --format table|csv|jsonl|json   output format (default: auto-detect)
  --out <file>                    write output to file instead of stdout

Output Formats:
  table   aligned columns with header (auto-selected when stdout is a terminal)
  csv     comma-separated with header row (auto-selected when stdout is piped)
  jsonl   one JSON object per line
  json    JSON array of objects

Account Keys:
  Blob-mode tables require GOBBLER_KEY_<ACCOUNT> to be set.
  Only accounts referenced by the query are checked.
  Example (PowerShell): $env:GOBBLER_KEY_GOBBLERSTORAGE = "sv=2023-..."
  Example (bash):       export GOBBLER_KEY_GOBBLERSTORAGE="sv=2023-..."
`

type outputFormat int

const (
	formatTable outputFormat = iota
	formatCSV
	formatJSONL
	formatJSON
)

func runQuery(args []string, catalogOverride string) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Println(queryNounHelp)
		os.Exit(0)
	}
	verb := args[0]
	rest := args[1:]
	switch verb {
	case "run":
		cmdQueryRun(rest, catalogOverride)
	default:
		fmt.Fprintf(os.Stderr, "gq query: unknown verb %q\nRun 'gq query --help' for usage.\n", verb)
		os.Exit(1)
	}
}

func cmdQueryRun(args []string, catalogOverride string) {
	fs := flag.NewFlagSet("gq query run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() { fmt.Print(queryRunHelp) }
	filePath := fs.String("file", "", "")
	formatFlag := fs.String("format", "", "")
	outPath := fs.String("out", "", "")

	positionals, flags := separatePosAndFlags(args)
	if err := fs.Parse(flags); err != nil {
		if err == flag.ErrHelp {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "gq query run: %v\nRun 'gq query run --help' for usage.\n", err)
		os.Exit(1)
	}

	// Resolve query string.
	var queryStr string
	switch {
	case *filePath != "":
		data, err := os.ReadFile(*filePath)
		if err != nil {
			fatal(err)
		}
		queryStr = string(data)
	case len(positionals) >= 1:
		queryStr = positionals[0]
	default:
		fmt.Fprintln(os.Stderr, "gq query run: missing query argument")
		fmt.Fprintln(os.Stderr, "Run 'gq query run --help' for usage.")
		os.Exit(1)
	}

	// Parse to find referenced tables.
	parsed, err := parser.Parse(queryStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gq: parse error: %v\n", err)
		os.Exit(1)
	}
	tableNames := referencedTables(parsed)

	// Load catalog.
	catalogPath, err := resolveCatalogPath(catalogOverride)
	if err != nil {
		fatal(err)
	}
	entries, err := readCatalogFile(catalogPath)
	if err != nil {
		fatal(err)
	}
	entryMap := make(map[string]CatalogEntry, len(entries))
	for _, e := range entries {
		entryMap[e.Table] = e
	}

	// Validate: all referenced tables must be registered; blob tables need keys.
	for _, name := range tableNames {
		e, ok := entryMap[name]
		if !ok {
			fmt.Fprintf(os.Stderr, "gq: table %q is not in the catalog\n", name)
			os.Exit(1)
		}
		if strings.ToLower(e.Mode) == "blob" {
			keyVar := "GOBBLER_KEY_" + strings.ToUpper(e.Account)
			if os.Getenv(keyVar) == "" {
				fmt.Fprintf(os.Stderr, "gq: account key for %q not set; set %s=<key>\n", e.Account, keyVar)
				os.Exit(1)
			}
		}
	}

	// Build runtime catalog (only the tables actually referenced).
	wantTables := make(map[string]bool, len(tableNames))
	for _, n := range tableNames {
		wantTables[n] = true
	}
	cat, err := buildRuntimeCatalog(entries, wantTables)
	if err != nil {
		fatal(err)
	}

	// Execute.
	result, err := api.Execute(queryStr, cat, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gq: %v\n", err)
		os.Exit(1)
	}

	// Resolve output format.
	outFmt := resolveFormat(*formatFlag)

	// Open output destination.
	var w io.Writer = os.Stdout
	if *outPath != "" {
		f, err := os.Create(*outPath)
		if err != nil {
			fatal(err)
		}
		defer f.Close()
		w = f
	}

	if err := writeResult(w, result, outFmt); err != nil {
		fatal(err)
	}
}

// referencedTables collects all source type names from q and its join sub-queries.
func referencedTables(q *ast.Query) []string {
	seen := make(map[string]bool)
	var walk func(*ast.Query)
	walk = func(q *ast.Query) {
		seen[q.Source.TypeName] = true
		for _, stage := range q.Stages {
			if j, ok := stage.(*ast.JoinStage); ok {
				walk(j.Right)
			}
		}
	}
	walk(q)
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	return names
}

// resolveFormat maps a --format flag value to an outputFormat.
// When the flag is empty, auto-detects based on whether stdout is a terminal.
func resolveFormat(val string) outputFormat {
	switch strings.ToLower(val) {
	case "table":
		return formatTable
	case "csv":
		return formatCSV
	case "jsonl":
		return formatJSONL
	case "json":
		return formatJSON
	case "":
		if isTerminal(os.Stdout) {
			return formatTable
		}
		return formatCSV
	default:
		fmt.Fprintf(os.Stderr, "gq: unknown format %q (must be table|csv|jsonl|json)\n", val)
		os.Exit(1)
		return formatTable // unreachable
	}
}

// isTerminal reports whether f is connected to a terminal.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// writeResult writes the query result in the requested format.
func writeResult(w io.Writer, r *api.Result, format outputFormat) error {
	switch format {
	case formatTable:
		return writeTable(w, r)
	case formatCSV:
		return writeCSV(w, r)
	case formatJSONL:
		return writeJSONL(w, r)
	case formatJSON:
		return writeJSON(w, r)
	}
	return nil
}

func writeTable(w io.Writer, r *api.Result) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	headers := make([]string, len(r.Schema))
	for i, m := range r.Schema {
		headers[i] = strings.ToUpper(m.Name)
	}
	fmt.Fprintln(tw, strings.Join(headers, "\t"))
	for i, row := range r.Rows {
		cells := make([]string, len(row))
		for j, v := range row {
			if !r.Nulls[i][j] {
				cells[j] = formatValue(v)
			}
		}
		fmt.Fprintln(tw, strings.Join(cells, "\t"))
	}
	return tw.Flush()
}

func writeCSV(w io.Writer, r *api.Result) error {
	cw := csv.NewWriter(w)
	header := make([]string, len(r.Schema))
	for i, m := range r.Schema {
		header[i] = m.Name
	}
	if err := cw.Write(header); err != nil {
		return err
	}
	for i, row := range r.Rows {
		record := make([]string, len(row))
		for j, v := range row {
			if !r.Nulls[i][j] {
				record[j] = formatValue(v)
			}
		}
		if err := cw.Write(record); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func writeJSONL(w io.Writer, r *api.Result) error {
	enc := json.NewEncoder(w)
	for i, row := range r.Rows {
		obj := make(map[string]any, len(r.Schema))
		for j, m := range r.Schema {
			if r.Nulls[i][j] {
				obj[m.Name] = nil
			} else {
				obj[m.Name] = jsonValue(row[j])
			}
		}
		if err := enc.Encode(obj); err != nil {
			return err
		}
	}
	return nil
}

func writeJSON(w io.Writer, r *api.Result) error {
	rows := make([]map[string]any, len(r.Rows))
	for i, row := range r.Rows {
		obj := make(map[string]any, len(r.Schema))
		for j, m := range r.Schema {
			if r.Nulls[i][j] {
				obj[m.Name] = nil
			} else {
				obj[m.Name] = jsonValue(row[j])
			}
		}
		rows[i] = obj
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rows)
}

// jsonValue converts time.Time and time.Duration to strings for JSON output;
// all other types are passed through as-is.
func jsonValue(v any) any {
	switch val := v.(type) {
	case time.Time:
		return val.UTC().Format(time.RFC3339Nano)
	case time.Duration:
		return val.String()
	default:
		return v
	}
}

// formatValue returns a string representation of v for table/csv output.
func formatValue(v any) string {
	switch val := v.(type) {
	case int32:
		return strconv.FormatInt(int64(val), 10)
	case int64:
		return strconv.FormatInt(val, 10)
	case float64:
		return strconv.FormatFloat(val, 'g', -1, 64)
	case bool:
		if val {
			return "true"
		}
		return "false"
	case string:
		return val
	case time.Time:
		return val.UTC().Format(time.RFC3339Nano)
	case time.Duration:
		return val.String()
	default:
		return fmt.Sprintf("%v", val)
	}
}
