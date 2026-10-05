// Command spoor is the single-binary LLM observability system: OTLP
// ingestion, web UI, and the CLI, all in one process.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
)

// version is set at release time: -ldflags "-X main.version=1.2.3". A
// `go install …@vX.Y.Z` build has no ldflags; its module version is in the build info.
var version = "dev"

func versionString() string {
	if info, ok := debug.ReadBuildInfo(); version == "dev" && ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return strings.TrimPrefix(info.Main.Version, "v")
	}
	return version
}

var commands = map[string]func(args []string) error{
	"serve": runServe, "migrate": runMigrate,
	"export": runExport, "demo": runDemo, "reprice": runReprice,
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	run, ok := commands[os.Args[1]]
	switch os.Args[1] {
	case "-h", "--help", "help":
		usage()
		return
	case "-v", "--version", "version":
		fmt.Println("spoor", versionString())
		return
	}
	if !ok {
		fmt.Fprintf(os.Stderr, "spoor: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(1)
	}
	if err := run(os.Args[2:]); err != nil && !errors.Is(err, flag.ErrHelp) { // -h already printed the usage
		fmt.Fprintln(os.Stderr, "spoor:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `spoor is a single-binary, self-hostable LLM observability system.

Usage:

	spoor <command> [flags]

Commands:

	demo       start spoor with real sample traces loaded: no setup, no env vars
	serve      run the OTLP ingestion and web UI servers
	migrate    apply pending database schema migrations
	export     write a trace or a session as JSONL or one shareable HTML file
	reprice    recompute stored span costs from the current model prices
	version    print the version (also --version)

Use "spoor <command> -h" for flags of a specific command.
`)
}
