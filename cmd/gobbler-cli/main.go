package main

import (
	"fmt"
	"os"
	"strings"
)

const topLevelHelp = `Gobbler Query Language CLI

gq — query Gobbler data sources using GQL

Usage:
  gq [--catalog <path>] <noun> <verb> [args] [flags]

Global Flags:
  --catalog <path>   catalog file to use (default: ~/.gobbler/catalog.json)
                     a .gobbler.json in the current directory takes precedence

Nouns:
  catalog   manage table registrations
  query     execute GQL queries

Run 'gq <noun> --help' for the list of commands in each noun.`

func main() {
	rest, catalogOverride := stripGlobalFlags(os.Args[1:])

	if len(rest) == 0 || rest[0] == "--help" || rest[0] == "-h" {
		fmt.Println(topLevelHelp)
		os.Exit(0)
	}

	noun := rest[0]
	nounArgs := rest[1:]

	switch noun {
	case "catalog":
		runCatalog(nounArgs, catalogOverride)
	case "query":
		runQuery(nounArgs, catalogOverride)
	default:
		fmt.Fprintf(os.Stderr, "gq: unknown noun %q\nRun 'gq --help' for usage.\n", noun)
		os.Exit(1)
	}
}

// stripGlobalFlags removes --catalog <path> (and --catalog=<path>) from args
// wherever they appear and returns the remaining args with the extracted value.
func stripGlobalFlags(args []string) (rest []string, catalogOverride string) {
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--catalog" && i+1 < len(args):
			catalogOverride = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--catalog="):
			catalogOverride = strings.TrimPrefix(args[i], "--catalog=")
		default:
			rest = append(rest, args[i])
		}
	}
	return
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "gq:", err)
	os.Exit(1)
}

// separatePosAndFlags splits args into positional arguments and flag arguments.
// This allows positional args to appear anywhere relative to flags, e.g.:
//
//	gq catalog add <table> --dir <path>   (positional before flag)
//	gq catalog add --dir <path> <table>   (positional after flag)
//
// The heuristic: if an arg starts with "-" it is a flag; for flags without "="
// the immediately following non-"-" arg is consumed as the flag's value.
// "--help" and "-h" are treated as self-contained (no value consumed).
func separatePosAndFlags(args []string) (positionals, flags []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positionals = append(positionals, arg)
			continue
		}
		flags = append(flags, arg)
		// Self-contained forms: --help/-h or --flag=value
		if arg == "--help" || arg == "-h" || strings.Contains(arg, "=") {
			continue
		}
		// Consume next arg as the flag's value if it doesn't start with "-".
		if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			flags = append(flags, args[i+1])
			i++
		}
	}
	return
}
