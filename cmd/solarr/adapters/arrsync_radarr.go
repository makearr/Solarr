package adapters

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/tmdb"
)

// movieProviderTMDB is the id a Radarr movie is matched by.
const movieProviderTMDB = tmdb.KeyMovie

// radarrSource walks Radarr's movies, four at once: each is added by its TMDB id. A movie Solarr already holds is
// matched and left as it is; one it creates takes Radarr's monitored flag, minimum availability and profile. Movies are
// counted in the Artists columns, the run's first-kind tally.
func (a ArrSync) radarrSource(importID int64, client arrimport.RadarrClient, options apiserver.ArrSyncOptions) arrSource[arrimport.RadarrMovie] {
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
			movies, err := client.Movies(ctx)
			if err == nil && a.WarmFilms != nil {
				ids := make([]string, 0, len(movies))
				for _, movie := range movies {
					if movie.TMDBID != 0 {
						ids = append(ids, strconv.FormatInt(movie.TMDBID, 10))
					}
				}
				a.warm(ctx, "films", a.WarmFilms, ids)
			}
			return movies, err
		},
		describe: func(radarr arrimport.RadarrMovie) arrItem {
			return arrItem{Name: radarr.Title, RootFolder: radarr.RootFolderPath, Monitored: radarr.Monitored}
		},
		workers: 4,
		monitor: func(ctx context.Context, _ int64, radarr arrimport.RadarrMovie) (library.ArrImportCounts, error) {
			return a.monitorMovie(ctx, radarr)
		},
		sync: func(ctx context.Context, categoryID int64, radarr arrimport.RadarrMovie) (library.ArrImportCounts, error) {
			created, err := a.syncMovie(ctx, importID, categoryID, radarr, profileNames, options.ProfileMap)
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

func (a ArrSync) syncMovie(
	ctx context.Context, importID, categoryID int64, radarr arrimport.RadarrMovie, profileNames map[int64]string, profileMap map[int64]*int64,
) (bool, error) {
	provider, externalID := tmdb.KeyMovie, strconv.FormatInt(radarr.TMDBID, 10)
	switch {
	case radarr.TMDBID != 0:
	case radarr.IMDbID != "":
		provider, externalID = "imdb", radarr.IMDbID
	default:
		return false, errors.New("no TMDB or IMDb id")
	}
	category, _ := a.Cache.Snapshot().Category(categoryID)
	profile := a.movieProfile(category, radarr, profileNames, profileMap)
	// Radarr's own record stands in when no metadata source knows the film (shorts, TV specials: 48 of 3,964 live).
	fallback := &media.Fallback{Title: radarr.Title, Year: radarr.Year, ExternalIDs: map[string]string{}}
	if radarr.IMDbID != "" {
		fallback.ExternalIDs["imdb"] = radarr.IMDbID
	}
	request := media.AddRequest{
		CategoryID: categoryID, Provider: provider, ExternalID: externalID, Monitored: radarr.Monitored, ProfileID: &profile,
		Fallback: fallback,
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
	a.link(ctx, linkMovie, radarr.ID, "work", workID)
	return result.Created, nil
}

// movieProfile is the Solarr profile the import's mapping names for the movie's Radarr profile, nil the library's;
// unmapped, the movie profile of the same name (shipped ones lead with "Movie "), else the library's own.
func (a ArrSync) movieProfile(category settings.Category, radarr arrimport.RadarrMovie, profileNames map[int64]string, profileMap map[int64]*int64) int64 {
	id, mapped := profileMap[radarr.QualityProfileID]
	if !mapped {
		id = sameNameProfile(a.Cache.Snapshot().QualityProfiles(), category.MediaType, profileNames[radarr.QualityProfileID])
	}
	if id != nil {
		return *id
	}
	return a.Categories.ProfileFor(category, a.DefaultProfileID)
}
