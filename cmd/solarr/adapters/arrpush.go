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
	"github.com/makearr/Solarr/internal/jobs"
)

// PushResult is what one send of monitoring did: how many items it looked at and how many it changed at the *Arr.
type PushResult = apiserver.ArrPushResult

// Run is the job of the schedule that sends monitoring back: every connection that asked for it, one after another.
func (a ArrSync) Run(ctx context.Context, _ jobs.Job) error {
	instances, err := a.Cache.ArrInstances(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, instance := range instances {
		if !instance.Enabled || !instance.PushMonitoring || !arrAppSupported(instance.App) {
			continue
		}
		if _, err := a.PushMonitoring(ctx, instance.ID); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", instance.Name, err))
		}
	}
	return errors.Join(failures...)
}

// PushMonitoring writes Solarr's monitoring of every item an import matched back to the connection's *Arr, where it
// differs. Only items an import linked are touched, and only their monitored flag; Solarr's state is the one that stands.
func (a ArrSync) PushMonitoring(ctx context.Context, instanceID int64) (apiserver.ArrPushResult, error) {
	instance, err := a.Cache.GetArrInstance(ctx, instanceID)
	if err != nil {
		return apiserver.ArrPushResult{}, err
	}
	if !instance.PushMonitoring {
		return apiserver.ArrPushResult{}, errors.New("this connection does not send monitoring back")
	}
	if !arrAppSupported(instance.App) {
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
	push := pusher{sync: a, client: client, links: byKind}
	switch instance.App {
	case settings.ArrAppSonarr:
		err = push.series(ctx)
	case settings.ArrAppRadarr:
		err = push.movies(ctx)
	case settings.ArrAppWhisparr:
		err = push.whisparr(ctx)
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
	sync   ArrSync
	client arrimport.LidarrClient
	links  map[string]map[string]library.ArrLink
	result apiserver.ArrPushResult
}

// local says whether Solarr monitors what a link names: a work when any of its wanted items is monitored, a person or a
// studio when someone follows them.
func (p pusher) local(ctx context.Context, link library.ArrLink) (bool, error) {
	switch link.LocalKind {
	case "work":
		items, err := p.sync.Desired.AlbumItemsForWork(ctx, link.LocalID)
		if err != nil {
			return false, err
		}
		for _, item := range items {
			if item.Monitored == 1 {
				return true, nil
			}
		}
		return false, nil
	default:
		follows, err := p.sync.Subscriptions.FollowsOf(ctx, library.Subject{Type: link.LocalKind, ID: link.LocalID}, 0)
		return len(follows) > 0, err
	}
}

// send compares one linked item's flag at the *Arr with Solarr's and writes the difference.
func (p *pusher) send(ctx context.Context, kind string, sourceID int64, upstream bool, path string, write func(context.Context, string, int64, bool) error) error {
	link, linked := p.links[kind][strconv.FormatInt(sourceID, 10)]
	if !linked {
		return nil
	}
	p.result.Checked++
	here, err := p.local(ctx, link)
	if err != nil || here == upstream {
		return err
	}
	if err := write(ctx, path, sourceID, here); err != nil {
		return fmt.Errorf("%s %d: %w", kind, sourceID, err)
	}
	p.result.Changed++
	return nil
}

func (p *pusher) movies(ctx context.Context) error {
	radarr := p.client.Radarr()
	movies, err := radarr.Movies(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, movie := range movies {
		failures = append(failures, p.send(ctx, linkMovie, movie.ID, movie.Monitored, "/api/v3/movie", radarr.SetMonitored))
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
		failures = append(failures, p.send(ctx, linkSeries, one.ID, one.Monitored, "/api/v3/series", sonarr.SetMonitored))
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
		failures = append(failures, p.send(ctx, linkArtist, artist.ID, artist.Monitored, "/api/v1/artist", p.client.SetMonitored))
		if _, linked := p.links[linkArtist][strconv.FormatInt(artist.ID, 10)]; !linked {
			continue
		}
		albums, err := p.client.Albums(ctx, artist.ID)
		if err != nil {
			failures = append(failures, fmt.Errorf("albums of %s: %w", artist.ArtistName, err))
			continue
		}
		for _, album := range albums {
			failures = append(failures, p.send(ctx, linkAlbum, album.ID, album.Monitored, "/api/v1/album", p.client.SetMonitored))
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
		failures = append(failures, p.send(ctx, linkPerformer, performer.ID, performer.Monitored, arrimport.WhisparrPerformerPath, whisparr.SetMonitored))
	}
	studios, err := whisparr.Studios(ctx)
	if err != nil {
		return errors.Join(append(failures, err)...)
	}
	for _, studio := range studios {
		failures = append(failures, p.send(ctx, linkStudio, studio.ID, studio.Monitored, arrimport.WhisparrStudioPath, whisparr.SetMonitored))
	}
	if len(p.links[linkScene]) > 0 {
		scenes, err := whisparr.Items(ctx)
		if err != nil {
			return errors.Join(append(failures, err)...)
		}
		for _, scene := range scenes {
			failures = append(failures, p.send(ctx, linkScene, scene.ID, scene.Monitored, arrimport.WhisparrScenePath, whisparr.SetMonitored))
		}
	}
	return errors.Join(failures...)
}
