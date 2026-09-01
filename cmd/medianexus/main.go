// Command medianexus is the single static binary: server, CLI and healthcheck
// (ADR-1, §20.1). It contains wiring only — every decision lives in internal/.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// Injected with -ldflags -X at build time; see documentation/versioning.md.
// An unknown commit is reported as unknown rather than as a plausible-looking
// wrong value, because the About page's source link depends on it (AGPL §13).
var (
	version   = "0.0.0-dev"
	commit    = "unknown"
	buildDate = "unknown"
)

// errNotImplemented keeps an unfinished command honest: it fails loudly rather
// than starting something that does nothing.
var errNotImplemented = errors.New("not implemented yet — stage 1 is in progress")

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "medianexus: %v\n", err) //nolint:errcheck // stderr on the way out
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	command := "serve"
	if len(args) > 0 {
		command = args[0]
	}

	switch command {
	case "version":
		_, err := fmt.Fprintf(out, "medianexus %s\ncommit     %s\nbuilt      %s\n", version, commit, buildDate)
		return err
	case "serve":
		return fmt.Errorf("serve: %w", errNotImplemented)
	default:
		return fmt.Errorf("unknown command %q; known commands: serve, version", command)
	}
}
