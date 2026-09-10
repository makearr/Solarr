package app

import (
	"context"
	"log/slog"

	"github.com/makearr/MediaNexus/internal/api/apiserver"
	"github.com/makearr/MediaNexus/internal/db/settings"
	"github.com/makearr/MediaNexus/internal/platform/clock"
	"github.com/makearr/MediaNexus/internal/platform/config"
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

// defaultQualityProfileName is the permissive, no-op profile
// ensureDefaultQualityProfile seeds: no hard gates, no tiers, no targets --
// every candidate passes and nothing is ever scored an upgrade over another.
// It exists only so a scan can write a schema-valid desired_item.profile_id
// for a category nobody has configured a real profile for yet (todo 4.4);
// picking a real quality profile from several is 4.3's follow-up UI, not this.
const defaultQualityProfileName = "Unconfigured (accepts anything)"

// ensureDefaultQualityProfile creates the fallback profile discovery-created
// desired items use when their category has none of its own configured, the
// same "create once, on the first boot that needs it" shape as
// ensureOpenAccessAdministrator.
func (p *process) ensureDefaultQualityProfile(ctx context.Context) error {
	for _, profile := range p.cache.Snapshot().QualityProfiles() {
		if profile.Name == defaultQualityProfileName {
			p.defaultQualityProfileID = profile.ID
			return nil
		}
	}
	created, err := p.cache.CreateQualityProfile(ctx, defaultQualityProfileName, "{}", "{}")
	if err != nil {
		return err
	}
	p.defaultQualityProfileID = created.ID
	return nil
}
