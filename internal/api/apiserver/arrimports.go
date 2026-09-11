package apiserver

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/MediaNexus/internal/api/apiserver/gen"
	"github.com/makearr/MediaNexus/internal/api/authz"
)

// ErrArrAppNotYetSupported means Sync was asked to run a connection for an
// app that only connects and parks today (ADR-52) -- Lidarr is the only one
// with a real mapping this pass.
var ErrArrAppNotYetSupported = errors.New("apiserver: this *Arr app is not yet wired for a real sync")

// ErrArrImportNotDone mirrors library.ErrArrImportNotDone at this package's
// own boundary, the same shape ErrImportSessionReverted's own pair keeps.
var ErrArrImportNotDone = errors.New("apiserver: this import is not in a revertible state")

// ArrImportRun is one *Arr import's own progress and outcome.
type ArrImportRun struct {
	ID                             int64
	InstanceID                     int64
	App                            string
	State                          string
	StartedAt                      int64
	FinishedAt                     *int64
	Error                          string
	ArtistsCreated, ArtistsMatched int64
	AlbumsCreated, AlbumsMatched   int64
	ItemsParked                    int64
}

// ArrImportItem is one entity a run touched.
type ArrImportItem struct {
	ID                    int64
	ImportID              int64
	Kind, SourceID, Title string
	PersonID              *int64
	PersonCreated         bool
	SubscriptionID        *int64
	WorkID                *int64
	WorkCreated           bool
	DesiredItemID         *int64
	DesiredItemCreated    bool
	CreatedAt             int64
}

// ArrImportDetail is one run plus every item it touched.
type ArrImportDetail struct {
	Run   ArrImportRun
	Items []ArrImportItem
}

// ArrImportRevertResult is what one revert actually undid.
type ArrImportRevertResult struct {
	WorksDeleted, SubscriptionsDeleted, PeopleDeleted int
	Errors                                            []string
}

// ArrSync runs and reverts *Arr imports (todo 4.13, ADR-52). Defined here
// and implemented by cmd/medianexus/adapters.ArrSync, the same split
// Discover's own interface keeps -- this package never imports
// internal/db/library directly.
type ArrSync interface {
	Sync(ctx context.Context, instanceID int64) (ArrImportRun, error)
	ListImports(ctx context.Context, instanceID int64) ([]ArrImportRun, error)
	GetImport(ctx context.Context, id int64) (ArrImportDetail, error)
	Revert(ctx context.Context, id int64) (ArrImportRevertResult, error)
}

// routeSyncArrInstance parses the id path parameter.
func (s *Server) routeSyncArrInstance(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "an instance id must be a number")
		return
	}
	s.SyncArrInstance(w, r, id)
}

// SyncArrInstance runs one import. Administrator only -- it exercises a
// stored credential against an outside system, and mutates the library.
func (s *Server) SyncArrInstance(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.arrSync == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "*Arr import is not available")
		return
	}

	run, err := s.arrSync.Sync(r.Context(), id)
	switch {
	case errors.Is(err, ErrArrAppNotYetSupported):
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict,
			"Not yet supported", "this app only connects and parks its own intent so far, no real sync")
	case err != nil:
		writeError(w, s.logger, err)
	default:
		writeJSON(w, s.logger, http.StatusOK, arrImportRunView(run))
	}
}

// routeListArrImports parses the instance_id query parameter, mirroring routeDiscoverSearch.
func (s *Server) routeListArrImports(w http.ResponseWriter, r *http.Request) {
	instanceID, err := strconv.ParseInt(r.URL.Query().Get("instance_id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "instance_id is required and must be a number")
		return
	}
	s.ListArrImports(w, r, gen.ListArrImportsParams{InstanceId: instanceID})
}

// ListArrImports answers every run for one instance, newest first.
func (s *Server) ListArrImports(w http.ResponseWriter, r *http.Request, params gen.ListArrImportsParams) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.arrSync == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "*Arr import is not available")
		return
	}

	runs, err := s.arrSync.ListImports(r.Context(), params.InstanceId)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	views := make([]gen.ArrImportRun, len(runs))
	for index, run := range runs {
		views[index] = arrImportRunView(run)
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

// routeGetArrImport parses the id path parameter.
func (s *Server) routeGetArrImport(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "an import id must be a number")
		return
	}
	s.GetArrImport(w, r, id)
}

// GetArrImport answers one run's own detail, including every item it touched.
func (s *Server) GetArrImport(w http.ResponseWriter, r *http.Request, id int64) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.arrSync == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "*Arr import is not available")
		return
	}

	detail, err := s.arrSync.GetImport(r.Context(), id)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	items := make([]gen.ArrImportItem, len(detail.Items))
	for index, item := range detail.Items {
		items[index] = arrImportItemView(item)
	}
	writeJSON(w, s.logger, http.StatusOK, gen.ArrImportDetail{Run: arrImportRunView(detail.Run), Items: items})
}

// routeRevertArrImport parses the id path parameter.
func (s *Server) routeRevertArrImport(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "an import id must be a number")
		return
	}
	s.RevertArrImport(w, r, id)
}

// RevertArrImport undoes one run. Administrator only.
func (s *Server) RevertArrImport(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.arrSync == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "*Arr import is not available")
		return
	}

	result, err := s.arrSync.Revert(r.Context(), id)
	switch {
	case errors.Is(err, ErrArrImportNotDone):
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict,
			"Not revertible", "this import is still running, or was already reverted")
	case err != nil:
		writeError(w, s.logger, err)
	default:
		writeJSON(w, s.logger, http.StatusOK, gen.ArrImportRevertResult{
			WorksDeleted: result.WorksDeleted, SubscriptionsDeleted: result.SubscriptionsDeleted,
			PeopleDeleted: result.PeopleDeleted, Errors: result.Errors,
		})
	}
}

func arrImportRunView(run ArrImportRun) gen.ArrImportRun {
	view := gen.ArrImportRun{
		Id: run.ID, InstanceId: run.InstanceID, App: gen.ArrApp(run.App), State: gen.ArrImportRunState(run.State),
		StartedAt: unixTime(run.StartedAt), ArtistsCreated: run.ArtistsCreated, ArtistsMatched: run.ArtistsMatched,
		AlbumsCreated: run.AlbumsCreated, AlbumsMatched: run.AlbumsMatched, ItemsParked: run.ItemsParked,
	}
	if run.FinishedAt != nil {
		at := unixTime(*run.FinishedAt)
		view.FinishedAt = &at
	}
	if run.Error != "" {
		errMsg := run.Error
		view.Error = &errMsg
	}
	return view
}

func arrImportItemView(item ArrImportItem) gen.ArrImportItem {
	return gen.ArrImportItem{
		Id: item.ID, ImportId: item.ImportID, Kind: gen.ArrImportItemKind(item.Kind),
		SourceId: item.SourceID, Title: item.Title,
		PersonId: item.PersonID, PersonCreated: item.PersonCreated, SubscriptionId: item.SubscriptionID,
		WorkId: item.WorkID, WorkCreated: item.WorkCreated,
		DesiredItemId: item.DesiredItemID, DesiredItemCreated: item.DesiredItemCreated,
		CreatedAt: unixTime(item.CreatedAt),
	}
}
