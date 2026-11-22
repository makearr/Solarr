package movie

import (
	"context"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/mediaserver"
)

// FromServer is what a media server of the person's own holds of a film; the merge ranks it after every other source.
func FromServer(item mediaserver.Item, source string) Movie {
	movie := Movie{
		Title: item.Title, OriginalTitle: item.OriginalTitle, Overview: item.Overview, Year: item.Year, Premiere: item.Date,
		OriginalLanguage: item.Language, Certification: item.AgeRating, Genres: item.Genres, Keywords: item.Tags, Sources: []string{source},
	}
	if item.RuntimeMS > 0 {
		movie.RuntimeMin = int(item.RuntimeMS / 60000)
	}
	if len(item.Organisations) > 0 {
		movie.Studio = item.Organisations[0]
		for _, name := range item.Organisations {
			movie.Studios = append(movie.Studios, Studio{Name: name})
		}
	}
	if item.Rating > 0 {
		movie.Ratings = []Rating{{Provider: source, Score: item.Rating}}
	}
	movie.IDs.TMDB, _ = strconv.ParseInt(item.IDs["tmdb"], 10, 64)
	movie.IDs.IMDb = item.IDs["imdb"]
	order := 0
	for _, person := range item.People {
		switch person.Kind {
		case "actor":
			movie.Credits = append(movie.Credits, Credit{Name: person.Name, Role: RoleActor, Character: person.Role, Order: order})
			order++
		case "director":
			movie.Credits = append(movie.Credits, Credit{Name: person.Name, Role: RoleDirector})
		case "writer":
			movie.Credits = append(movie.Credits, Credit{Name: person.Name, Role: RoleWriter})
		}
	}
	return movie
}

// askLocal asks the media server of the source's kind the library is mapped to; a film it does not hold is not an answer.
func (s Service) askLocal(ctx context.Context, categoryID int64, source string, ids IDs) (Movie, bool) {
	if s.Local == nil || categoryID == 0 {
		return Movie{}, false
	}
	known := map[string]string{"imdb": ids.IMDb}
	if ids.TMDB > 0 {
		known["tmdb"] = strconv.FormatInt(ids.TMDB, 10)
	}
	item, found, err := s.Local.Ask(ctx, categoryID, strings.TrimPrefix(source, settings.LocalSourcePrefix), "movie", mediaserver.Question{IDs: known})
	if err != nil {
		s.warn(source, ids.TMDB, err)
		return Movie{}, false
	}
	if !found {
		return Movie{}, false
	}
	movie := FromServer(item, source)
	// An id from a file or another provider is never replaced by the server's.
	if ids.TMDB > 0 {
		movie.IDs.TMDB = 0
	}
	if ids.IMDb != "" {
		movie.IDs.IMDb = ""
	}
	return movie, true
}

// IDsByPath names a library file by what the media servers hold of it, for a file nothing else has named; no server, no match
// and an item with no id all answer none.
func (s Service) IDsByPath(ctx context.Context, categoryID int64, filePath string) IDs {
	if s.Local == nil || categoryID == 0 {
		return IDs{}
	}
	item, found, err := s.Local.AskByPath(ctx, categoryID, filePath)
	if err != nil || !found || item.MediaType != "movie" {
		return IDs{}
	}
	ids := IDs{IMDb: item.IDs["imdb"]}
	ids.TMDB, _ = strconv.ParseInt(item.IDs["tmdb"], 10, 64)
	return ids
}
