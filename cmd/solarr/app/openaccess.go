package app

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
)

// seedAuthPolicy writes what config.yml asked for, but only on a first run.
// After that the stored policy is authoritative, so editing config.yml cannot
// quietly reopen an instance that was closed from the interface (ADR-21).
func (p *process) seedAuthPolicy(ctx context.Context) error {
	required := p.config.Security.AuthMode != config.AuthNone
	return p.cache.SeedAuthRequired(ctx, clock.NewSystem(), required)
}

// announceOpenAccess says, on every boot, that this instance is unprotected and
// on which address. Someone who turned authentication off and forgot has one
// place the fact is guaranteed to appear.
func (p *process) announceOpenAccess() {
	if p.cache.Snapshot().AuthRequired() {
		return
	}
	p.logger.Warn("authentication is disabled; every request is served as the administrator",
		slog.String("bind", p.config.Server.Bind),
		slog.Int("port", p.config.Server.Port))
}

// ensureOpenAccessAdministrator creates the account everyone is when
// authentication is off. A real row, so user_id on a job or a request still
// names somebody and ADR-27's multi-user work needs no special case. It carries
// no password hash, which is exactly what settings.db already models for an
// account that does not authenticate by password.
func (p *process) ensureOpenAccessAdministrator(ctx context.Context) error {
	if p.cache.Snapshot().AuthRequired() {
		return nil
	}
	for _, user := range p.cache.Snapshot().Users() {
		if user.Role == settings.RoleAdmin {
			return nil
		}
	}

	created, err := p.cache.CreateUser(ctx, clock.NewSystem(), apiserver.OpenAccessUsername, "", settings.RoleAdmin)
	if err != nil {
		return err
	}
	p.logger.Warn("created a passwordless administrator because authentication is disabled",
		slog.String("username", created.Username))
	return nil
}

// shippedDefaultQualityProfileName is migration 0012's profile that, like Lidarr's default,
// accepts anything and prefers lossless; without a list, a library searches by it.
const shippedDefaultQualityProfileName = "Lossless preferred"

// defaultQualityProfileName is the no-op fallback, only created when the shipped one was deleted.
const defaultQualityProfileName = "Unconfigured (accepts anything)"

// ensureDefaultQualityProfile picks the profile desired items get when their library lists none.
func (p *process) ensureDefaultQualityProfile(ctx context.Context) error {
	for _, name := range []string{shippedDefaultQualityProfileName, defaultQualityProfileName} {
		for _, profile := range p.cache.Snapshot().QualityProfiles() {
			if profile.Name == name {
				p.defaultQualityProfileID = profile.ID
				return nil
			}
		}
	}
	created, err := p.cache.CreateQualityProfile(ctx, defaultQualityProfileName, "{}", "{}")
	if err != nil {
		return err
	}
	p.defaultQualityProfileID = created.ID
	return nil
}
