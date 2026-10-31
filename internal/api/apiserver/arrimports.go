package apiserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// ArrSyncOptions is what one import brings in (asked 2026-09-30): monitored and unmonitored items,
// which albums by what the *Arr holds of them, and how its paths read here.
type ArrSyncOptions struct {
	Monitored, Unmonitored bool
	// Albums is "all", "missing" (the *Arr lacks a track) or "complete".
	Albums       string
	PathRewrites []PathRewrite
	// ProfileMap gives each *Arr quality profile's items a Solarr profile, nil for the library's; one not listed
	// takes the Solarr profile of the same name, else the library's.
	ProfileMap map[int64]*int64
	// SettingsInto are the libraries that take the *Arr's Media Management into their File Management first.
	SettingsInto []int64
	// NewFoldersLibrary is where the *Arr's root folders unknown here are about to be added, for a preview.
	NewFoldersLibrary *int64
	// MonitoringOnly sets the monitored flag of what Solarr already holds, adds nothing and ignores the item choices.
	MonitoringOnly bool
	// Indexers and DownloadClients also add the *Arr's own, as the connection's.
	Indexers, DownloadClients bool
}

// ArrImportPreview is what an import would do, read without changing anything (ADR-52's preview step).
type ArrImportPreview struct {
	// ItemKind is what the counts count: "artists", "series" or "movies".
	ItemKind string
	Roots    []ArrPreviewRoot
	Profiles []ArrPreviewProfile
	Settings []ArrPreviewSettings
}

// ArrPreviewRoot is one *Arr root folder: where it is here, its library, and what of it the options choose.
type ArrPreviewRoot struct {
	Path, LocalPath                   string
	RootFolderID, LibraryID           *int64
	Chosen, New, InLibrary, WithoutID int64
}

// ArrPreviewProfile is one *Arr quality profile in use and the Solarr profile proposed for it, nil for the library's.
type ArrPreviewProfile struct {
	ArrProfileID      int64
	Name              string
	Items             int64
	ProposedProfileID *int64
}

// ArrPreviewSettings is one library's File Management settings that the *Arr's Media Management would change.
type ArrPreviewSettings struct {
	LibraryID int64
	Changes   []ArrSettingChange
}

// ArrSettingChange is one setting, its value here and the *Arr's, in words.
type ArrSettingChange struct{ Key, Label, Current, Proposed string }

// PathRewrite reads an *Arr's path prefix as another here, e.g. /media/music as /mnt/media/music.
type PathRewrite struct{ From, To string }

// DefaultArrSyncOptions is what a sync did before it had options: monitored items, every album.
var DefaultArrSyncOptions = ArrSyncOptions{Monitored: true, Albums: "all"}

// ErrArrAppNotYetSupported means a connection was asked for something its app does not offer here yet
// (Readarr's sync, Sonarr's indexers): Prowlarr holds the indexers, Lidarr/Sonarr/Radarr sync.
var ErrArrAppNotYetSupported = errors.New("apiserver: this *Arr app does not offer that here")

// ErrArrImportNotDone mirrors library.ErrArrImportNotDone at this package's
// own boundary, the same shape ErrImportSessionReverted's own pair keeps.
var ErrArrImportNotDone = errors.New("apiserver: this import is not in a revertible state")

// ErrArrSyncAlreadyRunning means this instance already has a RUNNING import
// -- refused rather than started twice, the same "refused rather than
// double-queued" rule ADR-59's own Run now has for a registered task.
var ErrArrSyncAlreadyRunning = errors.New("apiserver: this instance already has a sync running")

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
	// ArtistsTotal and ArtistsDone are the live figure of a running import; zero total means not known.
	ArtistsTotal, ArtistsDone int64
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
// and implemented by cmd/solarr/adapters.ArrSync, the same split
// Discover's own interface keeps -- this package never imports
// internal/db/library directly.
type ArrSync interface {
	Sync(ctx context.Context, instanceID int64, options ArrSyncOptions) (ArrImportRun, error)
	// Preview reads what Sync would do with options and changes nothing.
	Preview(ctx context.Context, instanceID int64, options ArrSyncOptions) (ArrImportPreview, error)
	ListImports(ctx context.Context, instanceID int64) ([]ArrImportRun, error)
	GetImport(ctx context.Context, id int64) (ArrImportDetail, error)
	Revert(ctx context.Context, id int64) (ArrImportRevertResult, error)
	// RootFolders answers this instance's own configured root folders, read
	// live -- the setup wizard's "import the root folder(s)" step.
	RootFolders(ctx context.Context, instanceID int64) ([]string, error)
	// PushMonitoring writes Solarr's monitoring of what an import linked back to the *Arr, where it differs.
	PushMonitoring(ctx context.Context, instanceID int64) (ArrPushResult, error)
}

