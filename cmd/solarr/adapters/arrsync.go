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
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// musicProviderArtist is the id a Lidarr artist is matched by.
const musicProviderArtist = music.ProviderArtist

// LidarrClientFactory builds a REST client for one instance's own base URL
// and decrypted API key -- injected so this package never builds an
// httpx.Client itself (cmd/solarr/app/providers.go's own job, the same
// rate limiter/breaker/proxy wiring every other provider already gets).
type LidarrClientFactory func(baseURL, apiKey string) arrimport.LidarrClient

// ArrSync runs one *Arr import (todo 4.13, ADR-52): a monitored artist
// becomes person+subscription, a monitored album becomes a desired_item,
// each matched by its own MusicBrainz id (ADR-8) so a Lidarr-driven work
// and a scan- or search-driven one always land on the same row.
//
// Sync returns as soon as the run is recorded RUNNING; the actual walk runs
// detached in its own goroutine, and a caller polls GET /arr/imports/{id}
// for DONE/FAILED. Originally built synchronous ("a Lidarr library's own
// realistic scale is nowhere near WALK/GROUP's 500k-file design point");
// live-verified wrong the same day against a real 766-artist library (one
// artist alone with 251 albums, each needing its own Albums call) that
// would have run well past this process's own HTTP write timeout -- exactly
// the "revisit if a real large library proves this wrong" this type's
// earlier doc comment already named as the thing to watch for.
type ArrSync struct {
	Cache            *settings.Cache
	Identity         library.Identity
	People           library.People
	Subscriptions    library.Subscriptions
	Desired          library.Desired
	Imports          library.ArrImports
	Reverter         library.ArrImportRevert
	DefaultProfileID int64
	Clock            clock.Clock
	Lidarr           LidarrClientFactory
	// TV adds the series a Sonarr import maps; Categories resolves a library's quality profile.
	TV TVAdder
	// Movies adds the movies a Radarr import maps.
	Movies     media.Adding
	Categories Categories
	// WarmFilms and WarmSeries read in bulk what Wikidata says of the films (TMDB ids) and series (TheTVDB ids) an
	// import is about to add, so each add finds it kept instead of one serial query apiece. Nil warms nothing.
	WarmFilms, WarmSeries func(ctx context.Context, ids []string) error
	// Registry answers each media type's defaults a preview compares against; nil compares against none.
	Registry *media.Registry
	// Progress holds the live figure of running imports; nil keeps none.
	Progress *ArrProgress
	Logger   *slog.Logger
}

// ArrProgress is how far each running import has walked its artists; in memory, since it only matters while
// the walk goroutine lives.
type ArrProgress struct {
	mu   sync.Mutex
	runs map[int64][2]int64
}

// NewArrProgress builds an empty board.
func NewArrProgress() *ArrProgress { return &ArrProgress{runs: map[int64][2]int64{}} }

func (p *ArrProgress) set(importID, done, total int64) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.runs[importID] = [2]int64{done, total}
}

func (p *ArrProgress) clear(importID int64) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.runs, importID)
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
	return view
}

