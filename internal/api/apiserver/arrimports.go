package apiserver

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// ArrSyncOptions is what one import brings in (asked 2026-09-30): monitored and unmonitored items,
// which albums by what the *Arr holds of them, and how its paths read here.
type ArrSyncOptions struct {
	Monitored, Unmonitored bool
	// Present also brings the items the *Arr holds files for, monitored or not.
	Present bool
	// RootFolders and ImportLists also bring the *Arr's own, into NewFoldersLibrary.
	RootFolders, ImportLists bool
	// Albums is "all", "missing" (the *Arr lacks a track) or "complete".
	Albums       string
	PathRewrites []PathRewrite
	// ProfileMap gives each *Arr quality profile's items a Solarr profile, nil for the library's; one not listed
	// takes the Solarr profile of the same name, else the library's.
	ProfileMap map[int64]*int64
	// SettingsInto are the libraries that take the *Arr's Media Management into their File Management first.
	SettingsInto []int64
	// NamingInto are the libraries that take the *Arr's naming formats, translated, before the import.
	NamingInto []int64
	// NewFoldersLibrary is where the *Arr's root folders unknown here are about to be added, for a preview.
	NewFoldersLibrary *int64
	// MonitoringOnly sets the monitored flag of what Solarr already holds, adds nothing and ignores the item choices.
	MonitoringOnly bool
	// Indexers and DownloadClients also add the *Arr's own, as the connection's.
	Indexers, DownloadClients bool
	// Limit stops after the first this many chosen items, to try an import on a few; zero is all of them.
	Limit int
}

// ArrImportPreview is what an import would do, read without changing anything (ADR-52's preview step).
type ArrImportPreview struct {
	// ItemKind is what the counts count: "artists", "series" or "movies".
	ItemKind string
	Roots    []ArrPreviewRoot
	Profiles []ArrPreviewProfile
	// Follows says which release types the followed items take, one entry per *Arr follow profile in use.
	Follows  []ArrPreviewFollow
	Settings []ArrPreviewSettings
	// NamingChanges are what the *Arr's naming would change against a new library of the app's media type, for a library not made yet.
	NamingChanges []ArrSettingChange
	// Setup counts the indexers, download clients and import lists the options would bring; nil when none are asked for.
	Setup *ArrPreviewSetup
}

// ArrPreviewSetup is what the *Arr's indexers, download clients and import lists would add here.
type ArrPreviewSetup struct {
	Indexers, IndexersWithoutKey, DownloadClients, ImportLists int64
	// Problem says why one of the reads failed; the counts of the others stand.
	Problem string
}

// ArrPreviewFollow is one *Arr follow profile in use: the monitored items on it, and the release types they are followed for.
type ArrPreviewFollow struct {
	Name                   string
	Items                  int64
	PrimaryTypes           []string
	ExcludedSecondaryTypes []string
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
	// NamingChanges are what taking the *Arr's naming formats would change, and what it cannot take.
	NamingChanges []ArrSettingChange
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
	Notes                          []string
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

// ArrSync runs and reverts *Arr imports; implemented in cmd/solarr/adapters so this package never imports internal/db/library.
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
	// Pull runs a sync by the options stored on the connection.
	Pull(ctx context.Context, instanceID int64) (ArrImportRun, error)
	// TestConnection asks an *Arr for its version with the key as typed, before anything is stored.
	TestConnection(ctx context.Context, app settings.ArrApp, baseURL, apiKey string) ArrConnectionTest
	// Discover answers the apps found on their usual addresses.
	Discover(ctx context.Context) []DiscoveredService
	// Forget drops what is kept of a connection about to be deleted, refusing while a run of it is going.
	Forget(ctx context.Context, instanceID int64) error
}

// Why a connection test failed, in the word the interface turns into advice.
const (
	ArrProblemWrongKey    = "wrong_key"
	ArrProblemWrongPort   = "wrong_port"
	ArrProblemUnreachable = "unreachable"
	ArrProblemOther       = "other"
)

// ArrConnectionTest is the answer of a connection test: the app's own name and version, or what is wrong.
type ArrConnectionTest struct {
	OK               bool
	AppName, Version string
	Problem, Message string
	// Languages are the ISO codes of the languages the app's profiles ask for; empty when it has none.
	Languages []string
}

// DiscoveredService is an app that answered on its usual address.
type DiscoveredService struct {
	Kind, Host, BaseURL, Version string
	Port                         int
}

