package apiserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
)

// IndexerKind is one entry of the indexer catalog the user may add from (Prowlarr's add list).
type IndexerKind struct {
	ID, Name, Description, Impl, Protocol, Privacy, Language string
	BaseURLs, MediaTypes                                     []string
	UserLabel, SecretLabel                                   string
	SecretOptional                                           bool
	CompanionDownloader                                      string
}

// IndexerKinds answers the indexer catalog, in the add list's order.
type IndexerKinds interface {
	IndexerKinds() []IndexerKind
}

// ListIndexerKinds answers every indexer the user may add.
func (s *Server) ListIndexerKinds(w http.ResponseWriter, r *http.Request) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	views := []gen.IndexerKind{}
	if s.indexerKinds != nil {
		for _, kind := range s.indexerKinds.IndexerKinds() {
			views = append(views, indexerKindView(kind))
		}
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

func indexerKindView(kind IndexerKind) gen.IndexerKind {
	media := make([]gen.IndexerKindMediaTypes, len(kind.MediaTypes))
	for i, one := range kind.MediaTypes {
		media[i] = gen.IndexerKindMediaTypes(one)
	}
	urls := kind.BaseURLs
	if urls == nil {
		urls = []string{}
	}
	return gen.IndexerKind{
		Id: kind.ID, Name: kind.Name, Description: kind.Description, Impl: kind.Impl,
		Protocol: gen.IndexerKindProtocol(kind.Protocol), Privacy: gen.IndexerKindPrivacy(kind.Privacy),
		Language: kind.Language, BaseUrls: urls, MediaTypes: media, UserLabel: strPtr(kind.UserLabel),
		SecretLabel: strPtr(kind.SecretLabel), SecretOptional: kind.SecretOptional,
		CompanionDownloader: strPtr(kind.CompanionDownloader),
	}
}

// indexerKind finds a catalog entry by its id, or else the first entry of impl (an indexer added before the catalog).
func (s *Server) indexerKind(definition, impl string) (IndexerKind, bool) {
	if s.indexerKinds == nil {
		return IndexerKind{}, false
	}
	kinds := s.indexerKinds.IndexerKinds()
	if index := slices.IndexFunc(kinds, func(kind IndexerKind) bool { return definition != "" && kind.ID == definition }); index >= 0 {
		return kinds[index], true
	}
	index := slices.IndexFunc(kinds, func(kind IndexerKind) bool { return kind.Impl == impl })
	if index < 0 {
		return IndexerKind{}, false
	}
	return kinds[index], true
}

// validIndexerKind refuses an implementation Solarr has none of; without the catalog anything is let through.
func (s *Server) validIndexerKind(w http.ResponseWriter, impl string) bool {
	if s.indexerKinds == nil {
		return true
	}
	if _, found := s.indexerKind("", impl); !found {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request",
			fmt.Sprintf("%q is not an indexer Solarr can search; /indexers/schema lists them", impl))
		return false
	}
	return true
}

// TestIndexerConfig tests an indexer before it is added ("Test", then "Add").
func (s *Server) TestIndexerConfig(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	var body gen.TestIndexerConfigJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil || body.Impl == "" || body.BaseUrl == "" {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "expected impl and base_url")
		return
	}
	if s.indexerTester == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "indexer tests are not available")
		return
	}
	if !s.validIndexerKind(w, body.Impl) {
		return
	}
	result, err := s.indexerTester.TestIndexerConfig(r.Context(), body.Impl, body.BaseUrl, derefOr(body.ApiKey, ""))
	if err != nil {
		writeIndexerError(w, s, err, "The connection has problems", "Nothing was tested.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, gen.ConnectionTestResult{Ok: result.OK, Message: result.Message, TestedAt: result.TestedAt})
}

func (s *Server) routeAddIndexerCompanion(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "an indexer id must be a number")
		return
	}
	s.AddIndexerCompanion(w, r, id)
}

// AddIndexerCompanion adds the download client an indexer's releases need, at its address and with its key.
func (s *Server) AddIndexerCompanion(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	found, err := s.auth.Settings.GetIndexer(r.Context(), id)
	if err != nil {
		writeIndexerError(w, s, err, "The indexer has problems", "Nothing was added.")
		return
	}
	kind, known := s.indexerKind(found.Definition(), found.Impl)
	client, clientKnown := s.downloaderKindOf(kind.CompanionDownloader)
	if !known || kind.CompanionDownloader == "" || !clientKnown {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "this indexer needs no download client of its own")
		return
	}
	key, err := s.auth.Settings.DecryptedIndexerAPIKey(r.Context(), id)
	if err != nil {
		writeIndexerError(w, s, err, "The indexer has problems", "Nothing was added.")
		return
	}
	created, err := s.auth.Settings.CreateDownloader(r.Context(), settings.DownloaderRequest{
		Name: client.Label, Impl: client.Impl, BaseURL: found.BaseURL, Enabled: true, Protocol: client.Protocol,
	}, key, s.wall.Now().Unix())
	if err != nil {
		writeDownloaderError(w, s, err, "The download client has problems", "Nothing was added.")
		return
	}
	writeJSON(w, s.logger, http.StatusCreated, downloaderView(created))
}

func (s *Server) routeAddDownloaderCompanion(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "a downloader id must be a number")
		return
	}
	s.AddDownloaderCompanion(w, r, id)
}

// AddDownloaderCompanion adds the indexer that searches for a download client's releases, at its address and with
// its key.
func (s *Server) AddDownloaderCompanion(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	found, err := s.auth.Settings.GetDownloader(r.Context(), id)
	if err != nil {
		writeDownloaderError(w, s, err, "The download client has problems", "Nothing was added.")
		return
	}
	client, known := s.downloaderKindOf(found.Impl)
	kind, kindKnown := s.indexerKind(client.CompanionIndexer, "")
	if !known || client.CompanionIndexer == "" || !kindKnown {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "this download client has no indexer of its own")
		return
	}
	key, err := s.auth.Settings.DecryptedDownloaderCredentials(r.Context(), id)
	if err != nil {
		writeDownloaderError(w, s, err, "The download client has problems", "Nothing was added.")
		return
	}
	created, err := s.auth.Settings.CreateIndexer(r.Context(), settings.IndexerRequest{
		Name: kind.Name, Impl: kind.Impl, BaseURL: found.BaseURL, Enabled: true, Definition: kind.ID,
	}, key, s.wall.Now().Unix())
	if err != nil {
		writeIndexerError(w, s, err, "The indexer has problems", "Nothing was added.")
		return
	}
	writeJSON(w, s.logger, http.StatusCreated, indexerView(created))
}

// downloaderKindOf finds a download client kind by its impl.
func (s *Server) downloaderKindOf(impl string) (DownloaderKind, bool) {
	if s.downloaderKinds == nil || impl == "" {
		return DownloaderKind{}, false
	}
	kinds := s.downloaderKinds.DownloaderKinds()
	index := slices.IndexFunc(kinds, func(kind DownloaderKind) bool { return kind.Impl == impl })
	if index < 0 {
		return DownloaderKind{}, false
	}
	return kinds[index], true
}
