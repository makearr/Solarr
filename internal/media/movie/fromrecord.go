package movie

import (
	"context"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/specprovider"
)

// ConsumesRecords implements media.RecordConsuming.
func (Module) ConsumesRecords() bool { return true }

var _ media.RecordConsuming = Module{}

// RecordProvider is a definition-file provider as the movie service asks it.
type RecordProvider interface {
	ByID(ctx context.Context, namespace, id string) (specprovider.Record, error)
}

func (s Service) recordProvider(key string) RecordProvider {
	if s.Records == nil {
		return nil
	}
	return s.Records(key)
}

// FromRecord turns a definition's record into a movie. A movie's ids are TMDB and IMDb only, so a definition's own id is not kept.
func FromRecord(key string, record specprovider.Record) Movie {
	movie := Movie{
		Title: record.Title, OriginalTitle: record.OriginalTitle, Overview: record.Overview, Year: record.Year, RuntimeMin: record.RuntimeSeconds / 60,
		OriginalLanguage: record.Language, Certification: record.AgeRating, Genres: append([]string{}, record.Genres...), Keywords: append([]string{}, record.Tags...),
		Sources: []string{key},
	}
	movie.Aliases = aliasesOf(Movie{Title: record.Title, Aliases: append([]string{record.OriginalTitle}, record.AltTitles...)}, "")
	if id, err := strconv.ParseInt(record.IDs["tmdb"], 10, 64); err == nil {
		movie.IDs.TMDB = id
	}
	movie.IDs.IMDb = record.IDs["imdb"]
	if len(record.Date) >= 10 {
		movie.Premiere = record.Date[:10]
	}
	for _, organisation := range record.Organisations {
		if movie.Studio == "" {
			movie.Studio = organisation.Name
		}
		movie.Studios = append(movie.Studios, Studio{Name: organisation.Name, Country: record.Country})
	}
	for _, image := range record.Images {
		movie.Images = append(movie.Images, Image{Kind: movieImageKind(image.Kind), URL: image.URL, Source: key})
	}
	for _, person := range record.People {
		movie.Credits = append(movie.Credits, Credit{Name: person.Name, Role: movieRole(person.Role), Order: len(movie.Credits)})
	}
	if record.Rating.Votes > 0 || record.Rating.Value > 0 {
		movie.Ratings = []Rating{{Provider: key, Score: record.Rating.Value, Votes: int64(record.Rating.Votes)}}
	}
	return movie
}

func movieImageKind(kind string) string {
	switch kind {
	case "backdrop", "fanart":
		return "fanart"
	case "logo", "banner":
		return kind
	default:
		return "poster"
	}
}

func movieRole(role string) string {
	switch strings.ToLower(role) {
	case "director", "writer", "producer", "composer", "cinematographer":
		return strings.ToLower(role)
	default:
		return RoleActor
	}
}

// askRecord reads a movie from a definition by an id Solarr holds (IMDb first, then TMDB); a movie with neither is not asked.
func (s Service) askRecord(ctx context.Context, key string, ids IDs) (Movie, bool) {
	provider := s.recordProvider(key)
	if provider == nil {
		return Movie{}, false
	}
	for _, own := range []struct{ namespace, id string }{{"imdb", ids.IMDb}, {"tmdb", idText(ids.TMDB)}} {
		if own.id == "" {
			continue
		}
		record, err := provider.ByID(ctx, own.namespace, own.id)
		if err != nil {
			s.warn(key, ids.TMDB, err)
			continue
		}
		if record.Title != "" {
			return FromRecord(key, record), true
		}
	}
	return Movie{}, false
}

func idText(id int64) string {
	if id <= 0 {
		return ""
	}
	return strconv.FormatInt(id, 10)
}
