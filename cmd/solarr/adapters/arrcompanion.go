package adapters

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/importing/arrmap"
)

// Added is what Solarr does, as an *Arr's companion, when someone adds an item here: the *Arr is told to monitor it
// (added with a search when it lacks it) and searches and downloads itself; Solarr finds the file at its next scan.
// Movies by TMDB id go to Radarr and series by TVDB id to Sonarr; anything else is left to the person.
func (a ArrSync) Added(ctx context.Context, added apiserver.AddedWork) error {
	if a.Cache.Snapshot().SolarrMode() != settings.ModeArrCompanion {
		return nil
	}
	if target, ok := mediaTarget(added); ok {
		return a.companionMedia(ctx, added, target)
	}
	app, sourceID, ok := companionTarget(added)
	if !ok {
		return nil
	}
	instance, found, err := a.companionInstance(ctx, app, added.CategoryID)
	if err != nil || !found {
		return err
	}
	apiKey, err := a.Cache.DecryptedAPIKey(ctx, instance.ID)
	if err != nil {
		return err
	}
	client := a.Lidarr(instance.BaseURL, apiKey.Reveal())
	var arrID int64
	var kind string
	switch app {
	case settings.ArrAppRadarr:
		arrID, err = a.companionMovie(ctx, client.Radarr(), sourceID, added.Title)
		kind = arrmap.LinkMovie
	default:
		arrID, err = a.companionSeries(ctx, client.Sonarr(), sourceID, added.Title)
		kind = arrmap.LinkSeries
	}
	if err != nil {
		return fmt.Errorf("%s: %w", instance.Name, err)
	}
	return a.Links.Link(ctx, instance.ID, library.ArrLink{
		SourceKind: kind, SourceID: strconv.FormatInt(arrID, 10), LocalKind: "work", LocalID: added.WorkID,
	})
}

// companionTarget is the *Arr app an added item belongs to and its id there.
func companionTarget(added apiserver.AddedWork) (settings.ArrApp, int64, bool) {
	id, err := strconv.ParseInt(added.ExternalID, 10, 64)
	if err != nil || id <= 0 {
		return "", 0, false
	}
	switch {
	case added.Kind == "movie" && added.Provider == "tmdb":
		return settings.ArrAppRadarr, id, true
	case added.Kind == "series" && added.Provider == "tvdb":
		return settings.ArrAppSonarr, id, true
	}
	return "", 0, false
}

// companionInstance is the enabled connection of the app for the library, else the only enabled one of the app.
func (a ArrSync) companionInstance(ctx context.Context, app settings.ArrApp, categoryID int64) (settings.ArrInstance, bool, error) {
	instances, err := a.Cache.ArrInstances(ctx)
	if err != nil {
		return settings.ArrInstance{}, false, err
	}
	var ofApp []settings.ArrInstance
	for _, instance := range instances {
		if instance.Enabled && instance.App == app {
			ofApp = append(ofApp, instance)
		}
	}
	for _, instance := range ofApp {
		if instance.CategoryID != nil && *instance.CategoryID == categoryID {
			return instance, true, nil
		}
	}
	if len(ofApp) == 1 {
		return ofApp[0], true, nil
	}
	return settings.ArrInstance{}, false, nil
}

// companionMovie monitors a movie at Radarr and has it search, adding it first when Radarr lacks it; it answers Radarr's id.
func (a ArrSync) companionMovie(ctx context.Context, radarr arrimport.RadarrClient, tmdbID int64, title string) (int64, error) {
	movies, err := radarr.Movies(ctx)
	if err != nil {
		return 0, err
	}
	for _, movie := range movies {
		if movie.TMDBID != tmdbID {
			continue
		}
		if !movie.Monitored {
			if err := radarr.SetMonitored(ctx, "/api/v3/movie", movie.ID, true); err != nil {
				return 0, err
			}
		}
		return movie.ID, radarr.SearchMovie(ctx, movie.ID)
	}
	profile, root, err := a.radarrDefaults(ctx, radarr, movies)
	if err != nil {
		return 0, err
	}
	return radarr.AddMovie(ctx, arrimport.RadarrAdd{Title: title, TMDBID: tmdbID, QualityProfileID: profile, RootFolderPath: root})
}

// radarrDefaults are the quality profile and root folder most of Radarr's movies use, else its first of each.
func (a ArrSync) radarrDefaults(ctx context.Context, radarr arrimport.RadarrClient, movies []arrimport.RadarrMovie) (int64, string, error) {
	profiles, roots := map[int64]int{}, map[string]int{}
	for _, movie := range movies {
		profiles[movie.QualityProfileID]++
		roots[movie.RootFolderPath]++
	}
	profile, root := mostUsed(profiles), mostUsed(roots)
	if profile == 0 {
		known, err := radarr.QualityProfiles(ctx)
		if err != nil || len(known) == 0 {
			return 0, "", errors.Join(err, errors.New("the Radarr has no quality profile to add with"))
		}
		profile = known[0].ID
	}
	if root == "" {
		folders, err := radarr.RootFolders(ctx)
		if err != nil || len(folders) == 0 {
			return 0, "", errors.Join(err, errors.New("the Radarr has no root folder to add into"))
		}
		root = folders[0].Path
	}
	return profile, root, nil
}

