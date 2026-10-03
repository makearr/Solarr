package apiserver

import (
	"context"
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// TVProviderInfo is one TV metadata provider as the Metadata settings page lists it.
type TVProviderInfo struct {
	ID, Name, Adds string
	// Key is "not_needed", "missing", "stored" or "built_in".
	Key string
}

// TVProviderResult is one provider's answer to the test question.
type TVProviderResult struct {
	ID      string
	OK      bool
	Message string
}

// TVProviders reports the TV metadata providers and tests them live.
type TVProviders interface {
	List() []TVProviderInfo
	Test(ctx context.Context) []TVProviderResult
}

// ListTVProviders answers each provider and whether it has the key it needs, asking nothing of the providers.
func (s *Server) ListTVProviders(w http.ResponseWriter, r *http.Request) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.tvProviders == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "TV metadata is not wired")
		return
	}
	listed := s.tvProviders.List()
	view := make([]gen.TVProvider, 0, len(listed))
	for _, provider := range listed {
		view = append(view, gen.TVProvider{
			Id: gen.TVProviderId(provider.ID), Name: provider.Name, Adds: provider.Adds, Key: gen.TVProviderKey(provider.Key),
		})
	}
	writeJSON(w, s.logger, http.StatusOK, view)
}

// TestTVProviders asks each provider one cheap question. Administrator only.
func (s *Server) TestTVProviders(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.tvProviders == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "TV metadata is not wired")
		return
	}
	results := s.tvProviders.Test(r.Context())
	now := clock.NewSystem().Now().UTC()
	view := make([]gen.TVProviderTest, 0, len(results))
	for _, result := range results {
		view = append(view, gen.TVProviderTest{Id: gen.TVProviderTestId(result.ID), Ok: result.OK, Message: result.Message, TestedAt: now})
	}
	writeJSON(w, s.logger, http.StatusOK, view)
}
