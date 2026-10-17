package adapters

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/tmdb"
)

// radarrSource walks Radarr's movies, four at once: each is added by its TMDB id. A movie Solarr already holds is
// matched and left as it is; one it creates takes Radarr's monitored flag, minimum availability and profile. Movies are
// counted in the Artists columns, the run's first-kind tally.
func (a ArrSync) radarrSource(importID int64, client arrimport.RadarrClient) arrSource[arrimport.RadarrMovie] {
	var profileNames map[int64]string
	return arrSource[arrimport.RadarrMovie]{
		connect: func(ctx context.Context) error {
			if a.Movies == nil {
				return errors.New("no movie service")
			}
			_, err := client.SystemStatus(ctx)
			return err
		},
		list: func(ctx context.Context) ([]arrimport.RadarrMovie, error) {
			profileNames = radarrProfileNames(ctx, client)
			return client.Movies(ctx)
		},
		describe: func(radarr arrimport.RadarrMovie) arrItem {
			return arrItem{Name: radarr.Title, RootFolder: radarr.RootFolderPath, Monitored: radarr.Monitored}
		},
		workers: 4,
		sync: func(ctx context.Context, categoryID int64, radarr arrimport.RadarrMovie) (library.ArrImportCounts, error) {
			created, err := a.syncMovie(ctx, importID, categoryID, radarr, profileNames)
			switch {
			case err != nil:
				return library.ArrImportCounts{}, err
			case created:
				return library.ArrImportCounts{ArtistsCreated: 1}, nil
			}
			return library.ArrImportCounts{ArtistsMatched: 1}, nil
		},
	}
}

// radarrProfileNames reads the instance's quality profile names; a failure leaves the map empty, so every movie takes
// its library's profile.
func radarrProfileNames(ctx context.Context, client arrimport.RadarrClient) map[int64]string {
	profiles, err := client.QualityProfiles(ctx)
	if err != nil {
		return nil
	}
	names := make(map[int64]string, len(profiles))
	for _, profile := range profiles {
		names[profile.ID] = profile.Name
	}
	return names
}

// radarrAvailability reads Radarr's minimum availability as the movie type stores it; preDB, which Radarr no longer
// offers, is released, and an unknown word is left to the library's own.
func radarrAvailability(value string) string {
	switch strings.ToLower(value) {
	case "announced", "tba":
		return "announced"
	case "incinemas":
		return "in_cinemas"
	case "released", "predb":
		return "released"
	}
	return ""
}

func (a ArrSync) syncMovie(ctx context.Context, importID, categoryID int64, radarr arrimport.RadarrMovie, profileNames map[int64]string) (bool, error) {
	provider, externalID := tmdb.KeyMovie, strconv.FormatInt(radarr.TMDBID, 10)
	switch {
	case radarr.TMDBID != 0:
	case radarr.IMDbID != "":
		provider, externalID = "imdb", radarr.IMDbID
	default:
		return false, errors.New("no TMDB or IMDb id")
	}
	category, _ := a.Cache.Snapshot().Category(categoryID)
	profile := a.movieProfile(category, radarr, profileNames)
	request := media.AddRequest{
		CategoryID: categoryID, Provider: provider, ExternalID: externalID, Monitored: radarr.Monitored, ProfileID: &profile,
	}
	if availability := radarrAvailability(radarr.MinimumAvailability); availability != "" {
		request.Choices = map[string]string{"minimum_availability": availability}
	}
	result, err := a.Movies.AddWork(ctx, request)
	if err != nil {
		return false, err
	}
	workID := result.WorkID
	if _, err := a.Imports.AddItem(ctx, importID, library.NewArrImportItem{
		Kind: library.ArrImportItemMovie, SourceID: strconv.FormatInt(radarr.ID, 10), Title: radarr.Title,
		WorkID: &workID, WorkCreated: result.Created,
	}, a.Clock.Now().Unix()); err != nil {
		return false, err
	}
	return result.Created, nil
}

// movieProfile is the Solarr movie profile that has the movie's Radarr profile's name (shipped ones lead with "Movie "),
// else the library's own.
func (a ArrSync) movieProfile(category settings.Category, radarr arrimport.RadarrMovie, profileNames map[int64]string) int64 {
	if name := profileNames[radarr.QualityProfileID]; name != "" {
		for _, profile := range a.Cache.Snapshot().QualityProfiles() {
			if profile.MediaType == category.MediaType && strings.EqualFold(strings.TrimPrefix(profile.Name, "Movie "), name) {
				return profile.ID
			}
		}
	}
	return a.Categories.ProfileFor(category, a.DefaultProfileID)
}
