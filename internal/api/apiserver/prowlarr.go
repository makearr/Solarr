package apiserver

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
)

// ProwlarrIndexerRow is one indexer a Prowlarr serves, and whether it is already added here.
type ProwlarrIndexerRow struct {
	ProwlarrID   int64
	Name         string
	Impl         string
	BaseURL      string
	Enabled      bool
	Music        bool
	AlreadyAdded bool
}

// ProwlarrLister reads a Prowlarr's indexer list without saving anything.
type ProwlarrLister interface {
	ListProwlarr(ctx context.Context, baseURL, apiKey string) ([]ProwlarrIndexerRow, error)
}

// ListProwlarrIndexers answers what a Prowlarr serves, for the Indexers page to add from.
func (s *Server) ListProwlarrIndexers(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.prowlarr == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "Prowlarr is not wired up")
		return
	}
	var body gen.ListProwlarrIndexersJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil ||
		strings.TrimSpace(body.BaseUrl) == "" || strings.TrimSpace(body.ApiKey) == "" {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "Prowlarr's address and API key are both needed")
		return
	}
	rows, err := s.prowlarr.ListProwlarr(r.Context(), strings.TrimSpace(body.BaseUrl), strings.TrimSpace(body.ApiKey))
	if err != nil {
		// The key travels in a header, never the URL, so what failed can be shown as it is.
		writeProblem(w, s.logger, http.StatusBadGateway, TypeUnavailable, "Prowlarr could not be read", err.Error())
		return
	}
	views := make([]gen.ProwlarrIndexer, len(rows))
	for i, row := range rows {
		views[i] = gen.ProwlarrIndexer{
			ProwlarrId: row.ProwlarrID, Name: row.Name, Impl: gen.ProwlarrIndexerImpl(row.Impl), BaseUrl: row.BaseURL,
			Enabled: row.Enabled, Music: row.Music, AlreadyAdded: row.AlreadyAdded,
		}
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}
