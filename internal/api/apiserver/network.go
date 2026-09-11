package apiserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"

	"github.com/makearr/MediaNexus/internal/api/apiserver/gen"
	"github.com/makearr/MediaNexus/internal/api/authz"
	"github.com/makearr/MediaNexus/internal/db/settings"
	"github.com/makearr/MediaNexus/internal/platform/clock"
	"github.com/makearr/MediaNexus/internal/platform/config"
)

// ipAllowlist refuses an excluded address before authentication -- it can
// only deny, never authenticate (Invariant 89). /api/v1/health is exempt.
func (s *Server) ipAllowlist(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == s.healthPath || s.auth.Settings == nil {
			next.ServeHTTP(w, r)
			return
		}

		cidrs := s.auth.Settings.Snapshot().NetworkSettings().AllowCIDRs
		if len(cidrs) == 0 {
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
		Port: body.Port, ProxyBypass: body.ProxyBypass, AllowCIDRs: body.AllowCidrs,
	}
	if body.ProxyUrl != nil {
		wanted.ProxyURL = *body.ProxyUrl
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

func (s *Server) networkSettingsView() gen.NetworkSettings {
	stored := s.auth.Settings.Snapshot().NetworkSettings()
	view := gen.NetworkSettings{
		Port: stored.Port, InEffectPort: s.effectivePort,
		RestartRequired: stored.Port != 0 && stored.Port != s.effectivePort,
		ProxyBypass:     stored.ProxyBypass, AllowCidrs: stored.AllowCIDRs,
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
