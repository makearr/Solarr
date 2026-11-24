package adapters

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/importing/arrmap"
	"github.com/makearr/Solarr/internal/jobs"
)

// ErrSafeMode is what a refused write to an *Arr says when the instance runs in safe mode.
var ErrSafeMode = errors.New("safe mode sends nothing to an *Arr")

// PushResult is what one send of monitoring did: how many items it looked at and how many it changed at the *Arr.
type PushResult = apiserver.ArrPushResult

// Run is the job of the schedule that sends monitoring back: every connection that asked for it, one after another.
func (a ArrSync) Run(ctx context.Context, _ jobs.Job) error {
	if a.SafeMode {
		if a.Logger != nil {
			a.Logger.Info("skipped: safe mode", "job", "arr monitoring push")
		}
		return nil
	}
	instances, err := a.Cache.ArrInstances(ctx)
	if err != nil {
		return err
	}
	mode := a.Cache.Snapshot().SolarrMode()
	var failures []error
	for _, instance := range instances {
		if !instance.Enabled || !mode.PushesMonitoring(instance.PushMonitoring) || !a.supported(instance.App) || instance.App.ComicManager() {
			continue
		}
		// Pull first: a change made in the *Arr since the last run is taken in before Solarr's older value is sent.
		if mode == settings.ModeArrCompanion {
			if err := a.pullMonitored(ctx, instance); err != nil {
				failures = append(failures, fmt.Errorf("%s: %w", instance.Name, err))
			}
		}
		if _, err := a.PushMonitoring(ctx, instance.ID); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", instance.Name, err))
		}
	}
	return errors.Join(failures...)
}

// pullMonitored brings what the *Arr monitors and Solarr lacks into Solarr, as a companion does after sending its own
// changes. It only follows an import the person ran and finished, so the folders and mappings were chosen by them.
func (a ArrSync) pullMonitored(ctx context.Context, instance settings.ArrInstance) error {
	runs, err := a.Imports.ListForInstance(ctx, instance.ID)
	if err != nil {
		return err
	}
	finished := false
	for _, run := range runs {
		if run.State == library.ArrImportDone {
			finished = true
		}
	}
	if !finished {
		return nil
	}
	if _, err := a.Sync(ctx, instance.ID, apiserver.ArrSyncOptions{Monitored: true, Albums: "all"}); err != nil && !errors.Is(err, apiserver.ErrArrSyncAlreadyRunning) {
		return err
	}
	return nil
}

// PushMonitoring writes Solarr's monitoring of every item an import matched back to the connection's *Arr, where it
// differs. Only items an import linked are touched, and only their monitored flag; Solarr's state is the one that stands.
func (a ArrSync) PushMonitoring(ctx context.Context, instanceID int64) (apiserver.ArrPushResult, error) {
	if a.SafeMode {
		return apiserver.ArrPushResult{}, ErrSafeMode
	}
	instance, err := a.Cache.GetArrInstance(ctx, instanceID)
	if err != nil {
		return apiserver.ArrPushResult{}, err
	}
	if mode := a.Cache.Snapshot().SolarrMode(); !mode.PushesMonitoring(instance.PushMonitoring) {
		if mode == settings.ModeObserve {
			return apiserver.ArrPushResult{}, errors.New("observe mode sends nothing to an *Arr; arr_companion does")
		}
		return apiserver.ArrPushResult{}, errors.New("this connection does not send monitoring back")
	}
	if !a.supported(instance.App) || instance.App.ComicManager() {
		return apiserver.ArrPushResult{}, fmt.Errorf("no sending back for %s", instance.App)
	}
	apiKey, err := a.Cache.DecryptedAPIKey(ctx, instanceID)
	if err != nil {
		return apiserver.ArrPushResult{}, err
	}
	links, err := a.Links.ForInstance(ctx, instanceID)
	if err != nil {
		return apiserver.ArrPushResult{}, err
	}
	byKind := map[string]map[string]library.ArrLink{}
	for _, link := range links {
		if byKind[link.SourceKind] == nil {
			byKind[link.SourceKind] = map[string]library.ArrLink{}
		}
		byKind[link.SourceKind][link.SourceID] = link
	}
	client := a.Lidarr(instance.BaseURL, apiKey.Reveal())
	push := pusher{sync: a, instanceID: instanceID, client: client, links: byKind}
	switch instance.App {
	case settings.ArrAppSonarr:
		err = push.series(ctx)
	case settings.ArrAppRadarr:
		err = push.movies(ctx)
	case settings.ArrAppWhisparr:
		err = push.whisparr(ctx)
	case settings.ArrAppReadarr:
		err = push.books(ctx)
	default:
		err = push.artists(ctx)
	}
	if recordErr := a.Cache.SetArrInstancePushed(ctx, instanceID, a.Clock.Now().Unix()); recordErr != nil && a.Logger != nil {
		a.Logger.Warn("arr push: recording the time failed", "instance_id", instanceID, "error", recordErr)
	}
	return push.result, err
}

// pusher sends one connection's differences.
type pusher struct {
	sync       ArrSync
	instanceID int64
	client     arrimport.LidarrClient
	links      map[string]map[string]library.ArrLink
	result     apiserver.ArrPushResult
}