// Sync starts one instance's own import and returns immediately with the
// RUNNING run -- recorded regardless of outcome, so a connection failure
// still leaves a row the UI can show, not a silently missing one. The
// actual walk (runAndFinish) runs detached from ctx: an HTTP request's own
// context is cancelled the instant this method returns, but the sync it
// started must keep running.
func (a ArrSync) Sync(ctx context.Context, instanceID int64, options apiserver.ArrSyncOptions) (apiserver.ArrImportRun, error) {
	instance, err := a.Cache.GetArrInstance(ctx, instanceID)
	if err != nil {
		return apiserver.ArrImportRun{}, err
	}
	if !arrAppSupported(instance.App) {
		return apiserver.ArrImportRun{}, fmt.Errorf("%w: %s", apiserver.ErrArrAppNotYetSupported, instance.App)
	}
	apiKey, err := a.Cache.DecryptedAPIKey(ctx, instanceID)
	if err != nil {
		return apiserver.ArrImportRun{}, err
	}

	existing, err := a.Imports.ListForInstance(ctx, instanceID)
	if err != nil {
		return apiserver.ArrImportRun{}, err
	}
	for _, run := range existing {
		if run.State == library.ArrImportRunning {
			return apiserver.ArrImportRun{}, apiserver.ErrArrSyncAlreadyRunning
		}
	}

	now := a.Clock.Now()
	importID, err := a.Imports.Start(ctx, instanceID, string(instance.App), now.Unix())
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

// runAndFinish is Sync's own detached second half: the real walk, then
// recording its outcome. ctx has already had Sync's own cancellation
// stripped (context.WithoutCancel) -- it must outlive the HTTP request that
// started it.
func (a ArrSync) runAndFinish(
	ctx context.Context, importID, instanceID int64, instance settings.ArrInstance, client arrimport.LidarrClient,
	options apiserver.ArrSyncOptions,
) {
	walk := arrWalk{importID: importID, options: options, progress: a.Progress, place: func(rootFolder string) (int64, bool) {
		return a.categoryFor(instance, rootFolder, a.Cache.Snapshot().RootFolders())
	}}
	var counts library.ArrImportCounts
	var syncErr error
	settingsErr := a.takeSettings(ctx, instance.App, client, options.SettingsInto)
	switch instance.App {
	case settings.ArrAppSonarr:
		counts, syncErr = walkArr(ctx, walk, a.sonarrSource(importID, client.Sonarr(), options))
	case settings.ArrAppRadarr:
		counts, syncErr = walkArr(ctx, walk, a.radarrSource(importID, client.Radarr(), options))
	default:
		counts, syncErr = walkArr(ctx, walk, a.lidarrSource(importID, client, options))
	}
	defer a.Progress.clear(importID)

	finishedAt := a.Clock.Now().Unix()
	errMsg := ""
	if syncErr != nil {
		errMsg = syncErr.Error()
	}
	if settingsErr != nil {
		errMsg = strings.TrimPrefix(errMsg+"; settings: "+settingsErr.Error(), "; ")
	}
	// A connection or artist-listing failure leaves every count at zero --
	// the run never got started, FAILED rather than a DONE with nothing to
	// show for it. A per-artist/per-album failure still leaves real counts
	// (syncLidarr's own best-effort tolerance), so that stays DONE with its
	// own error summarising what did not import.
	state := library.ArrImportDone
	if syncErr != nil && counts == (library.ArrImportCounts{}) {
		state = library.ArrImportFailed
	}
	if err := a.Imports.Finish(ctx, importID, state, finishedAt, errMsg, counts); err != nil {
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

// takeSettings gives each library named the *Arr's Media Management as its File Management, before the walk places
// anything there; a library that cannot take it keeps its own and the run says so.
func (a ArrSync) takeSettings(ctx context.Context, app settings.ArrApp, client arrimport.LidarrClient, libraries []int64) error {
	if len(libraries) == 0 {
		return nil
	}
	var from arrimport.MediaManagement
	var err error
	switch app {
	case settings.ArrAppSonarr:
		from, err = client.Sonarr().MediaManagement(ctx)
	case settings.ArrAppRadarr:
		from, err = client.Radarr().MediaManagement(ctx)
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

// arrAppSupported is every app an import is mapped for.
func arrAppSupported(app settings.ArrApp) bool {
	return app == settings.ArrAppLidarr || app == settings.ArrAppSonarr || app == settings.ArrAppRadarr
}

// RootFolders reads one instance's own configured root folders live -- the
// setup wizard's own "import the root folder(s)" step, and the only place
// this package builds a client without also running a sync.
func (a ArrSync) RootFolders(ctx context.Context, instanceID int64) ([]string, error) {
	instance, err := a.Cache.GetArrInstance(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	if !arrAppSupported(instance.App) {
		return nil, fmt.Errorf("%w: %s", apiserver.ErrArrAppNotYetSupported, instance.App)
	}
	apiKey, err := a.Cache.DecryptedAPIKey(ctx, instanceID)
	if err != nil {
		return nil, err
	}

	client := a.Lidarr(instance.BaseURL, apiKey.Reveal())
	var folders []arrimport.LidarrRootFolder
	switch instance.App {
	case settings.ArrAppSonarr:
		folders, err = client.Sonarr().RootFolders(ctx)
	case settings.ArrAppRadarr:
		folders, err = client.Radarr().RootFolders(ctx)
	default:
		folders, err = client.RootFolders(ctx)
	}
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
		StartedAt: run.StartedAt, FinishedAt: run.FinishedAt, Error: run.Error,
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

// lidarrSource walks Lidarr's artists: each becomes a person (followed when monitored), then its wanted albums
// become works with an album item, each matched by its MusicBrainz id. One album failing does not stop the artist's.
func (a ArrSync) lidarrSource(importID int64, client arrimport.LidarrClient, options apiserver.ArrSyncOptions) arrSource[arrimport.LidarrArtist] {
	return arrSource[arrimport.LidarrArtist]{
		connect: func(ctx context.Context) error { _, err := client.SystemStatus(ctx); return err },
		list:    client.Artists,
		describe: func(artist arrimport.LidarrArtist) arrItem {
			return arrItem{Name: artist.ArtistName, RootFolder: artist.RootFolderPath, Monitored: artist.Monitored}
		},
		workers: 1,
		sync: func(ctx context.Context, categoryID int64, artist arrimport.LidarrArtist) (library.ArrImportCounts, error) {
			var counts library.ArrImportCounts
			personID, created, err := a.syncArtist(ctx, importID, artist, artist.Monitored)
			if err != nil {
				return counts, err
			}
			if created {
				counts.ArtistsCreated++
			} else {
				counts.ArtistsMatched++
			}
			albums, err := client.Albums(ctx, artist.ID)
			if err != nil {
				return counts, fmt.Errorf("list albums: %w", err)
			}
			var failures []string
			for _, album := range albums {
				wanted := (album.Monitored && options.Monitored) || (!album.Monitored && options.Unmonitored)
				if !wanted || !albumWanted(album, options.Albums) {
					continue
				}
				workCreated, err := a.syncAlbum(ctx, importID, categoryID, personID, album, options.ProfileMap[artist.QualityProfileID])
				switch {
				case err != nil:
					failures = append(failures, fmt.Sprintf("%s: %v", album.Title, err))
				case workCreated:
					counts.AlbumsCreated++
				default:
					counts.AlbumsMatched++
				}
			}
			if len(failures) > 0 {
				return counts, errors.New(strings.Join(failures, "; "))
			}
			return counts, nil
		},
	}
}

// syncArtist resolves-or-creates the person behind artist's own MusicBrainz
// id and ensures their subscription, recording one arr_import_item either
// way. created reports whether this call is the one that created the
// person -- a brand-new person cannot have had a prior subscription either,
// so the subscription is created exactly when the person is.
func (a ArrSync) syncArtist(
	ctx context.Context, importID int64, artist arrimport.LidarrArtist, follow bool,
) (personID int64, created bool, err error) {
	if artist.ForeignArtistID == "" {
		return 0, false, errors.New("no MusicBrainz artist id")
	}
	now := a.Clock.Now()

	_, found, err := a.People.FindByExternalID(ctx, music.ProviderArtist, artist.ForeignArtistID)
	if err != nil {
		return 0, false, err
	}
	personID, err = a.People.ResolvePerson(ctx, now, library.PersonLink{
		Provider: music.ProviderArtist, ExternalID: artist.ForeignArtistID,
		Person: library.PersonIdentity{Name: artist.ArtistName},
	})
	if err != nil {
		return 0, false, err
	}
	created = !found

	// An unmonitored artist arrives as an artist in the library, not one followed for new albums.
	var subscription *int64
	if follow {
		subscriptionID, err := a.People.Subscribe(ctx, now, personID, 0, library.SubscriptionRequest{
			MediaTypes: []string{"music"}, Roles: []string{"artist"},
		})
		if err != nil {
			return 0, false, err
		}
		subscription = &subscriptionID
	}

	if _, err := a.Imports.AddItem(ctx, importID, library.NewArrImportItem{
		Kind: library.ArrImportItemArtist, SourceID: strconv.FormatInt(artist.ID, 10), Title: artist.ArtistName,
		PersonID: &personID, PersonCreated: created, SubscriptionID: subscription,
	}, now.Unix()); err != nil {
		return 0, false, err
	}
	return personID, created, nil
}

// syncAlbum resolves-or-creates the work behind album's own MusicBrainz
// release-group id and ensures its album-level desired_item, recording one
// arr_import_item either way. A newly created work gets personID credited
// as its primary artist (AddCredit's own role="artist" ord=0 case) -- an
// already-matched work is left exactly as whatever created it already
// credited, never overwritten.
func (a ArrSync) syncAlbum(
	ctx context.Context, importID, categoryID, personID int64, album arrimport.LidarrAlbum, mapped *int64,
) (workCreated bool, err error) {
	if album.ForeignAlbumID == "" {
		return false, errors.New("no MusicBrainz release-group id")
	}
	now := a.Clock.Now()

	resolutions, err := a.Identity.ResolveWorksDetailed(ctx, now, []library.WorkLink{{
		Provider: music.ProviderReleaseGroup, ExternalID: album.ForeignAlbumID, Confidence: 1.0,
		Work: library.WorkIdentity{
			MediaType: "music", CanonicalTitle: album.Title, SortTitle: music.SortTitle(album.Title),
		},
	}})
	if err != nil {
		return false, err
	}
	resolution := resolutions[0]

	if resolution.WorkCreated {
		if err := a.People.AddLeadCredit(ctx, resolution.WorkID, personID, music.CreditRoleArtist); err != nil {
			return false, err
		}
	}

	profileID := a.DefaultProfileID
	if category, ok := a.Cache.Snapshot().Category(categoryID); ok && category.QualityProfileID != nil {
		profileID = *category.QualityProfileID
	}
	if mapped != nil {
		profileID = *mapped
	}
	item, err := a.Desired.EnsureAlbumItem(ctx, library.AlbumRequest{
		WorkID: resolution.WorkID, CategoryID: categoryID, ProfileID: profileID, Monitored: album.Monitored,
	})
	if err != nil {
		return false, err
	}

	workID, itemID := resolution.WorkID, item.ID
	if _, err := a.Imports.AddItem(ctx, importID, library.NewArrImportItem{
		Kind: library.ArrImportItemAlbum, SourceID: strconv.FormatInt(album.ID, 10), Title: album.Title,
		WorkID: &workID, WorkCreated: resolution.WorkCreated,
		// A work this call did not create cannot be told apart from one
		// whose desired_item already existed without a pre-check this pass
		// skips (desired_item_created is informational only -- revert acts
		// on WorkCreated, which cascades desired_item with it).
		DesiredItemID: &itemID, DesiredItemCreated: resolution.WorkCreated,
	}, now.Unix()); err != nil {
		return false, err
	}
	return resolution.WorkCreated, nil
}

// categoryFor answers which category a Lidarr artist's own root folder path
// belongs to. instance.CategoryID, when set, always wins -- the simple
// "everything from this instance goes to one category" case ADR-52 allows
// without requiring path matching at all. Otherwise the longest-matching
// configured root folder (fsx.Within) names the category, the same
// longest-prefix reasoning a real filesystem's own mount resolution uses.
func (a ArrSync) categoryFor(instance settings.ArrInstance, rootFolderPath string, roots []settings.RootFolder) (int64, bool) {
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

// albumWanted is the albums option: every album, those the *Arr lacks a track of, or those it holds whole.
func albumWanted(album arrimport.LidarrAlbum, which string) bool {
	switch which {
	case "missing":
		return !album.Complete()
	case "complete":
		return album.Complete()
	default:
		return true
	}
}

// warm reads Wikidata in bulk ahead of a walk; a failure only costs speed, each add still asks on its own.
func (a ArrSync) warm(ctx context.Context, what string, warm func(context.Context, []string) error, ids []string) {
	logger := a.Logger
	if logger == nil {
		logger = slog.Default()
	}
	started := a.Clock.Now()
	if err := warm(ctx, ids); err != nil {
		logger.WarnContext(ctx, "arr import: warming wikidata failed", slog.String("works", what), slog.Any("error", err))
		return
	}
	logger.InfoContext(ctx, "arr import: wikidata warmed", slog.String("works", what), slog.Int("ids", len(ids)),
		slog.Duration("took", a.Clock.Now().Sub(started)))
}
