package movie

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/importing/arrmap"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/tmdb"
)

// RadarrImport adds Radarr's movies, four at once, each by its TMDB id. A movie Solarr already holds is matched and left
// as it is; one it creates takes Radarr's monitored flag, minimum availability and profile. Movies count in the run's
// first-kind tally.
type RadarrImport struct {
	Adding media.Adding
	Cache  *settings.Cache
	// ProfileFor is a library's profile for what its import adds.
	ProfileFor func(category settings.Category) int64
	// Warm reads in bulk what Wikidata says of the films about to be added; nil warms nothing.
	Warm     func(ctx context.Context, ids []string)
	Monitors arrmap.Monitors
}

// Import walks Radarr's movies.
func (r RadarrImport) Import(ctx context.Context, run arrmap.Run) (library.ArrImportCounts, error) {
	return arrmap.Walk(ctx, run, r.source(run))
}

func (r RadarrImport) source(run arrmap.Run) arrmap.Source[arrimport.RadarrMovie] {
	client := run.Client.Radarr()
	var profileNames map[int64]string
	return arrmap.Source[arrimport.RadarrMovie]{
		Connect: func(ctx context.Context) error {
			if r.Adding == nil {
				return errors.New("no movie service")
			}
			_, err := client.SystemStatus(ctx)
			return err
		},
		List: func(ctx context.Context) ([]arrimport.RadarrMovie, error) {
			profileNames = radarrProfileNames(ctx, client)
			return client.Movies(ctx)
		},
		Prepare: func(ctx context.Context, chosen []arrimport.RadarrMovie) {
			if r.Warm == nil {
				return
			}
			ids := make([]string, 0, len(chosen))
			for _, movie := range chosen {
				if movie.TMDBID != 0 {
					ids = append(ids, strconv.FormatInt(movie.TMDBID, 10))
				}
			}
			r.Warm(ctx, ids)
		},
		Describe: func(radarr arrimport.RadarrMovie) arrmap.Item {
			return arrmap.Item{Name: radarr.Title, RootFolder: radarr.RootFolderPath, Monitored: radarr.Monitored}
		},
		Workers: 4,
		Monitor: func(ctx context.Context, _ int64, radarr arrimport.RadarrMovie) (library.ArrImportCounts, error) {
			workID, held, err := r.Monitors.Work(ctx, MediaType, tmdb.KeyMovie, strconv.FormatInt(radarr.TMDBID, 10), radarr.Monitored)
			if held && err == nil {
				run.Link(ctx, arrmap.LinkMovie, radarr.ID, "work", workID)
			}
			return arrmap.Found(held, err)
		},
		Sync: func(ctx context.Context, categoryID int64, radarr arrimport.RadarrMovie) (library.ArrImportCounts, error) {
			return arrmap.Tally(r.syncMovie(ctx, run, categoryID, radarr, profileNames))
		},
	}
}

func radarrProfileNames(ctx context.Context, client arrimport.RadarrClient) map[int64]string {
	return arrmap.ProfileNames(ctx, client.QualityProfiles, func(profile arrimport.SonarrQualityProfile) (int64, string) {
		return profile.ID, profile.Name
	})
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

func (r RadarrImport) syncMovie(ctx context.Context, run arrmap.Run, categoryID int64, radarr arrimport.RadarrMovie, profileNames map[int64]string) (bool, error) {
	provider, externalID := tmdb.KeyMovie, strconv.FormatInt(radarr.TMDBID, 10)
	switch {
	case radarr.TMDBID != 0:
	case radarr.IMDbID != "":
		provider, externalID = "imdb", radarr.IMDbID
	default:
		return false, errors.New("no TMDB or IMDb id")
	}
	category, _ := r.Cache.Snapshot().Category(categoryID)
	profile := r.movieProfile(category, radarr, profileNames, run.Options.ProfileMap)
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
	result, err := r.Adding.AddWork(ctx, request)
	if err != nil {
		return false, err
	}
	workID := result.WorkID
	if err := run.Record(ctx, library.NewArrImportItem{
		Kind: library.ArrImportItemMovie, SourceID: strconv.FormatInt(radarr.ID, 10), Title: radarr.Title,
		WorkID: &workID, WorkCreated: result.Created,
	}); err != nil {
		return false, err
	}
	run.Link(ctx, arrmap.LinkMovie, radarr.ID, "work", workID)
	return result.Created, nil
}

// movieProfile is the Solarr profile the import's mapping names for the movie's Radarr profile, nil the library's;
// unmapped, the movie profile of the same name (shipped ones lead with "Movie "), else the library's own.
func (r RadarrImport) movieProfile(category settings.Category, radarr arrimport.RadarrMovie, profileNames map[int64]string, profileMap map[int64]*int64) int64 {
	id, mapped := profileMap[radarr.QualityProfileID]
	if !mapped {
		id = arrmap.SameNameProfile(r.Cache.Snapshot().QualityProfiles(), category.MediaType, profileNames[radarr.QualityProfileID])
	}
	if id != nil {
		return *id
	}
	return r.ProfileFor(category)
}

// Preview reads Radarr's movies.
func (r RadarrImport) Preview(ctx context.Context, client arrimport.LidarrClient) (arrmap.PreviewSource, error) {
	radarr := client.Radarr()
	source := arrmap.PreviewSource{ItemKind: "movies", MediaType: MediaType}
	movies, err := radarr.Movies(ctx)
	if err != nil {
		return arrmap.PreviewSource{}, err
	}
	for _, movie := range movies {
		source.Items = append(source.Items, arrmap.PreviewItem{RootFolder: movie.RootFolderPath, Monitored: movie.Monitored,
			ProfileID: movie.QualityProfileID, Provider: tmdb.KeyMovie, ExternalID: arrmap.IDText(movie.TMDBID)})
	}
	return arrmap.ReadPreview(ctx, source, radarr, radarrProfileNames(ctx, radarr))
}
