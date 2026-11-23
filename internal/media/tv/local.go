package tv

import (
	"context"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/mediaserver"
)

// FromServer is what a media server of the person's own holds of a series; the merge ranks it after every other source.
func FromServer(item mediaserver.Item, source string) Series {
	series := Series{
		Title: item.Title, Overview: item.Overview, Year: item.Year, FirstAired: item.Date, Genres: item.Genres,
		ContentRating: item.AgeRating, Rating: item.Rating, OriginalLanguage: item.Language, Sources: []string{source},
	}
	if item.RuntimeMS > 0 {
		series.RuntimeMin = int(item.RuntimeMS / 60000)
	}
	if len(item.Organisations) > 0 {
		series.Network = item.Organisations[0]
	}
	series.IDs.TVDB, _ = strconv.ParseInt(item.IDs["tvdb"], 10, 64)
	series.IDs.TMDB, _ = strconv.ParseInt(item.IDs["tmdb"], 10, 64)
	series.IDs.IMDb = item.IDs["imdb"]
	for _, person := range item.People {
		if person.Kind == "actor" {
			series.Cast = append(series.Cast, Person{Name: person.Name, Character: person.Role})
		}
	}
	series.Credits = serverCredits(item.People)
	return series
}

// fetchLocal asks the media server of the source's kind the library is mapped to; a series it does not hold is not an answer.
func (s Service) fetchLocal(ctx context.Context, categoryID int64, source string, ids IDs) (Series, bool) {
	if s.Local == nil || categoryID == 0 {
		return Series{}, false
	}
	known := map[string]string{"imdb": ids.IMDb}
	if ids.TVDB > 0 {
		known["tvdb"] = strconv.FormatInt(ids.TVDB, 10)
	}
	if ids.TMDB > 0 {
		known["tmdb"] = strconv.FormatInt(ids.TMDB, 10)
	}
	item, found, err := s.Local.Ask(ctx, categoryID, strings.TrimPrefix(source, settings.LocalSourcePrefix), "tv", mediaserver.Question{IDs: known})
	if err != nil {
		s.warn(source, ids.TVDB, err)
		return Series{}, false
	}
	if !found {
		return Series{}, false
	}
	series := FromServer(item, source)
	// An id from a file or another provider is never replaced by the server's.
	if ids.TVDB > 0 {
		series.IDs.TVDB = 0
	}
	if ids.TMDB > 0 {
		series.IDs.TMDB = 0
	}
	if ids.IMDb != "" {
		series.IDs.IMDb = ""
	}
	return series, true
}

// GuessByPath names a library folder by what the media servers hold of it, for a folder no nfo or name has named.
func (s Service) GuessByPath(ctx context.Context, categoryID int64, folderPath string) FolderGuess {
	if s.Local == nil || categoryID == 0 {
		return FolderGuess{}
	}
	item, found, err := s.Local.AskByPath(ctx, categoryID, folderPath)
	if err != nil || !found || item.MediaType != "tv" {
		return FolderGuess{}
	}
	guess := FolderGuess{IMDbID: item.IDs["imdb"]}
	guess.TVDBID, _ = strconv.ParseInt(item.IDs["tvdb"], 10, 64)
	guess.TMDBID, _ = strconv.ParseInt(item.IDs["tmdb"], 10, 64)
	return guess
}

// serverCredits turns a server's people into credits keyed by name (TMDBID stays zero); a person of another kind is left out.
func serverCredits(people []mediaserver.Credit) []Credit {
	var credits []Credit
	order := 0
	for _, person := range people {
		switch person.Kind {
		case "actor", "gueststar":
			credits = append(credits, Credit{Name: person.Name, Role: RoleActor, Character: person.Role, Order: order})
			order++
		case "director":
			credits = append(credits, Credit{Name: person.Name, Role: RoleDirector})
		case "writer":
			credits = append(credits, Credit{Name: person.Name, Role: RoleWriter})
		case "producer":
			credits = append(credits, Credit{Name: person.Name, Role: RoleProducer})
		case "composer":
			credits = append(credits, Credit{Name: person.Name, Role: RoleComposer})
		}
	}
	return credits
}