// ArrPushResult is what one send of monitoring did.
type ArrPushResult struct{ Checked, Changed int64 }

// routePushArrMonitoring parses the id path parameter.
func (s *Server) routePushArrMonitoring(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "an instance id must be a number")
		return
	}
	s.PushArrMonitoring(w, r, id)
}

// PushArrMonitoring sends the monitoring of what an import brought in back to its *Arr, now. Administrator only.
func (s *Server) PushArrMonitoring(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.arrSync == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "*Arr import is not available")
		return
	}
	result, err := s.arrSync.PushMonitoring(r.Context(), id)
	switch {
	case errors.Is(err, settings.ErrNotFound):
		writeError(w, s.logger, err)
	case err != nil:
		writeProblem(w, s.logger, http.StatusBadGateway, TypeUnavailable, "The *Arr could not be written", httpx.Explain(err))
	default:
		writeJSON(w, s.logger, http.StatusOK, map[string]int64{"checked": result.Checked, "changed": result.Changed})
	}
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

	options, ok := s.readArrSyncOptions(w, r)
	if !ok {
		return
	}
	run, err := s.arrSync.Sync(r.Context(), id, options)
	switch {
	case errors.Is(err, ErrArrAppNotYetSupported):
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict,
			"Not yet supported", "this app only connects and parks its own intent so far, no real sync")
	case errors.Is(err, ErrArrSyncAlreadyRunning):
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict,
			"Already running", "this instance already has a sync in progress")
	case err != nil:
		writeError(w, s.logger, err)
	default:
		writeJSON(w, s.logger, http.StatusOK, arrImportRunView(run))
	}
}

// readArrSyncOptions reads a sync's or a preview's options; an empty body is the defaults. A failure is answered.
func (s *Server) readArrSyncOptions(w http.ResponseWriter, r *http.Request) (ArrSyncOptions, bool) {
	options := DefaultArrSyncOptions
	if r.ContentLength == 0 {
		return options, true
	}
	var body gen.ArrSyncOptions
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "the sync options could not be read")
		return ArrSyncOptions{}, false
	}
	options = ArrSyncOptions{Monitored: derefOr(body.Monitored, true), Unmonitored: derefOr(body.Unmonitored, false), Albums: "all"}
	if body.Albums != nil {
		options.Albums = string(*body.Albums)
	}
	for _, rewrite := range derefOr(body.PathRewrites, nil) {
		options.PathRewrites = append(options.PathRewrites, PathRewrite{From: rewrite.From, To: rewrite.To})
	}
	if body.ProfileMap != nil {
		options.ProfileMap = make(map[int64]*int64, len(*body.ProfileMap))
		for _, entry := range *body.ProfileMap {
			options.ProfileMap[entry.ArrProfileId] = entry.ProfileId
		}
	}
	options.SettingsInto = derefOr(body.SettingsInto, nil)
	options.NewFoldersLibrary = body.NewFoldersLibraryId
	options.MonitoringOnly = body.Mode != nil && *body.Mode == gen.ArrSyncOptionsModeMonitoring
	options.Indexers, options.DownloadClients = derefOr(body.Indexers, false), derefOr(body.DownloadClients, false)
	if !options.MonitoringOnly && !options.Monitored && !options.Unmonitored && !options.Indexers && !options.DownloadClients {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Nothing to import", "choose monitored items, unmonitored items or both")
		return ArrSyncOptions{}, false
	}
	return options, true
}

