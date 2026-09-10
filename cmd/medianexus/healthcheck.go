package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/makearr/MediaNexus/internal/api/apiserver"
	"github.com/makearr/MediaNexus/internal/db/settings"
	"github.com/makearr/MediaNexus/internal/db/store"
	"github.com/makearr/MediaNexus/internal/platform/clock"
	"github.com/makearr/MediaNexus/internal/platform/config"
	"github.com/makearr/MediaNexus/internal/platform/fsx"
)

// healthcheckTimeout is under the image's HEALTHCHECK timeout, so a slow answer
// reports as unhealthy rather than being cut off mid-request.
const healthcheckTimeout = 4 * time.Second

// runHealthcheck is what the image's HEALTHCHECK runs. It is a subcommand of
// the same static binary, so the image needs no curl or wget (section 20.2).
func runHealthcheck(args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	flags.SetOutput(errOut)
	configPath := flags.String("config", defaultConfigPath, "path to config.yml")
	if err := flags.Parse(args); err != nil {
		return err
	}

	loaded, err := config.Load(fsx.NewOS(), *configPath)
	if err != nil && !errors.Is(err, config.ErrNotFound) {
		return err
	}

	endpoint := healthEndpoint(loaded, resolvedHealthcheckPort(loaded))
	client := &http.Client{Timeout: healthcheckTimeout} //nolint:forbidigo // a probe against ourselves, not an outbound provider

	response, err := client.Get(endpoint)
	if err != nil {
		return fmt.Errorf("healthcheck: %s: %w", endpoint, err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		return fmt.Errorf("healthcheck: %s returned %d: %s",
			endpoint, response.StatusCode, strings.TrimSpace(string(body)))
	}
	_, err = fmt.Fprintln(out, "healthy")
	return err
}

// healthEndpoint builds the URL from the same configuration the server binds with, so a non-
// default port or base URL does not silently break the probe.
func healthEndpoint(loaded config.Config, port int) string {
	host := loaded.Server.Bind
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::", "[::]":
		host = "::1"
	}

	base := strings.TrimSuffix(loaded.Server.BaseURL, "/")
	return (&url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(host, fmt.Sprint(port)),
		Path:   base + apiserver.APIPrefix + "/health",
	}).String()
}

// resolvedHealthcheckPort re-derives ADR-58's order from scratch, or a port
// change from the Network screen would make this probe the wrong one.
func resolvedHealthcheckPort(loaded config.Config) int {
	if _, envSet := os.LookupEnv(config.EnvName("server.port")); envSet {
		return loaded.Server.Port
	}

	settingsPath := filepath.Join(loaded.Paths.DBDir, "settings.db")
	if _, err := fsx.NewOS().Stat(settingsPath); err != nil {
		return loaded.Server.Port
	}

	ctx, cancel := context.WithTimeout(context.Background(), healthcheckTimeout)
	defer cancel()

	opened, err := store.Open(ctx, clock.NewSystem(), store.Config{Name: "settings", Path: settingsPath, ReadPoolSize: 1})
	if err != nil {
		return loaded.Server.Port
	}
	defer func() { _ = opened.Close() }()

	cache, err := settings.NewCache(ctx, opened)
	if err != nil {
		return loaded.Server.Port
	}
	if stored := cache.Snapshot().NetworkSettings().Port; stored != 0 {
		return stored
	}
	return loaded.Server.Port
}
