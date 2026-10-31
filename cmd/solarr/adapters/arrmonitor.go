package adapters

import (
	"context"
	"fmt"
	"slices"
	"strconv"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/media"
)

// skipped is the tally of an item a monitoring-only import found no counterpart of here: nothing was added.
var skipped = library.ArrImportCounts{ItemsParked: 1}

// monitorChoice is the monitor option of a media type that means "monitored" (its widest) or "not": the type's own words
// for it, from its descriptor.
func monitorChoice(descriptor media.Descriptor, monitored bool) (string, bool) {
	if len(descriptor.MonitorOptions) == 0 {
		return "", false
	}
	if !monitored {
		for _, option := range descriptor.MonitorOptions {
			if option.Key == "none" {
				return option.Key, true
			}
		}
		return "", false
	}
	for _, preferred := range []string{"all", "scene_only", "movie_only", "book_only"} {
		if slices.ContainsFunc(descriptor.MonitorOptions, func(option media.Choice) bool { return option.Key == preferred }) {
			return preferred, true
		}
	}
	for _, option := range descriptor.MonitorOptions {
		if option.Key != "none" {
			return option.Key, true
		}
	}
	return "", false
}

// monitorWork sets the monitoring of the work of the media type that the provider names by id, through the type's own
// monitor option, else item by item; false when Solarr holds no such work.
func (a ArrSync) monitorWork(ctx context.Context, mediaType, provider, externalID string, monitored bool) (int64, bool, error) {
	if externalID == "" || externalID == "0" {
		return 0, false, nil
	}
	workID, held, err := a.Series.WorkByProviderID(ctx, mediaType, provider, externalID)
	if err != nil || !held {
		return 0, false, err
	}
	items, err := a.Desired.AlbumItemsForWork(ctx, workID)
	if err != nil || len(items) == 0 {
		return 0, false, err
	}
	if a.Registry != nil {
		if module, ok := a.Registry.Module(media.Key(mediaType)); ok {
			if monitoring, ok := module.(media.Monitoring); ok {
				if option, known := monitorChoice(module.Descriptor(), monitored); known {
					return workID, true, monitoring.ApplyMonitorOption(ctx, items[0].CategoryID, workID, option, false)
				}
			}
		}
	}
	for _, item := range items {
		if err := a.Desired.SetItemMonitored(ctx, item.ID, monitored); err != nil {
			return workID, true, err
		}
	}
	return workID, true, nil
}

// monitorPerson follows the person the provider names by id in the media type's libraries, or stops following them; false
// when Solarr holds no such person.
func (a ArrSync) monitorPerson(ctx context.Context, provider, externalID, mediaType, role string, categoryID int64, monitored bool) (int64, bool, error) {
	if externalID == "" {
		return 0, false, nil
	}
	personID, held, err := a.People.FindByExternalID(ctx, provider, externalID)
	if err != nil || !held {
		return 0, false, err
	}
	subject := library.Subject{Type: "person", ID: personID}
	if monitored {
		_, err := a.People.Subscribe(ctx, a.Clock.Now(), personID, 0, library.SubscriptionRequest{MediaTypes: []string{mediaType}, Roles: []string{role}, CategoryID: categoryID})
		return personID, true, err
	}
	follows, err := a.Subscriptions.FollowsOf(ctx, subject, 0)
	if err != nil {
		return personID, true, err
	}
	for _, follow := range follows {
		if err := a.Subscriptions.Unfollow(ctx, subject, 0, follow.CategoryID); err != nil {
			return personID, true, err
		}
	}
	return personID, true, nil
}

// monitorOrganisation is monitorPerson for a studio.
func (a ArrSync) monitorOrganisation(ctx context.Context, provider, externalID, mediaType string, categoryID int64, monitored bool) (int64, bool, error) {
	if externalID == "" {
		return 0, false, nil
	}
	organisationID, held, err := a.Organisations.FindByExternalID(ctx, provider, externalID)
	if err != nil || !held {
		return 0, false, err
	}
	subject := library.Subject{Type: "organisation", ID: organisationID}
	if monitored {
		_, err := a.Subscriptions.Follow(ctx, a.Clock.Now(), subject, 0, library.FollowRequest{CategoryID: categoryID, MediaType: mediaType})
		return organisationID, true, err
	}
	follows, err := a.Subscriptions.FollowsOf(ctx, subject, 0)
	if err != nil {
		return organisationID, true, err
	}
	for _, follow := range follows {
		if err := a.Subscriptions.Unfollow(ctx, subject, 0, follow.CategoryID); err != nil {
			return organisationID, true, err
		}
	}
	return organisationID, true, nil
}

