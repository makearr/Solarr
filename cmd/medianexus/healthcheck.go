package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/makearr/MediaNexus/internal/api/apiserver"
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

	endpoint := healthEndpoint(loaded)
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
func healthEndpoint(loaded config.Config) string {
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
		Host:   net.JoinHostPort(host, fmt.Sprint(loaded.Server.Port)),
		Path:   base + apiserver.APIPrefix + "/health",
	}).String()
}
