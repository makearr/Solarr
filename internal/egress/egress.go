// Package egress reports the address this host appears to connect from. It is
// the only outbound call MediaNexus makes without being asked, which is why it
// is switchable off and names the service that answered.
package egress

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/httpx"
)

// maxResponse bounds what an echo service can make this process read. An address
// is at most 45 characters; anything larger is not an answer to this question.
const maxResponse = 128

// Status is the last thing the check learned. Every field is optional: a check
// that is off, has not run, or failed reports nothing rather than a stale or
// invented address.
type Status struct {
	Enabled   bool
	PublicIP  string
	Source    string
	CheckedAt time.Time
	Err       string
}

// Prober asks an echo service, on an interval, and caches what it hears.
type Prober struct {
	client   *httpx.Client
	clock    clock.Clock
	logger   *slog.Logger
	endpoint string
	interval time.Duration
	enabled  bool

	mu     sync.RWMutex
	status Status
}

// Config describes the check.
type Config struct {
	Enabled  bool
	Endpoint string
	Interval time.Duration
	Client   *httpx.Client
	Clock    clock.Clock
	Logger   *slog.Logger
}

// New builds a prober. A disabled one is still usable and simply reports that.
func New(config Config) *Prober {
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	if config.Interval <= 0 {
		config.Interval = 6 * time.Hour
	}
	return &Prober{
		client:   config.Client,
		clock:    config.Clock,
		logger:   config.Logger,
		endpoint: config.Endpoint,
		interval: config.Interval,
		enabled:  config.Enabled && config.Client != nil && config.Endpoint != "",
		status:   Status{Enabled: config.Enabled},
	}
}

// Status reports the cached result.
func (p *Prober) Status() Status {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.status
}

// Run checks once and then on the interval, until the context is done. A failure
// is recorded and retried on the next tick rather than escalated: not knowing the
// public address stops nothing.
func (p *Prober) Run(ctx context.Context) {
	if !p.enabled {
		return
	}

	p.checkOnce(ctx)
	ticker := p.clock.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
			p.checkOnce(ctx)
		}
	}
}

func (p *Prober) checkOnce(ctx context.Context) {
	address, err := p.ask(ctx)

	p.mu.Lock()
	defer p.mu.Unlock()
	p.status.Enabled = true
	p.status.CheckedAt = p.clock.Now()
	p.status.Source = p.endpoint
	if err != nil {
		// The previous address is kept: it is more likely still true than not,
		// and the error says the reading is stale.
		p.status.Err = err.Error()
		p.logger.DebugContext(ctx, "public ip check failed", slog.Any("error", err))
		return
	}
	p.status.PublicIP = address
	p.status.Err = ""
}

func (p *Prober) ask(ctx context.Context) (string, error) {
	response, err := p.client.Get(ctx, p.endpoint)
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("egress: %s answered %s", p.endpoint, response.Status)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponse))
	if err != nil {
		return "", err
	}

	// Parsed rather than trusted: this is a third party's response rendered in
	// the interface, so it is an address or it is nothing.
	text := strings.TrimSpace(string(body))
	parsed, err := netip.ParseAddr(text)
	if err != nil {
		return "", errors.New("egress: the reply was not an IP address")
	}
	return parsed.String(), nil
}
