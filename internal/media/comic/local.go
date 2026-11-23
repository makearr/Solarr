package comic

import (
	"context"
	"strings"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/mediaserver"
)

// localIDs names the provider each id of a media server's series belongs to.
var localIDs = map[string]string{
	"comicvine": ProviderComicVine, "anilist": ProviderAniListManga, "mal": ProviderMAL,
	"mangadex": ProviderMangaDex, "mangaupdates": ProviderMangaUpdates,
}

// askOther asks a source with no client of its own: a media server of the person's, or a definition file.
func (s Sources) askOther(ctx context.Context, categoryID int64, series *Series, key string, hint Hint) {
	if strings.HasPrefix(key, settings.LocalSourcePrefix) {
		s.askLocal(ctx, categoryID, series, key, hint)
		return
	}
	s.askRecords(ctx, series, key, hint)
}

// askLocal fills what the series lacks from the media server of the source's kind the library is mapped to, found by the
// title; a provider id the server's links carry is kept only where the series has none.
func (s Sources) askLocal(ctx context.Context, categoryID int64, series *Series, source string, hint Hint) {
	title := firstNonEmpty(series.Title, hint.Title)
	if s.Local == nil || categoryID == 0 || title == "" {
		return
	}
	item, found, err := s.Local.Ask(ctx, categoryID, strings.TrimPrefix(source, settings.LocalSourcePrefix), "comic", mediaserver.Question{Title: title})
	if err != nil {
		s.warn(source, title, err)
		return
	}
	if !found || (hint.Year != 0 && item.Year != 0 && item.Year != hint.Year) {
		return
	}
	for name, id := range item.IDs {
		if provider, known := localIDs[name]; known && series.IDs[provider] == "" {
			series.IDs[provider] = id
		}
	}
	publisher := ""
	if len(item.Organisations) > 0 {
		publisher = item.Organisations[0]
	}
	series.fill(item.Title, item.Overview, publisher, "", item.Year, 0)
	series.addAliases(item.OriginalTitle)
	series.addGenres(item.Genres...)
	series.addGenres(item.Tags...)
	if series.OriginalLanguage == "" {
		series.OriginalLanguage = item.Language
	}
	for _, person := range item.People {
		series.Credits = append(series.Credits, Credit{Name: person.Name, Role: person.Kind, Order: len(series.Credits)})
	}
	series.Sources = append(series.Sources, source)
}