// routePreviewArrImport parses the id path parameter.
func (s *Server) routePreviewArrImport(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "an instance id must be a number")
		return
	}
	s.PreviewArrImport(w, r, id)
}

// PreviewArrImport answers what an import with the options would do, reading the instance and changing nothing.
func (s *Server) PreviewArrImport(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.arrSync == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "*Arr import is not available")
		return
	}
	options, ok := s.readArrSyncOptions(w, r)
	if !ok {
		return
	}
	preview, err := s.arrSync.Preview(r.Context(), id, options)
	switch {
	case errors.Is(err, ErrArrAppNotYetSupported):
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict, "Not yet supported", "this app has no import here yet")
	case errors.Is(err, settings.ErrNotFound):
		writeError(w, s.logger, err)
	case err != nil:
		writeProblem(w, s.logger, http.StatusBadGateway, TypeUnavailable, "The *Arr could not be read", httpx.Explain(err))
	default:
		writeJSON(w, s.logger, http.StatusOK, arrImportPreviewView(preview))
	}
}

func arrImportPreviewView(preview ArrImportPreview) gen.ArrImportPreview {
	view := gen.ArrImportPreview{
		ItemKind: gen.ArrImportPreviewItemKind(preview.ItemKind),
		Roots:    make([]gen.ArrPreviewRoot, len(preview.Roots)), Profiles: make([]gen.ArrPreviewProfile, len(preview.Profiles)),
		Settings: make([]gen.ArrPreviewSettings, len(preview.Settings)),
	}
	for i, root := range preview.Roots {
		view.Roots[i] = gen.ArrPreviewRoot{
			Path: root.Path, LocalPath: root.LocalPath, RootFolderId: root.RootFolderID, LibraryId: root.LibraryID,
			Chosen: root.Chosen, New: root.New, InLibrary: root.InLibrary, WithoutId: root.WithoutID,
		}
	}
	for i, profile := range preview.Profiles {
		view.Profiles[i] = gen.ArrPreviewProfile{
			ArrProfileId: profile.ArrProfileID, Name: profile.Name, Items: profile.Items, ProposedProfileId: profile.ProposedProfileID,
		}
	}
	for i, library := range preview.Settings {
		changes := make([]struct {
			Current  string `json:"current"`
			Key      string `json:"key"`
			Label    string `json:"label"`
			Proposed string `json:"proposed"`
		}, len(library.Changes))
		for j, change := range library.Changes {
			changes[j].Key, changes[j].Label, changes[j].Current, changes[j].Proposed = change.Key, change.Label, change.Current, change.Proposed
		}
		view.Settings[i] = gen.ArrPreviewSettings{LibraryId: library.LibraryID, Changes: changes}
	}
	return view
}

// routeListArrInstanceRootFolders parses the id path parameter, mirroring routeSyncArrInstance.
func (s *Server) routeListArrInstanceRootFolders(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "an instance id must be a number")
		return
	}
	s.ListArrInstanceRootFolders(w, r, id)
}

