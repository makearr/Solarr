package main

import (
	"context"
	"log/slog"

	"time"

	"github.com/makearr/MediaNexus/internal/apiserver"
	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/config"
	"github.com/makearr/MediaNexus/internal/egress"
	"github.com/makearr/MediaNexus/internal/httpx"
	"github.com/makearr/MediaNexus/internal/settings"
)

// openAccessUsername owns an install that asks for no credential.
const openAccessUsername = "administrator"

// announceOpenAccess says, on every boot, that this instance is unprotected and
// on which address. Someone who set auth_mode none and forgot has one place the
// fact is guaranteed to appear.
func (p *process) announceOpenAccess() {
	if p.config.Security.AuthMode != config.AuthNone {
		return
	}
	p.logger.Warn("authentication is disabled; every request is served as the administrator",
		slog.String("auth_mode", string(config.AuthNone)),
		slog.String("bind", p.config.Server.Bind),
		slog.Int("port", p.config.Server.Port))
}

// ensureOpenAccessAdministrator creates the account everyone is when
// authentication is off. A real row, so user_id on a job or a request still
// names somebody and ADR-27's multi-user work needs no special case. It carries
// no password hash, which is exactly what settings.db already models for an
// account that does not authenticate by password.
func (p *process) ensureOpenAccessAdministrator(ctx context.Context) error {
	if p.config.Security.AuthMode != config.AuthNone {
		return nil
	}
	for _, user := range p.cache.Snapshot().Users() {
		if user.Role == settings.RoleAdmin {
			return nil
		}
	}

	created, err := p.cache.CreateUser(ctx, clock.NewSystem(), openAccessUsername, "", settings.RoleAdmin)
	if err != nil {
		return err
	}
	p.logger.Warn("created a passwordless administrator because authentication is disabled",
		slog.String("username", created.Username))
	return nil
}

// egressStatus adapts the prober to the interface apiserver defines, so neither
// package has to know about the other.
type egressStatus struct {
	prober *egress.Prober
}

func (e egressStatus) Status() apiserver.EgressStatus {
	status := e.prober.Status()
	return apiserver.EgressStatus{
		Enabled:   status.Enabled,
		PublicIP:  status.PublicIP,
		Source:    status.Source,
		CheckedAt: status.CheckedAt,
		Err:       status.Err,
	}
}

// publicIPClient is the one outbound client the process builds for itself. Every
// other client belongs to a provider and is built where that provider is
// configured (ADR-13).
func (p *process) publicIPClient() *httpx.Client {
	wall := clock.NewSystem()
	// Nil means the system resolver and dialer; the SSRF check still applies.
	guard := httpx.NewGuard(nil, nil, nil)
	return httpx.NewClient(wall, guard, httpx.NewLimiter(wall, httpx.Config{}, nil),
		httpx.NewBreaker(wall, httpx.BreakerConfig{}), httpx.ClientConfig{
			Provider:  "public-ip",
			UserAgent: httpx.UserAgent(version),
			Timeout:   10 * time.Second,
		})
}
