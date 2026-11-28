package adapters

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/importing/arrmap"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// LidarrClientFactory builds a REST client from an instance's URL and decrypted key; injected so this package never builds an httpx.Client (providers.go does).
type LidarrClientFactory func(baseURL, apiKey string) arrimport.LidarrClient

// ArrSync runs one *Arr import (todo 4.13, ADR-52); Sync returns once the run is RUNNING and the walk continues detached, because a large library outlives the HTTP write timeout (callers poll GET /arr/imports/{id}).
type ArrSync struct {
	Cache    *settings.Cache
	Identity library.Identity
	People   library.People
	Imports  library.ArrImports
	Reverter library.ArrImportRevert
	Clock    clock.Clock
	// SafeMode refuses everything that writes to an *Arr (documentation/safe-mode.md).
	SafeMode bool
	// Desired and Subscriptions read the monitoring a push sends back.
	Desired       library.Desired
	Subscriptions library.Subscriptions
	Lidarr        LidarrClientFactory
	// Probe builds the client a discovery probe tries one address with; nil finds nothing.
	Probe ProbeClientFactory
	// Importers is each app's mapping onto its media type, set where the modules are registered.
	Importers map[settings.ArrApp]arrmap.Importer
	// Links remembers what each *Arr item is here, for sending monitoring back.
	Links library.ArrLinks
	// Setup adds the *Arr's indexers and download clients an import brings; nil brings none.
	Setup ArrSetup
	// AfterFolders runs after a pull added root folders, to scan them; nil does nothing.
	AfterFolders func(ctx context.Context)
	// Registry answers each media type's defaults a preview compares against; nil compares against none.
	Registry *media.Registry
	// Progress holds the live figure of running imports; nil keeps none.
	Progress *ArrProgress
	Logger   *slog.Logger
}

// ArrProgress is how far each running import has walked its artists; in memory, since it only matters while
// the walk goroutine lives.
type ArrProgress struct {
	mu     sync.Mutex
	runs   map[int64][2]int64
	counts map[int64]library.ArrImportCounts
}

// NewArrProgress builds an empty board.
func NewArrProgress() *ArrProgress {
	return &ArrProgress{runs: map[int64][2]int64{}, counts: map[int64]library.ArrImportCounts{}}
}

func (p *ArrProgress) set(importID, done, total int64) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.runs[importID] = [2]int64{done, total}
}

// setCounts keeps what the walk has added so far, which the database only learns when the run ends.
func (p *ArrProgress) setCounts(importID int64, counts library.ArrImportCounts) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.counts[importID] = counts
}

func (p *ArrProgress) clear(importID int64) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.runs, importID)
	delete(p.counts, importID)
}