// ListArrInstanceRootFolders reads one instance's own configured root
// folders live. Administrator only -- it exercises a stored credential
// against an outside system, the same reason SyncArrInstance is.
func (s *Server) ListArrInstanceRootFolders(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.arrSync == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "*Arr import is not available")
		return
	}

	paths, err := s.arrSync.RootFolders(r.Context(), id)
	switch {
	case errors.Is(err, ErrArrAppNotYetSupported):
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict,
			"Not yet supported", "this app only connects and parks its own intent so far")
	case err != nil:
		// An *Arr that does not answer is a 502 saying so, not an internal error (found 2026-09-30).
		s.writeArrConnectionError(w, err)
	default:
		views := make([]gen.ArrRootFolder, len(paths))
		for index, path := range paths {
			views[index] = gen.ArrRootFolder{Path: path}
		}
		writeJSON(w, s.logger, http.StatusOK, views)
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
	if run.ArtistsTotal > 0 {
		view.ArtistsTotal, view.ArtistsDone = &run.ArtistsTotal, &run.ArtistsDone
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

// ArrDownloadClientRow is one download client an *Arr connection uses, and whether Solarr can and does.
type ArrDownloadClientRow struct {
	Name, Impl, Protocol, BaseURL, Category, Username string
	Enabled, Supported, AlreadyAdded                  bool
}

// ArrConnections reads a connection's indexers and download clients, and uses its indexers here.
type ArrConnections interface {
	InstanceIndexers(ctx context.Context, instanceID int64) ([]ProwlarrIndexerRow, error)
	UseInstanceIndexers(ctx context.Context, instanceID int64, prowlarrIDs []int64) (int64, error)
	InstanceDownloadClients(ctx context.Context, instanceID int64) ([]ArrDownloadClientRow, error)
}

func (s *Server) routeListArrInstanceIndexers(w http.ResponseWriter, r *http.Request) {
	if id, err := parsePathID(w, s, r, "id", "an instance id must be a number"); err == nil {
		s.ListArrInstanceIndexers(w, r, id)
	}
}

func (s *Server) routeUseArrInstanceIndexers(w http.ResponseWriter, r *http.Request) {
	if id, err := parsePathID(w, s, r, "id", "an instance id must be a number"); err == nil {
		s.UseArrInstanceIndexers(w, r, id)
	}
}

func (s *Server) routeListArrInstanceDownloadClients(w http.ResponseWriter, r *http.Request) {
	if id, err := parsePathID(w, s, r, "id", "an instance id must be a number"); err == nil {
		s.ListArrInstanceDownloadClients(w, r, id)
	}
}

// arrConnectionsReady answers whether the connections reader is wired, having answered if not.
func (s *Server) arrConnectionsReady(w http.ResponseWriter, r *http.Request) bool {
	if !s.requireAdministrator(w, r) {
		return false
	}
	if s.arrConnections == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "*Arr connections are not available")
		return false
	}
	return true
}

// writeArrConnectionError tells an app that cannot answer apart from an *Arr that did not.
func (s *Server) writeArrConnectionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrArrAppNotYetSupported):
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict, "Not for this app", err.Error())
	case errors.Is(err, settings.ErrNotFound):
		writeError(w, s.logger, err)
	default:
		writeProblem(w, s.logger, http.StatusBadGateway, TypeUnavailable, "The *Arr could not be read", httpx.Explain(err))
	}
}

// ListArrInstanceIndexers answers a Prowlarr connection's indexers.
func (s *Server) ListArrInstanceIndexers(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.arrConnectionsReady(w, r) {
		return
	}
	rows, err := s.arrConnections.InstanceIndexers(r.Context(), id)
	if err != nil {
		s.writeArrConnectionError(w, err)
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

// UseArrInstanceIndexers adds the named Prowlarr indexers, with the connection's own key.
func (s *Server) UseArrInstanceIndexers(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.arrConnectionsReady(w, r) {
		return
	}
	var body gen.UseArrInstanceIndexersJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "expected prowlarr_ids")
		return
	}
	if err := s.auth.Registry.LookupAll(settings.EntityIndexer); err != nil {
		writeError(w, s.logger, err)
		return
	}
	added, err := s.arrConnections.UseInstanceIndexers(r.Context(), id, body.ProwlarrIds)
	if err != nil {
		s.writeArrConnectionError(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]int64{"added": added})
}

// ListArrInstanceDownloadClients answers a connection's download clients.
func (s *Server) ListArrInstanceDownloadClients(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.arrConnectionsReady(w, r) {
		return
	}
	rows, err := s.arrConnections.InstanceDownloadClients(r.Context(), id)
	if err != nil {
		s.writeArrConnectionError(w, err)
		return
	}
	views := make([]gen.ArrDownloadClient, len(rows))
	for i, row := range rows {
		views[i] = gen.ArrDownloadClient{
			Name: row.Name, Impl: row.Impl, Protocol: row.Protocol, Enabled: row.Enabled, BaseUrl: row.BaseURL,
			Supported: row.Supported, AlreadyAdded: row.AlreadyAdded,
		}
		if row.Username != "" {
			username := row.Username
			views[i].Username = &username
		}
		if row.Category != "" {
			category := row.Category
			views[i].Category = &category
		}
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}
