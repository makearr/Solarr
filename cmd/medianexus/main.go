// Command medianexus is the single static binary: server, CLI and healthcheck
// (ADR-1, §20.1). It contains wiring only — every decision lives in internal/.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/makearr/MediaNexus/internal/config"
	"github.com/makearr/MediaNexus/internal/fsx"
)

// Injected with -ldflags -X at build time; see documentation/versioning.md.
// An unknown commit is reported as unknown rather than as a plausible-looking
// wrong value, because the About page's source link depends on it (AGPL §13).
var (
	version   = "0.0.0-dev"
	commit    = "unknown"
	buildDate = "unknown"
)

// defaultConfigPath matches the single /config volume the image expects (§20.2).
const defaultConfigPath = "/config/config.yml"

// errNotImplemented keeps an unfinished command honest: it fails loudly rather
// than starting something that does nothing.
var errNotImplemented = errors.New("not implemented yet — stage 1 is in progress")

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "medianexus: %v\n", err) //nolint:errcheck // stderr on the way out
		os.Exit(1)
	}
}

func run(args []string, out, errOut io.Writer) error {
	command := "serve"
	if len(args) > 0 {
		command = args[0]
	}

	switch command {
	case "version":
		_, err := fmt.Fprintf(out, "medianexus %s\ncommit     %s\nbuilt      %s\n", version, commit, buildDate)
		return err
	case "config":
		return runConfig(args[1:], out, errOut)
	case "serve":
		return fmt.Errorf("serve: %w", errNotImplemented)
	default:
		return fmt.Errorf("unknown command %q; known commands: config, serve, version", command)
	}
}

func runConfig(args []string, out, errOut io.Writer) error {
	if len(args) == 0 {
		return errors.New("config: expected a subcommand; known subcommands: check")
	}
	if args[0] != "check" {
		return fmt.Errorf("config: unknown subcommand %q; known subcommands: check", args[0])
	}

	flags := flag.NewFlagSet("config check", flag.ContinueOnError)
	flags.SetOutput(errOut)
	path := flags.String("config", defaultConfigPath, "path to config.yml")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}

	return checkConfig(*path, out)
}

// checkConfig runs the same validation startup runs and exits, so configuration
// can be verified without starting the service — a container pre-flight, or a
// CI step (§14.1).
func checkConfig(path string, out io.Writer) error {
	fsys := fsx.NewOS()

	loaded, err := config.Load(fsys, path)
	switch {
	case errors.Is(err, config.ErrNotFound):
		// A first run writes one from the defaults, so this is not a failure —
		// but saying so beats silently checking something that is not there.
		if _, writeErr := fmt.Fprintf(out, "no config.yml at %s; the defaults below would be used\n\n", path); writeErr != nil {
			return writeErr
		}
	case err != nil:
		return err
	}

	problems := config.CheckDirectories(fsys, loaded)
	if err := problems.OrNil(); err != nil {
		return err
	}

	_, err = fmt.Fprintf(out,
		"configuration is valid\n"+
			"  config     %s\n"+
			"  databases  %s\n"+
			"  backups    %s\n"+
			"  cache      %s\n"+
			"  listening  %s\n",
		path, loaded.Paths.DBDir, loaded.Paths.BackupDir, loaded.Paths.CacheDir, listenAddress(loaded))
	return err
}

func listenAddress(loaded config.Config) string {
	return fmt.Sprintf("%s:%d%s", loaded.Server.Bind, loaded.Server.Port,
		filepath.Clean("/"+loaded.Server.BaseURL))
}