// annotate adds the live figure to a run still walking.
func (p *ArrProgress) annotate(view apiserver.ArrImportRun) apiserver.ArrImportRun {
	if p == nil || view.State != library.ArrImportRunning {
		return view
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if figure, found := p.runs[view.ID]; found {
		view.ArtistsDone, view.ArtistsTotal = figure[0], figure[1]
	}
	if counts, found := p.counts[view.ID]; found {
		view.ArtistsCreated, view.ArtistsMatched = counts.ArtistsCreated, counts.ArtistsMatched
		view.AlbumsCreated, view.AlbumsMatched, view.ItemsParked = counts.AlbumsCreated, counts.AlbumsMatched, counts.ItemsParked
	}
	return view
}

// Sync records a RUNNING run regardless of outcome so a connection failure leaves a visible row, and runs the walk detached from ctx because the request context ends on return.
func (a ArrSync) Sync(ctx context.Context, instanceID int64, options apiserver.ArrSyncOptions) (apiserver.ArrImportRun, error) {
	instance, err := a.Cache.GetArrInstance(ctx, instanceID)
	if err != nil {
		return apiserver.ArrImportRun{}, err
	}
	if !a.supported(instance.App) {
		return apiserver.ArrImportRun{}, fmt.Errorf("%w: %s", apiserver.ErrArrAppNotYetSupported, instance.App)
	}
	apiKey, err := a.Cache.DecryptedAPIKey(ctx, instanceID)
	if err != nil {
		return apiserver.ArrImportRun{}, err
	}

	now := a.Clock.Now()
	importID, err := a.Imports.Start(ctx, instanceID, string(instance.App), now.Unix())
	if errors.Is(err, library.ErrArrImportRunning) {
		return apiserver.ArrImportRun{}, apiserver.ErrArrSyncAlreadyRunning
	}
	if err != nil {
		return apiserver.ArrImportRun{}, err
	}

	client := a.Lidarr(instance.BaseURL, apiKey.Reveal())
	go a.runAndFinish(context.WithoutCancel(ctx), importID, instanceID, instance, client, options)

	run, err := a.Imports.Get(ctx, importID)
	if err != nil {
		return apiserver.ArrImportRun{}, err
	}
	return a.Progress.annotate(arrImportRunView(run)), nil
}

// runAndFinish is the detached second half: the walk, then its outcome; ctx is already uncancelled (context.WithoutCancel) so it outlives the request.
func (a ArrSync) runAndFinish(
	ctx context.Context, importID, instanceID int64, instance settings.ArrInstance, client arrimport.LidarrClient,
	options apiserver.ArrSyncOptions,
) {
	settingsErr := errors.Join(a.takeSettings(ctx, instance.App, client, options.SettingsInto), a.takeNaming(ctx, instance.App, client, options.NamingInto))
	foldersErr := a.rootFolders(ctx, instance, client, options)
	// Indexers, clients and lists are small and first, so a long library walk never hides them.
	notes, setupErr := a.importSetup(ctx, instance, client, options)
	listsErr := a.importLists(ctx, instance, client, options)
	counts, syncErr := a.Importers[instance.App].Import(ctx, a.run(importID, instance, client, options))
	defer a.Progress.clear(importID)
	if setupErr != nil {
		syncErr = errors.Join(syncErr, setupErr)
	}

	finishedAt := a.Clock.Now().Unix()
	errMsg := ""
	if syncErr != nil {
		errMsg = syncErr.Error()
	}
	if settingsErr != nil {
		errMsg = strings.TrimPrefix(errMsg+"; settings: "+settingsErr.Error(), "; ")
	}
	if foldersErr != nil {
		errMsg = strings.TrimPrefix(errMsg+"; "+foldersErr.Error(), "; ")
	}
	if listsErr != nil {
		errMsg = strings.TrimPrefix(errMsg+"; "+listsErr.Error(), "; ")
	}
	// A connection or artist-listing failure leaves counts at zero, so the run is FAILED; a per-artist or per-album failure keeps real counts and stays DONE with an error summary.
	state := library.ArrImportDone
	if syncErr != nil && counts == (library.ArrImportCounts{}) {
		state = library.ArrImportFailed
	}
	if err := a.Imports.Finish(ctx, importID, state, finishedAt, errMsg, notes, counts); err != nil {
		if a.Logger != nil {
			a.Logger.Error("arr import: recording run outcome failed",
				slog.Int64("import_id", importID), slog.Any("error", err))
		}
		return
	}
	// Best-effort: a failure here would otherwise hide a completed run
	// behind an error, and the run's own DONE row is already the source of
	// truth for what happened.
	if err := a.Cache.SetArrInstanceSyncResult(ctx, instanceID, finishedAt, errMsg); err != nil && a.Logger != nil {
		a.Logger.Warn("arr import: recording instance sync result failed",
			slog.Int64("instance_id", instanceID), slog.Any("error", err))
	}
}

// run is one import as a mapping sees it: what a walk records and links, the walk's place and progress.
func (a ArrSync) run(importID int64, instance settings.ArrInstance, client arrimport.LidarrClient, options apiserver.ArrSyncOptions) arrmap.Run {
	return arrmap.Run{
		Client: client, Logger: a.Logger,
		Options: arrmap.Options{
			Monitored: options.Monitored, Unmonitored: options.Unmonitored, Present: options.Present, Albums: options.Albums, ProfileMap: options.ProfileMap,
			MonitoringOnly: options.MonitoringOnly, Limit: options.Limit,
		},
		Place: func(rootFolder string) (int64, bool) {
			return a.categoryFor(instance, rootFolder, rewritePath(rootFolder, options.PathRewrites), a.Cache.Snapshot().RootFolders())
		},
		Progress: func(done, total int64) { a.Progress.set(importID, done, total) },
		Counted:  func(counts library.ArrImportCounts) { a.Progress.setCounts(importID, counts) },
		Record: func(ctx context.Context, item library.NewArrImportItem) error {
			_, err := a.Imports.AddItem(ctx, importID, item, a.Clock.Now().Unix())
			return err
		},
		Link: func(ctx context.Context, sourceKind string, sourceID int64, localKind string, localID int64) {
			if localID == 0 {
				return
			}
			err := a.Links.Link(ctx, instance.ID, library.ArrLink{SourceKind: sourceKind, SourceID: strconv.FormatInt(sourceID, 10), LocalKind: localKind, LocalID: localID})
			if err != nil && a.Logger != nil {
				a.Logger.Warn("arr import: link not kept", "kind", sourceKind, "id", sourceID, "error", err)
			}
		},
	}
}

// takeSettings gives each library named the *Arr's Media Management as its File Management, before the walk places
// anything there; a library that cannot take it keeps its own and the run says so.
func (a ArrSync) takeSettings(ctx context.Context, app settings.ArrApp, client arrimport.LidarrClient, libraries []int64) error {
	if len(libraries) == 0 || app.ComicManager() {
		return nil
	}
	var from arrimport.MediaManagement
	var err error
	switch app {
	case settings.ArrAppSonarr:
		from, err = client.Sonarr().MediaManagement(ctx)
	case settings.ArrAppRadarr:
		from, err = client.Radarr().MediaManagement(ctx)
	case settings.ArrAppWhisparr:
		from, err = client.Whisparr().MediaManagement(ctx)
	default:
		from, err = client.MediaManagement(ctx)
	}
	if err != nil {
		return fmt.Errorf("read media management: %w", err)
	}
	var failures []error
	for _, id := range libraries {
		category, found := a.Cache.Snapshot().Category(id)
		if !found {
			failures = append(failures, fmt.Errorf("library %d not found", id))
			continue
		}
		next, _ := arrFileManagement(category.FileManagement, from, app, a.extraFileDefaults(category.MediaType))
		if err := a.Cache.SetCategoryFileManagement(ctx, id, next); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", category.Name, err))
		}
	}
	return errors.Join(failures...)
}