// ArrPushResult is what one send of monitoring did; Unknown counts linked items Solarr holds no wanted item of, left alone.
type ArrPushResult struct{ Checked, Changed, Unknown int64 }

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
		writeJSON(w, s.logger, http.StatusOK, map[string]int64{"checked": result.Checked, "changed": result.Changed, "unknown": result.Unknown})
	}
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
	if !s.decodeBody(w, r, maxImportBody, &body, "the sync options could not be read") {
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
	options.NamingInto = derefOr(body.NamingInto, nil)
	options.NewFoldersLibrary = body.NewFoldersLibraryId
	options.MonitoringOnly = body.Mode != nil && *body.Mode == gen.ArrSyncOptionsModeMonitoring
	options.Indexers, options.DownloadClients = derefOr(body.Indexers, false), derefOr(body.DownloadClients, false)
	options.ImportLists = derefOr(body.ImportLists, false)
	options.Limit = derefOr(body.Limit, 0)
	if !options.MonitoringOnly && !options.Monitored && !options.Unmonitored && !options.Indexers && !options.DownloadClients && !options.ImportLists {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Nothing to import", "choose monitored items, unmonitored items or both")
		return ArrSyncOptions{}, false
	}
	return options, true
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
	if len(preview.Follows) > 0 {
		follows := make([]gen.ArrPreviewFollow, len(preview.Follows))
		for i, follow := range preview.Follows {
			follows[i] = gen.ArrPreviewFollow{
				Name: follow.Name, Items: follow.Items, PrimaryTypes: follow.PrimaryTypes, ExcludedSecondaryTypes: follow.ExcludedSecondaryTypes,
			}
		}
		view.Follows = &follows
	}
	if preview.Setup != nil {
		view.Setup = &gen.ArrPreviewSetup{
			Indexers: preview.Setup.Indexers, IndexersWithoutKey: preview.Setup.IndexersWithoutKey,
			DownloadClients: preview.Setup.DownloadClients, ImportLists: preview.Setup.ImportLists,
		}
		if preview.Setup.Problem != "" {
			view.Setup.Problem = &preview.Setup.Problem
		}
	}
	if len(preview.NamingChanges) > 0 {
		naming := make([]gen.ArrSettingChange, len(preview.NamingChanges))
		for j, change := range preview.NamingChanges {
			naming[j] = gen.ArrSettingChange{Key: change.Key, Label: change.Label, Current: change.Current, Proposed: change.Proposed}
		}
		view.NamingChanges = &naming
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
		naming := make([]gen.ArrSettingChange, len(library.NamingChanges))
		for j, change := range library.NamingChanges {
			naming[j] = gen.ArrSettingChange{Key: change.Key, Label: change.Label, Current: change.Current, Proposed: change.Proposed}
		}
		view.Settings[i] = gen.ArrPreviewSettings{LibraryId: library.LibraryID, Changes: changes}
		if len(naming) > 0 {
			view.Settings[i].NamingChanges = &naming
		}
	}
	return view
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
	if !s.requireAdministrator(w, r) {
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

// GetArrImport answers one run's own detail, including every item it touched.
func (s *Server) GetArrImport(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
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
	if len(run.Notes) > 0 {
		view.Notes = &run.Notes
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
	SyncIndexers(ctx context.Context, instanceID int64) (ProwlarrSyncResult, error)
}

// ProwlarrSyncResult counts what one Prowlarr sync did.
type ProwlarrSyncResult struct {
	Added, Updated, Paused, Resumed, Skipped int64
	Gone                                     int64
	ClientsAdded, ClientsWaiting             int64
}

func syncOptionsView(options settings.ProwlarrSync) gen.ProwlarrSyncOptions {
	return gen.ProwlarrSyncOptions{
		ActiveIndexers: options.ActiveIndexers, PausedIndexers: options.PausedIndexers, SupportedOnly: options.SupportedOnly,
		DownloadClients: options.DownloadClients, Proxies: options.Proxies, Automatic: options.Automatic,
		ProwlarrAsIndexer: &options.ProwlarrAsIndexer, FlaresolverrUrl: &options.FlareSolverrURL,
	}
}

func (s *Server) routeArrInstanceSync(w http.ResponseWriter, r *http.Request) {
	id, err := parsePathID(w, s, r, "id", "an instance id must be a number")
	if err != nil || !s.requireAdministrator(w, r) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.GetArrInstanceSyncOptions(w, r, id)
	case http.MethodPut:
		s.SetArrInstanceSyncOptions(w, r, id)
	default:
		s.SyncArrInstanceIndexers(w, r, id)
	}
}

// GetArrInstanceSyncOptions answers what a Prowlarr connection keeps in step.
func (s *Server) GetArrInstanceSyncOptions(w http.ResponseWriter, r *http.Request, id int64) {
	instance, err := s.auth.Settings.GetArrInstance(r.Context(), id)
	if err != nil {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such connection")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, syncOptionsView(instance.Sync()))
}

// SetArrInstanceSyncOptions stores what a Prowlarr connection keeps in step.
func (s *Server) SetArrInstanceSyncOptions(w http.ResponseWriter, r *http.Request, id int64) {
	var body gen.SetArrInstanceSyncOptionsJSONRequestBody
	if !s.decodeBody(w, r, maxSetupBody, &body, "expected the sync options") {
		return
	}
	options := settings.ProwlarrSync{
		ActiveIndexers: body.ActiveIndexers, PausedIndexers: body.PausedIndexers, SupportedOnly: body.SupportedOnly,
		DownloadClients: body.DownloadClients, Proxies: body.Proxies, Automatic: body.Automatic,
		ProwlarrAsIndexer: derefOr(body.ProwlarrAsIndexer, false), FlareSolverrURL: strings.TrimSpace(derefOr(body.FlaresolverrUrl, "")),
	}
	if _, err := s.auth.Settings.GetArrInstance(r.Context(), id); err != nil {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such connection")
		return
	}
	if err := s.auth.Settings.SetArrInstanceSyncOptions(r.Context(), id, options, s.wall.Now().Unix()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, syncOptionsView(options))
}

// SyncArrInstanceIndexers syncs a Prowlarr connection's indexers now.
func (s *Server) SyncArrInstanceIndexers(w http.ResponseWriter, r *http.Request, id int64) {
	if s.arrConnections == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "*Arr connections are not available")
		return
	}
	result, err := s.arrConnections.SyncIndexers(r.Context(), id)
	if err != nil {
		s.writeArrConnectionError(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, gen.ProwlarrSyncResult{
		Added: result.Added, Updated: result.Updated, Paused: result.Paused, Resumed: result.Resumed, Skipped: result.Skipped,
		Gone: &result.Gone, ClientsAdded: &result.ClientsAdded, ClientsWaiting: &result.ClientsWaiting,
	})
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
	if !s.decodeBody(w, r, maxImportBody, &body, "expected prowlarr_ids") {
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

func pullOptionsView(pull settings.ArrPull) gen.ArrPullOptions {
	view := gen.ArrPullOptions{
		Present: pull.Present, Monitored: pull.Monitored, Unmonitored: pull.Unmonitored, Indexers: pull.Indexers,
		DownloadClients: pull.DownloadClients, RootFolders: pull.RootFolders, ImportLists: pull.ImportLists, Automatic: pull.Automatic,
	}
	if pull.LibraryID != 0 {
		view.LibraryId = &pull.LibraryID
	}
	if pull.Albums != "" {
		albums := gen.ArrPullOptionsAlbums(pull.Albums)
		view.Albums = &albums
	}
	rewrites := make([]struct {
		From string `json:"from"`
		To   string `json:"to"`
	}, len(pull.PathRewrites))
	for index, rewrite := range pull.PathRewrites {
		rewrites[index].From, rewrites[index].To = rewrite.From, rewrite.To
	}
	view.PathRewrites = &rewrites
	roots := make([]gen.ArrRootLibrary, len(pull.RootLibraries))
	for index, one := range pull.RootLibraries {
		roots[index] = gen.ArrRootLibrary{Root: one.Root, LibraryId: one.LibraryID}
	}
	view.RootLibraries = &roots
	return view
}

func (s *Server) routeArrInstancePull(w http.ResponseWriter, r *http.Request) {
	id, err := parsePathID(w, s, r, "id", "an instance id must be a number")
	if err != nil || !s.requireAdministrator(w, r) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.GetArrInstancePull(w, r, id)
	case http.MethodPut:
		s.SetArrInstancePull(w, r, id)
	default:
		s.PullArrInstance(w, r, id)
	}
}

// GetArrInstancePull answers what a content connection brings in.
func (s *Server) GetArrInstancePull(w http.ResponseWriter, r *http.Request, id int64) {
	instance, err := s.auth.Settings.GetArrInstance(r.Context(), id)
	if err != nil {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such connection")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, pullOptionsView(instance.Pull()))
}

// SetArrInstancePull stores what a content connection brings in.
func (s *Server) SetArrInstancePull(w http.ResponseWriter, r *http.Request, id int64) {
	var body gen.SetArrInstancePullJSONRequestBody
	if !s.decodeBody(w, r, maxSetupBody, &body, "expected the pull options") {
		return
	}
	pull := settings.ArrPull{
		Present: body.Present, Monitored: body.Monitored, Unmonitored: body.Unmonitored, Indexers: body.Indexers,
		DownloadClients: body.DownloadClients, RootFolders: body.RootFolders, ImportLists: body.ImportLists, Automatic: body.Automatic,
		LibraryID: derefOr(body.LibraryId, 0), Albums: "all",
	}
	if body.Albums != nil {
		pull.Albums = string(*body.Albums)
	}
	for _, rewrite := range derefOr(body.PathRewrites, nil) {
		if strings.TrimSpace(rewrite.From) != "" && strings.TrimSpace(rewrite.To) != "" {
			pull.PathRewrites = append(pull.PathRewrites, settings.ArrPathRead{From: strings.TrimSpace(rewrite.From), To: strings.TrimSpace(rewrite.To)})
		}
	}
	for _, one := range derefOr(body.RootLibraries, nil) {
		if root := strings.TrimSpace(one.Root); root != "" && one.LibraryId > 0 {
			pull.RootLibraries = append(pull.RootLibraries, settings.ArrRootLibrary{Root: root, LibraryID: one.LibraryId})
		}
	}
	instance, err := s.auth.Settings.GetArrInstance(r.Context(), id)
	if err != nil {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such connection")
		return
	}
	if (pull.RootFolders || pull.ImportLists) && pull.LibraryID == 0 && instance.CategoryID == nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Choose a library", "root folders and import lists are added to a library; choose which")
		return
	}
	if err := s.auth.Settings.SetArrInstancePull(r.Context(), id, pull, s.wall.Now().Unix()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, pullOptionsView(pull))
}

// PullArrInstance pulls from the *Arr now, by the stored options.
func (s *Server) PullArrInstance(w http.ResponseWriter, r *http.Request, id int64) {
	if s.arrSync == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "*Arr import is not available")
		return
	}
	run, err := s.arrSync.Pull(r.Context(), id)
	switch {
	case errors.Is(err, ErrArrAppNotYetSupported):
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict, "Not for this app", "this app has nothing to pull from")
	case errors.Is(err, ErrArrSyncAlreadyRunning):
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict, "Already running", "this instance already has a sync in progress")
	case err != nil:
		writeError(w, s.logger, err)
	default:
		writeJSON(w, s.logger, http.StatusOK, arrImportRunView(run))
	}
}