// local says whether Solarr monitors what a link names: a work when any of its wanted items is monitored, a person or a
// studio when someone follows them. known is false for a work with no wanted item, which says nothing either way.
func (p pusher) local(ctx context.Context, link library.ArrLink) (monitored, known bool, err error) {
	switch link.LocalKind {
	case "work":
		items, err := p.sync.Desired.AlbumItemsForWork(ctx, link.LocalID)
		if err != nil || len(items) == 0 {
			return false, false, err
		}
		for _, item := range items {
			if item.Monitored == 1 {
				return true, true, nil
			}
		}
		return false, true, nil
	default:
		followed, err := p.sync.Subscriptions.Followed(ctx, library.Subject{Type: link.LocalKind, ID: link.LocalID})
		return followed, true, err
	}
}

// send compares one linked item's flag at the *Arr with Solarr's and writes what changed here. The value both sides last
// agreed on says which side moved: only the *Arr's change is left for the pull, only Solarr's is sent, and when both
// moved Solarr's stands. Nothing is written for an item Solarr holds no wanted item of.
func (p *pusher) send(ctx context.Context, kind string, sourceID int64, upstream bool, path string, write func(context.Context, string, int64, bool) error) error {
	link, linked := p.links[kind][strconv.FormatInt(sourceID, 10)]
	if !linked {
		return nil
	}
	p.result.Checked++
	here, known, err := p.local(ctx, link)
	if err != nil {
		return err
	}
	if !known {
		p.result.Unknown++
		return nil
	}
	if here == upstream {
		return p.remember(ctx, link, here)
	}
	if link.SyncedMonitored != nil && here == *link.SyncedMonitored {
		// Solarr did not change and the *Arr did: its value is not overwritten, the pull takes it.
		return nil
	}
	if err := write(ctx, path, sourceID, here); err != nil {
		return fmt.Errorf("%s %d: %w", kind, sourceID, err)
	}
	p.result.Changed++
	return p.remember(ctx, link, here)
}

// remember records the value both sides now hold, when it is not the one already recorded.
func (p *pusher) remember(ctx context.Context, link library.ArrLink, monitored bool) error {
	if link.SyncedMonitored != nil && *link.SyncedMonitored == monitored {
		return nil
	}
	return p.sync.Links.SetSynced(ctx, p.instanceID, link, monitored)
}

func (p *pusher) movies(ctx context.Context) error {
	radarr := p.client.Radarr()
	movies, err := radarr.Movies(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, movie := range movies {
		failures = append(failures, p.send(ctx, arrmap.LinkMovie, movie.ID, movie.Monitored, "/api/v3/movie", radarr.SetMonitored))
	}
	return errors.Join(failures...)
}

func (p *pusher) series(ctx context.Context) error {
	sonarr := p.client.Sonarr()
	series, err := sonarr.Series(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, one := range series {
		failures = append(failures, p.send(ctx, arrmap.LinkSeries, one.ID, one.Monitored, "/api/v3/series", sonarr.SetMonitored))
	}
	return errors.Join(failures...)
}

func (p *pusher) books(ctx context.Context) error {
	readarr := p.client.Readarr()
	books, err := readarr.Books(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, book := range books {
		failures = append(failures, p.send(ctx, arrmap.LinkBook, book.ID, book.Monitored, "/api/v1/book", readarr.SetMonitored))
	}
	return errors.Join(failures...)
}

func (p *pusher) artists(ctx context.Context) error {
	artists, err := p.client.Artists(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, artist := range artists {
		failures = append(failures, p.send(ctx, arrmap.LinkArtist, artist.ID, artist.Monitored, "/api/v1/artist", p.client.SetMonitored))
		if _, linked := p.links[arrmap.LinkArtist][strconv.FormatInt(artist.ID, 10)]; !linked {
			continue
		}
		albums, err := p.client.Albums(ctx, artist.ID)
		if err != nil {
			failures = append(failures, fmt.Errorf("albums of %s: %w", artist.ArtistName, err))
			continue
		}
		for _, album := range albums {
			failures = append(failures, p.send(ctx, arrmap.LinkAlbum, album.ID, album.Monitored, "/api/v1/album", p.client.SetMonitored))
		}
	}
	return errors.Join(failures...)
}

func (p *pusher) whisparr(ctx context.Context) error {
	whisparr := p.client.Whisparr()
	var failures []error
	performers, err := whisparr.Performers(ctx)
	if err != nil {
		return err
	}
	for _, performer := range performers {
		failures = append(failures, p.send(ctx, arrmap.LinkPerformer, performer.ID, performer.Monitored, arrimport.WhisparrPerformerPath, whisparr.SetMonitored))
	}
	studios, err := whisparr.Studios(ctx)
	if err != nil {
		return errors.Join(append(failures, err)...)
	}
	for _, studio := range studios {
		failures = append(failures, p.send(ctx, arrmap.LinkStudio, studio.ID, studio.Monitored, arrimport.WhisparrStudioPath, whisparr.SetMonitored))
	}
	if len(p.links[arrmap.LinkScene]) > 0 {
		scenes, err := whisparr.Items(ctx)
		if err != nil {
			return errors.Join(append(failures, err)...)
		}
		for _, scene := range scenes {
			failures = append(failures, p.send(ctx, arrmap.LinkScene, scene.ID, scene.Monitored, arrimport.WhisparrScenePath, whisparr.SetMonitored))
		}
	}
	return errors.Join(failures...)
}