// readNaming answers an app's naming settings; the comic managers have none.
func readNaming(ctx context.Context, app settings.ArrApp, client arrimport.LidarrClient) (arrimport.Naming, error) {
	switch app {
	case settings.ArrAppSonarr:
		return client.Sonarr().Naming(ctx)
	case settings.ArrAppRadarr:
		return client.Radarr().Naming(ctx)
	case settings.ArrAppReadarr:
		return client.Readarr().Naming(ctx)
	case settings.ArrAppWhisparr:
		return client.Whisparr().Naming(ctx)
	}
	return client.Naming(ctx)
}

// takeNaming gives each library the *Arr's naming formats translated into its own; a format it cannot express stays as the library has it.
func (a ArrSync) takeNaming(ctx context.Context, app settings.ArrApp, client arrimport.LidarrClient, libraries []int64) error {
	if len(libraries) == 0 || app.ComicManager() {
		return nil
	}
	config, err := readNaming(ctx, app, client)
	if err != nil {
		return fmt.Errorf("read naming: %w", err)
	}
	var failures []error
	for _, id := range libraries {
		category, found := a.Cache.Snapshot().Category(id)
		if !found {
			failures = append(failures, fmt.Errorf("library %d not found", id))
			continue
		}
		module, ok := a.namingModule(category.MediaType)
		if !ok {
			continue
		}
		profile := module.DefaultNamingProfile()
		if category.Naming != nil {
			profile = *category.Naming
		}
		next, _, _ := arrNaming(module, profile, config)
		if err := a.Cache.SetCategoryNaming(ctx, id, next); err != nil {
			failures = append(failures, fmt.Errorf("%s: naming: %w", category.Name, err))
		}
	}
	return errors.Join(failures...)
}

// namingModule is a media type's naming, when it declares one.
func (a ArrSync) namingModule(mediaType string) (media.Naming, bool) {
	if a.Registry == nil {
		return nil, false
	}
	return media.Capability[media.Naming](a.Registry, media.Key(mediaType))
}

// supported is every app an import is mapped for.
func (a ArrSync) supported(app settings.ArrApp) bool {
	_, found := a.Importers[app]
	return found
}