// TestArrConnection tries an *Arr's address and key before it is added. Administrator only.
func (s *Server) TestArrConnection(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.arrSync == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "*Arr import is not available")
		return
	}
	var body gen.TestArrConnectionJSONRequestBody
	if !s.decodeBody(w, r, maxImportBody, &body, "expected app, base_url and api_key") {
		return
	}
	if strings.TrimSpace(body.BaseUrl) == "" {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "base_url is required")
		return
	}
	result := s.arrSync.TestConnection(r.Context(), settings.ArrApp(body.App), body.BaseUrl, body.ApiKey)
	view := gen.ArrConnectionTest{Ok: result.OK}
	if result.OK {
		view.AppName, view.Version = &result.AppName, &result.Version
		if len(result.Languages) > 0 {
			view.Languages = &result.Languages
		}
	} else {
		problem := gen.ArrConnectionTestProblem(result.Problem)
		view.Problem, view.Message = &problem, &result.Message
	}
	writeJSON(w, s.logger, http.StatusOK, view)
}

// DiscoverArrServices looks for the apps setup wants on their usual addresses. Administrator only.
func (s *Server) DiscoverArrServices(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.arrSync == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "*Arr import is not available")
		return
	}
	found := s.arrSync.Discover(r.Context())
	views := make([]gen.DiscoveredService, len(found))
	for index, service := range found {
		views[index] = gen.DiscoveredService{Kind: gen.DiscoveredServiceKind(service.Kind), Host: service.Host, Port: service.Port, BaseUrl: service.BaseURL}
		if service.Version != "" {
			version := service.Version
			views[index].Version = &version
		}
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}