// companionSeries is companionMovie for Sonarr.
func (a ArrSync) companionSeries(ctx context.Context, sonarr arrimport.SonarrClient, tvdbID int64, title string) (int64, error) {
	series, err := sonarr.Series(ctx)
	if err != nil {
		return 0, err
	}
	for _, one := range series {
		if one.TVDBID != tvdbID {
			continue
		}
		if !one.Monitored {
			if err := sonarr.SetMonitored(ctx, "/api/v3/series", one.ID, true); err != nil {
				return 0, err
			}
		}
		return one.ID, sonarr.SearchSeries(ctx, one.ID)
	}
	profiles, languages, roots := map[int64]int{}, map[int64]int{}, map[string]int{}
	for _, one := range series {
		profiles[one.QualityProfileID]++
		languages[one.LanguageProfileID]++
		roots[one.RootFolderPath]++
	}
	profile, root := mostUsed(profiles), mostUsed(roots)
	if profile == 0 {
		known, err := sonarr.QualityProfiles(ctx)
		if err != nil || len(known) == 0 {
			return 0, errors.Join(err, errors.New("the Sonarr has no quality profile to add with"))
		}
		profile = known[0].ID
	}
	if root == "" {
		folders, err := sonarr.RootFolders(ctx)
		if err != nil || len(folders) == 0 {
			return 0, errors.Join(err, errors.New("the Sonarr has no root folder to add into"))
		}
		root = folders[0].Path
	}
	return sonarr.AddSeries(ctx, arrimport.SonarrAdd{
		Title: title, TVDBID: tvdbID, QualityProfileID: profile, LanguageProfileID: mostUsed(languages), RootFolderPath: root,
	})
}

// mostUsed is the key counted most; the zero value of the key when nothing was counted, ties to the smaller key.
func mostUsed[K int64 | string](counts map[K]int) K {
	var best K
	most := 0
	for key, count := range counts {
		if key == *new(K) {
			continue
		}
		if count > most || (count == most && key < best) {
			best, most = key, count
		}
	}
	return best
}

// mediaRef is an added album, artist or scene by the id its provider gives, and the *Arr it goes to.
type mediaRef struct {
	app      settings.ArrApp
	ref      string
	linkKind string
}

// mediaTarget is the *Arr an added album or artist (Lidarr, by MusicBrainz id) or scene (Whisparr, by title) goes to.
func mediaTarget(added apiserver.AddedWork) (mediaRef, bool) {
	switch {
	case added.Kind == "album" && added.Provider == "musicbrainz:release-group":
		return mediaRef{settings.ArrAppLidarr, added.ExternalID, arrmap.LinkAlbum}, true
	case added.Kind == "artist" && added.Provider == "musicbrainz:artist":
		return mediaRef{settings.ArrAppLidarr, added.ExternalID, arrmap.LinkArtist}, true
	case (added.Kind == "adult" || added.Kind == "scene") && (added.Provider == "stashdb" || added.Provider == "theporndb"):
		return mediaRef{settings.ArrAppWhisparr, added.ExternalID, arrmap.LinkScene}, true
	}
	return mediaRef{}, false
}

// companionMedia hands an added album, artist or scene to Lidarr or Whisparr, which monitor and search for it.
func (a ArrSync) companionMedia(ctx context.Context, added apiserver.AddedWork, target mediaRef) error {
	instance, found, err := a.companionInstance(ctx, target.app, added.CategoryID)
	if err != nil || !found {
		return err
	}
	apiKey, err := a.Cache.DecryptedAPIKey(ctx, instance.ID)
	if err != nil {
		return err
	}
	client := a.Lidarr(instance.BaseURL, apiKey.Reveal())
	var arrID int64
	if target.app == settings.ArrAppLidarr {
		arrID, err = a.companionMusic(ctx, client, target)
	} else {
		arrID, err = a.companionScene(ctx, client.Whisparr(), target.ref, added.Title)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", instance.Name, err)
	}
	local := library.ArrLink{SourceKind: target.linkKind, SourceID: strconv.FormatInt(arrID, 10), LocalKind: "work", LocalID: added.WorkID}
	return a.Links.Link(ctx, instance.ID, local)
}