// RootFolders reads one instance's own configured root folders live -- the
// setup wizard's own "import the root folder(s)" step, and the only place
// this package builds a client without also running a sync.
func (a ArrSync) RootFolders(ctx context.Context, instanceID int64) ([]string, error) {
	instance, err := a.Cache.GetArrInstance(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	if !a.supported(instance.App) {
		return nil, fmt.Errorf("%w: %s", apiserver.ErrArrAppNotYetSupported, instance.App)
	}
	apiKey, err := a.Cache.DecryptedAPIKey(ctx, instanceID)
	if err != nil {
		return nil, err
	}

	client := a.Lidarr(instance.BaseURL, apiKey.Reveal())
	folders, err := arrRootFolders(ctx, instance.App, client)
	if err != nil {
		return nil, err
	}
	paths := make([]string, len(folders))
	for index, folder := range folders {
		paths[index] = folder.Path
	}
	return paths, nil
}

// ListImports answers every run for one instance, newest first.
func (a ArrSync) ListImports(ctx context.Context, instanceID int64) ([]apiserver.ArrImportRun, error) {
	runs, err := a.Imports.ListForInstance(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	views := make([]apiserver.ArrImportRun, len(runs))
	for index, run := range runs {
		views[index] = a.Progress.annotate(arrImportRunView(run))
	}
	return views, nil
}

// Forget removes the links of a connection that is being deleted; its import runs stay as the undo history.
func (a ArrSync) Forget(ctx context.Context, instanceID int64) error {
	runs, err := a.Imports.ListForInstance(ctx, instanceID)
	if err != nil {
		return err
	}
	running := int64(0)
	for _, run := range runs {
		if run.State == library.ArrImportRunning {
			running++
		}
	}
	if running > 0 {
		return &settings.InUseError{Entity: "*Arr connection", ID: instanceID, References: []settings.Reference{{Kind: "imports still running", Count: running}}}
	}
	return a.Links.Forget(ctx, instanceID)
}

// GetImport answers one run's own detail, including every item it touched.
func (a ArrSync) GetImport(ctx context.Context, id int64) (apiserver.ArrImportDetail, error) {
	run, err := a.Imports.Get(ctx, id)
	if err != nil {
		return apiserver.ArrImportDetail{}, err
	}
	items, err := a.Imports.ListItems(ctx, id)
	if err != nil {
		return apiserver.ArrImportDetail{}, err
	}
	views := make([]apiserver.ArrImportItem, len(items))
	for index, item := range items {
		views[index] = arrImportItemView(item)
	}
	return apiserver.ArrImportDetail{Run: a.Progress.annotate(arrImportRunView(run)), Items: views}, nil
}

// Revert undoes one run -- library.ErrArrImportNotDone translated to this
// package's own boundary error, the same shape every other adapter keeps
// (ImportRevert's own ErrImportSessionReverted pair).
func (a ArrSync) Revert(ctx context.Context, id int64) (apiserver.ArrImportRevertResult, error) {
	result, err := a.Reverter.Revert(ctx, id)
	if errors.Is(err, library.ErrArrImportNotDone) {
		return apiserver.ArrImportRevertResult{}, apiserver.ErrArrImportNotDone
	}
	if err != nil {
		return apiserver.ArrImportRevertResult{}, err
	}
	errs := make([]string, len(result.Errors))
	for index, itemErr := range result.Errors {
		errs[index] = itemErr.Error()
	}
	return apiserver.ArrImportRevertResult{
		WorksDeleted: result.WorksDeleted, SubscriptionsDeleted: result.SubscriptionsDeleted,
		PeopleDeleted: result.PeopleDeleted, Errors: errs,
	}, nil
}

func arrImportRunView(run library.ArrImportRun) apiserver.ArrImportRun {
	return apiserver.ArrImportRun{
		ID: run.ID, InstanceID: run.InstanceID, App: run.App, State: run.State,
		StartedAt: run.StartedAt, FinishedAt: run.FinishedAt, Error: run.Error, Notes: run.Notes,
		ArtistsCreated: run.ArtistsCreated, ArtistsMatched: run.ArtistsMatched,
		AlbumsCreated: run.AlbumsCreated, AlbumsMatched: run.AlbumsMatched, ItemsParked: run.ItemsParked,
	}
}

func arrImportItemView(item library.ArrImportItem) apiserver.ArrImportItem {
	return apiserver.ArrImportItem{
		ID: item.ID, ImportID: item.ImportID, Kind: item.Kind, SourceID: item.SourceID, Title: item.Title,
		PersonID: item.PersonID, PersonCreated: item.PersonCreated, SubscriptionID: item.SubscriptionID,
		WorkID: item.WorkID, WorkCreated: item.WorkCreated,
		DesiredItemID: item.DesiredItemID, DesiredItemCreated: item.DesiredItemCreated,
		CreatedAt: item.CreatedAt,
	}
}

// categoryFor picks a Lidarr artist's category: the library chosen for its root folder wins, then instance.CategoryID when set (ADR-52), else the longest-matching configured root folder (fsx.Within).
func (a ArrSync) categoryFor(instance settings.ArrInstance, arrRootPath, rootFolderPath string, roots []settings.RootFolder) (int64, bool) {
	if libraryID, chosen := instance.Pull().LibraryOfRoot(arrRootPath); chosen {
		return libraryID, true
	}
	if instance.CategoryID != nil {
		return *instance.CategoryID, true
	}

	var best settings.RootFolder
	bestLen := -1
	for _, root := range roots {
		if fsx.Within(root.Path, rootFolderPath) && len(root.Path) > bestLen {
			best, bestLen = root, len(root.Path)
		}
	}
	if bestLen < 0 {
		return 0, false
	}
	categories := a.Cache.Snapshot().CategoriesForRootFolder(best.ID)
	if len(categories) == 0 {
		return 0, false
	}
	return categories[0], true
}

// rewritePath reads an *Arr path as it is here: the first rewrite whose prefix it is under applies.
func rewritePath(path string, rewrites []apiserver.PathRewrite) string {
	for _, rewrite := range rewrites {
		from := strings.TrimRight(rewrite.From, "/")
		if from == "" {
			continue
		}
		if path == from || strings.HasPrefix(path, from+"/") {
			return strings.TrimRight(rewrite.To, "/") + strings.TrimPrefix(path, from)
		}
	}
	return path
}