// link remembers what an *Arr item is here, for sending monitoring back; a failure only costs that.
func (a ArrSync) link(ctx context.Context, sourceKind string, sourceID int64, localKind string, localID int64) {
	if a.instanceID == 0 || localID == 0 {
		return
	}
	err := a.Links.Link(ctx, a.instanceID, library.ArrLink{SourceKind: sourceKind, SourceID: strconv.FormatInt(sourceID, 10), LocalKind: localKind, LocalID: localID})
	if err != nil && a.Logger != nil {
		a.Logger.Warn("arr import: link not kept", "kind", sourceKind, "id", sourceID, "error", err)
	}
}

// found tallies one monitored item: matched when Solarr held it, else skipped.
func found(held bool, err error) (library.ArrImportCounts, error) {
	switch {
	case err != nil:
		return library.ArrImportCounts{}, err
	case held:
		return library.ArrImportCounts{ArtistsMatched: 1}, nil
	}
	return skipped, nil
}

// monitorMovie sets one Radarr movie's monitoring on the film Solarr holds under its TMDB id.
func (a ArrSync) monitorMovie(ctx context.Context, radarr arrimport.RadarrMovie) (library.ArrImportCounts, error) {
	workID, held, err := a.monitorWork(ctx, "movie", movieProviderTMDB, strconv.FormatInt(radarr.TMDBID, 10), radarr.Monitored)
	if held && err == nil {
		a.link(ctx, linkMovie, radarr.ID, "work", workID)
	}
	return found(held, err)
}

// monitorSeries gives the series Solarr holds under its TheTVDB id the monitoring Sonarr holds, episode by episode.
func (a ArrSync) monitorSeries(ctx context.Context, series arrimport.SonarrSeries, client sonarrEpisodes) (library.ArrImportCounts, error) {
	if series.TVDBID == 0 {
		return skipped, nil
	}
	workID, held, err := a.Series.WorkByProviderID(ctx, "tv", tvProviderTVDB, strconv.FormatInt(series.TVDBID, 10))
	if err != nil || !held {
		return found(false, err)
	}
	items, err := a.Desired.AlbumItemsForWork(ctx, workID)
	if err != nil || len(items) == 0 {
		return found(false, err)
	}
	a.link(ctx, linkSeries, series.ID, "work", workID)
	return found(true, a.carryOverMonitoring(ctx, items[0].CategoryID, workID, series, client))
}

// monitorArtist gives the artist and the albums Solarr holds under their MusicBrainz ids the monitoring Lidarr holds.
func (a ArrSync) monitorArtist(ctx context.Context, artist arrimport.LidarrArtist, client arrimport.LidarrClient) (library.ArrImportCounts, error) {
	personID, held, err := a.monitorPerson(ctx, musicProviderArtist, artist.ForeignArtistID, "music", "artist", 0, artist.Monitored)
	if err != nil || !held {
		return found(held, err)
	}
	a.link(ctx, linkArtist, artist.ID, "person", personID)
	counts := library.ArrImportCounts{ArtistsMatched: 1}
	albums, err := client.Albums(ctx, artist.ID)
	if err != nil {
		return counts, fmt.Errorf("list albums: %w", err)
	}
	for _, album := range albums {
		workID, albumHeld, err := a.monitorWork(ctx, "music", musicProviderReleaseGroup, album.ForeignAlbumID, album.Monitored)
		if err != nil {
			return counts, fmt.Errorf("%s: %w", album.Title, err)
		}
		if albumHeld {
			counts.AlbumsMatched++
			a.link(ctx, linkAlbum, album.ID, "work", workID)
		}
	}
	return counts, nil
}

// monitorWhisparr sets one of Whisparr's three kinds of item on what Solarr holds under the StashDB id Whisparr keeps for it.
func (a ArrSync) monitorWhisparr(ctx context.Context, categoryID int64, entry whisparrEntry) (library.ArrImportCounts, error) {
	switch entry.kind {
	case whisparrScene:
		workID, held, err := a.monitorWork(ctx, adultMediaType, adultProviderStashDB, entry.scene.Identifier(), entry.scene.Monitored)
		if held && err == nil {
			a.link(ctx, linkScene, entry.scene.ID, "work", workID)
		}
		return found(held, err)
	case whisparrPerformer:
		personID, held, err := a.monitorPerson(ctx, "stashdb:performer", entry.performer.ForeignID, adultMediaType, adultRolePerformer, categoryID, entry.performer.Monitored)
		if held && err == nil {
			a.link(ctx, linkPerformer, entry.performer.ID, "person", personID)
		}
		return found(held, err)
	}
	organisationID, held, err := a.monitorOrganisation(ctx, "stashdb:studio", entry.studio.ForeignID, adultMediaType, categoryID, entry.studio.Monitored)
	if held && err == nil {
		a.link(ctx, linkStudio, entry.studio.ID, "organisation", organisationID)
	}
	return found(held, err)
}

// The *Arr's own words for what a link is of, and where each is read and written.
const (
	linkArtist    = "artist"
	linkAlbum     = "album"
	linkSeries    = "series"
	linkMovie     = "movie"
	linkScene     = "scene"
	linkPerformer = "performer"
	linkStudio    = "studio"
)