// companionMusic monitors an artist or album at Lidarr and has it search, adding it first when Lidarr lacks it.
func (a ArrSync) companionMusic(ctx context.Context, lidarr arrimport.LidarrClient, target mediaRef) (int64, error) {
	artist := target.linkKind == arrmap.LinkArtist
	var resource map[string]any
	var id int64
	var found bool
	var err error
	if artist {
		resource, id, found, err = lidarr.LookupArtist(ctx, target.ref)
	} else {
		resource, id, found, err = lidarr.LookupAlbum(ctx, target.ref)
	}
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, errors.New("the Lidarr does not know it")
	}
	path := "/api/v1/album"
	if artist {
		path = "/api/v1/artist"
	}
	if id != 0 {
		if err := lidarr.SetMonitored(ctx, path, id, true); err != nil {
			return 0, err
		}
		if artist {
			return id, lidarr.SearchArtist(ctx, id)
		}
		return id, lidarr.SearchAlbum(ctx, id)
	}
	defaults, err := a.lidarrDefaults(ctx, lidarr)
	if err != nil {
		return 0, err
	}
	if artist {
		return lidarr.AddArtist(ctx, resource, defaults)
	}
	return lidarr.AddAlbum(ctx, resource, defaults)
}

// lidarrDefaults are the profiles and root folder most of Lidarr's artists use, else its first of each.
func (a ArrSync) lidarrDefaults(ctx context.Context, lidarr arrimport.LidarrClient) (arrimport.AddDefaults, error) {
	artists, err := lidarr.Artists(ctx)
	if err != nil {
		return arrimport.AddDefaults{}, err
	}
	qualities, metadatas, roots := map[int64]int{}, map[int64]int{}, map[string]int{}
	for _, artist := range artists {
		qualities[artist.QualityProfileID]++
		metadatas[artist.MetadataProfileID]++
		roots[artist.RootFolderPath]++
	}
	defaults := arrimport.AddDefaults{QualityProfileID: mostUsed(qualities), MetadataProfileID: mostUsed(metadatas), RootFolderPath: mostUsed(roots)}
	if defaults.QualityProfileID == 0 {
		known, err := lidarr.QualityProfiles(ctx)
		if err != nil || len(known) == 0 {
			return defaults, errors.Join(err, errors.New("the Lidarr has no quality profile to add with"))
		}
		defaults.QualityProfileID = known[0].ID
	}
	if defaults.MetadataProfileID == 0 {
		known, err := lidarr.MetadataProfiles(ctx)
		if err != nil || len(known) == 0 {
			return defaults, errors.Join(err, errors.New("the Lidarr has no metadata profile to add with"))
		}
		defaults.MetadataProfileID = known[0].ID
	}
	if defaults.RootFolderPath == "" {
		folders, err := lidarr.RootFolders(ctx)
		if err != nil || len(folders) == 0 {
			return defaults, errors.Join(err, errors.New("the Lidarr has no root folder to add into"))
		}
		defaults.RootFolderPath = folders[0].Path
	}
	return defaults, nil
}

// companionScene monitors a scene at Whisparr and has it search, adding it first when Whisparr lacks it. Whisparr
// finds a scene by its title only, so the one whose id is the provider's is picked from what the title finds.
func (a ArrSync) companionScene(ctx context.Context, whisparr arrimport.WhisparrClient, ref, title string) (int64, error) {
	found, err := whisparr.LookupScenes(ctx, title)
	if err != nil {
		return 0, err
	}
	for _, scene := range found {
		if !sceneHasID(scene, ref) {
			continue
		}
		if id, _ := scene["id"].(float64); id > 0 {
			if err := whisparr.SetMonitored(ctx, arrimport.WhisparrScenePath, int64(id), true); err != nil {
				return 0, err
			}
			return int64(id), whisparr.SearchScene(ctx, int64(id))
		}
		profiles, err := whisparr.QualityProfiles(ctx)
		if err != nil || len(profiles) == 0 {
			return 0, errors.Join(err, errors.New("the Whisparr has no quality profile to add with"))
		}
		folders, err := whisparr.RootFolders(ctx)
		if err != nil || len(folders) == 0 {
			return 0, errors.Join(err, errors.New("the Whisparr has no root folder to add into"))
		}
		return whisparr.AddScene(ctx, scene, arrimport.AddDefaults{QualityProfileID: profiles[0].ID, RootFolderPath: folders[0].Path})
	}
	return 0, errors.New("the Whisparr found no scene with that id under that title")
}

// sceneHasID says a Whisparr lookup result is the scene with the provider's id, whichever of its id fields carries it.
func sceneHasID(scene map[string]any, ref string) bool {
	for _, field := range []string{"foreignId", "stashId", "tpdbId"} {
		if value, _ := scene[field].(string); value != "" && (value == ref || strings.HasSuffix(value, ":"+ref)) {
			return true
		}
	}
	return false
}
