package app

import (
	"context"
	"fmt"
	"net/netip"
	"path/filepath"
	"strings"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
	"github.com/makearr/Solarr/internal/platform/secrets"
)

// Environment variables that write into settings.db at startup (documentation/environment-variables.md).
const (
	envRootFolders = "SOLARR_ROOT_FOLDERS"
	envProxyURL    = "SOLARR_PROXY_URL"
)

// providerKeyEnv is the variable that supplies a provider's API key, e.g. SOLARR_TMDB_API_KEY.
func providerKeyEnv(provider string) string {
	return config.EnvPrefix + strings.ToUpper(provider) + "_API_KEY"
}

// applyEnvSettings makes settings.db agree with the environment: a variable that is set wins over
// the stored value and rewrites it, one that is unset leaves the stored value alone.
func (p *process) applyEnvSettings(ctx context.Context) error {
	wall := clock.NewSystem()

	if err := p.applyProviderKeys(ctx, wall); err != nil {
		return err
	}
	if err := p.applyRootFolders(ctx); err != nil {
		return err
	}
	return p.applyNetworkEnv(ctx, wall)
}

func (p *process) applyProviderKeys(ctx context.Context, wall clock.Clock) error {
	for _, provider := range settings.ProviderKeyProviders() {
		name := providerKeyEnv(provider)
		value, set := p.env(name)
		if !set || strings.TrimSpace(value) == "" {
			continue
		}
		key := secrets.Secret(strings.TrimSpace(value))

		stored, err := p.cache.ProviderKey(provider)
		if err == nil && stored == key {
			continue
		}
		if err := p.cache.SetProviderKey(ctx, wall, provider, key); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		p.logger.Info("api key set from the environment", "provider", provider, "variable", name)
	}
	return nil
}

// applyRootFolders adds every folder SOLARR_ROOT_FOLDERS names that is not yet a root folder.
// It never removes one: a root folder owns library history that an unset variable must not erase.
func (p *process) applyRootFolders(ctx context.Context) error {
	value, set := p.env(envRootFolders)
	if !set {
		return nil
	}
	folders, err := parseRootFolders(value)
	if err != nil {
		return fmt.Errorf("%s: %w", envRootFolders, err)
	}

	known := make(map[string]bool)
	for _, root := range p.cache.Snapshot().RootFolders() {
		known[root.Path] = true
	}
	for _, folder := range folders {
		if known[folder.path] {
			continue
		}
		if _, err := p.cache.CreateRootFolder(ctx, folder.path, folder.label); err != nil {
			return fmt.Errorf("%s: %w", envRootFolders, err)
		}
		known[folder.path] = true
		p.logger.Info("root folder added from the environment", "path", folder.path, "variable", envRootFolders)
	}
	return nil
}

type envRootFolder struct{ path, label string }

// parseRootFolders reads "/media/music,/media/tv" or "Music=/media/music,TV=/media/tv".
func parseRootFolders(value string) ([]envRootFolder, error) {
	var folders []envRootFolder
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		folder := envRootFolder{path: part}
		if label, path, labelled := strings.Cut(part, "="); labelled && !strings.HasPrefix(part, "/") {
			folder = envRootFolder{path: strings.TrimSpace(path), label: strings.TrimSpace(label)}
		}
		if !filepath.IsAbs(folder.path) {
			return nil, fmt.Errorf("%q is not an absolute path", folder.path)
		}
		folder.path = filepath.Clean(folder.path)
		folders = append(folders, folder)
	}
	return folders, nil
}

// applyNetworkEnv writes the port and proxy the environment sets into the Network policy, so the
// Network screen shows what is in effect instead of asking for a restart.
func (p *process) applyNetworkEnv(ctx context.Context, wall clock.Clock) error {
	network := p.cache.Snapshot().NetworkSettings()
	changed := false

	if config.EnvSet("server.port", p.env) && network.Port != p.config.Server.Port {
		network.Port = p.config.Server.Port
		changed = true
	}
	if value, set := p.env(envProxyURL); set && strings.TrimSpace(value) != "" {
		proxy := strings.TrimSpace(value)
		if !network.ProxyEnabled || network.ProxyURL != proxy {
			network.ProxyEnabled, network.ProxyURL = true, proxy
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if err := p.cache.SetNetworkSettings(ctx, wall, network, startupCaller(network.AllowCIDRs)); err != nil {
		return fmt.Errorf("network settings from the environment: %w", err)
	}
	p.logger.Info("network settings set from the environment", "port", network.Port, "proxy", network.ProxyEnabled)
	return nil
}

// startupCaller stands in for the caller the lockout check wants: a startup write is nobody's request, so an address
// the allowlist already admits keeps the check from refusing a change that locks no one out.
func startupCaller(allowCIDRs []string) netip.Addr {
	for _, raw := range allowCIDRs {
		if prefix, err := netip.ParsePrefix(raw); err == nil {
			return prefix.Addr()
		}
	}
	return netip.Addr{}
}
