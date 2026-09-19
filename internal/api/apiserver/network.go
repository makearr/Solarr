package apiserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// ipAllowlist refuses an excluded address before authentication -- it can
// only deny, never authenticate (Invariant 89). /api/v1/health is exempt.
func (s *Server) ipAllowlist(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == s.healthPath || s.auth.Settings == nil {
			next.ServeHTTP(w, r)
			return
		}

		network := s.auth.Settings.Snapshot().NetworkSettings()
		cidrs := network.AllowCIDRs
		if !network.AllowCIDRsEnabled || len(cidrs) == 0 {
			next.ServeHTTP(w, r)
			return
		}

		address := s.resolvedClientAddress(r)
		if !address.IsValid() || !addressInAny(address, cidrs) {
			writeProblem(w, s.logger, http.StatusForbidden, TypeForbidden,
				"Forbidden", "this address is not allowed to reach this instance")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// GetNetworkSettings answers ADR-58's own policy, plus the port this
// instance actually bound to at startup.
func (s *Server) GetNetworkSettings(w http.ResponseWriter, r *http.Request) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, s.networkSettingsView())
}

// UpdateNetworkSettings changes the port, egress proxy or IP allowlist.
// Administrator only, written wholesale like the cache budget (ADR-44).
func (s *Server) UpdateNetworkSettings(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}

	var body gen.UpdateNetworkSettingsJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "expected port, proxy_bypass and allow_cidrs")
		return
	}

	wanted := settings.NetworkSettings{
		Port: body.Port, ProxyEnabled: body.ProxyEnabled, ProxyBypass: body.ProxyBypass,
		AllowCIDRsEnabled: body.AllowCidrsEnabled, AllowCIDRs: body.AllowCidrs,
	}
	if body.ProxyUrl != nil {
		wanted.ProxyURL = *body.ProxyUrl
	}

	if err := s.auth.Registry.LookupAll(
		settings.KeyNetworkPort, settings.KeyNetworkProxyEnabled, settings.KeyNetworkProxyURL,
		settings.KeyNetworkProxyBypass, settings.KeyNetworkAllowCIDRsEnabled, settings.KeyNetworkAllowCIDRs,
	); err != nil {
		writeError(w, s.logger, err)
		return
	}

	// ADR-58's revised precedence (2026-09-12): config.yml now outranks
	// settings.db, so a deliberate port choice (0 means "no override," left
	// untouched) has to reach the file too, or the change would be silently
	// undone by config.yml's own stale value on the next restart. Written
	// first and required to succeed -- a settings.db write with no matching
	// config.yml write is the exact inconsistency this exists to prevent.
	if wanted.Port != 0 && s.configPath != "" {
		if err := s.writeConfigServerPort(wanted.Port); err != nil {
			writeProblem(w, s.logger, http.StatusInternalServerError, TypeInternal,
				"The port could not be saved", "config.yml could not be updated: "+err.Error())
			return
		}
	}

	caller := s.resolvedClientAddress(r)
	if err := s.auth.Settings.SetNetworkSettings(r.Context(), clock.NewSystem(), wanted, caller); err != nil {
		var problems config.Problems
		if errors.As(err, &problems) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"The network settings have problems", problems.Report("the network settings", "Nothing was changed."))
			return
		}
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, s.networkSettingsView())
}

// writeConfigServerPort surgically patches server.port into config.yml on
// disk, preserving the rest of the file (config.SetServerPort's own doc
// comment explains why a full remarshal is not acceptable here).
func (s *Server) writeConfigServerPort(port int) error {
	file, err := s.fsys.Open(s.configPath)
	if err != nil {
		return err
	}
	data, err := io.ReadAll(file)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}

	patched, err := config.SetServerPort(data, port)
	if err != nil {
		return err
	}

	// Secret, not plain: config.yml carries the encryption key (Invariant 68).
	return fsx.WriteAtomicSecret(s.fsys, s.configPath, func(w io.Writer) error {
		_, writeErr := w.Write(patched)
		return writeErr
	})
}

func (s *Server) networkSettingsView() gen.NetworkSettings {
	stored := s.auth.Settings.Snapshot().NetworkSettings()
	view := gen.NetworkSettings{
		Port: stored.Port, InEffectPort: s.effectivePort,
		RestartRequired: stored.Port != 0 && stored.Port != s.effectivePort,
		ProxyEnabled:    stored.ProxyEnabled, ProxyBypass: stored.ProxyBypass,
		AllowCidrsEnabled: stored.AllowCIDRsEnabled, AllowCidrs: stored.AllowCIDRs,
	}
	if stored.ProxyURL != "" {
		view.ProxyUrl = &stored.ProxyURL
	}
	// A fresh instance has never written these; nil serialises to JSON null,
	// which crashed the Network screen's own network.proxy_bypass.join(...)
	// (found live against a genuinely fresh install, never hit before since
	// every prior test instance already had these set from earlier use).
	if view.ProxyBypass == nil {
		view.ProxyBypass = []string{}
	}
	if view.AllowCidrs == nil {
		view.AllowCidrs = []string{}
	}
	return view
}

// addressInAny reports whether address is inside any of cidrs, skipping any
// entry that no longer parses rather than failing the whole check.
func addressInAny(address netip.Addr, cidrs []string) bool {
	for _, raw := range cidrs {
		if prefix, err := netip.ParsePrefix(raw); err == nil && prefix.Contains(address) {
			return true
		}
	}
	return false
}
