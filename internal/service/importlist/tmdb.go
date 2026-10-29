package importlist

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/tmdb"
)

// The ways reading a list fails that the person can act on; anything else is a failed run.
var (
	ErrNeedsKey          = errors.New("importlist: this list needs the TMDB key, under Settings, Metadata")
	ErrListNotFound      = errors.New("importlist: the list does not exist or is private")
	ErrSourceUnavailable = errors.New("importlist: the list's source did not answer")
)

// Page bounds: a TMDB list of up to 500 entries, a chart's first 100.
const (
	maxListPages  = 25
	maxChartPages = 5
)

const providerTMDB = "tmdb"

// TMDBLists is the TMDB client as its lists need it.
type TMDBLists interface {
	List(ctx context.Context, listID string, page int) (tmdb.ListPage, error)
	Trending(ctx context.Context, media, window string, page int) (tmdb.ListPage, error)
	Popular(ctx context.Context, media string, page int) (tmdb.ListPage, error)
}

const (
	tmdbListField = "list"
	windowField   = "window"
)

var tmdbListFields = []Field{{Key: tmdbListField, Label: "List", Kind: FieldText, Required: true,
	Help: "The list's number, or its link: https://www.themoviedb.org/list/..."}}

var trendingFields = []Field{{Key: windowField, Label: "Trending over", Kind: FieldChoice, Default: tmdb.TrendingWeek,
	Choices: []media.Choice{{Key: tmdb.TrendingDay, Label: "Today"}, {Key: tmdb.TrendingWeek, Label: "This week"}}}}

// tmdbListLink is a list's page on themoviedb.org, whose path starts with the list's number.
var tmdbListLink = regexp.MustCompile(`^https?://(www\.)?themoviedb\.org/list/(\d+)(-[^/?#]*)?/?([?#].*)?$`)

// TMDBListID reads a list's number from the number itself or its link, never fetching the link.
func TMDBListID(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if id, err := strconv.ParseInt(value, 10, 64); err == nil && id > 0 {
		return value, true
	}
	if match := tmdbListLink.FindStringSubmatch(value); match != nil {
		return match[2], true
	}
	return "", false
}

// TMDBList is a public TMDB list.
type TMDBList struct{ Client TMDBLists }

// Entries implements Source.
func (l TMDBList) Entries(ctx context.Context, options Options) ([]media.ListEntry, error) {
	id, ok := TMDBListID(options[tmdbListField])
	if !ok {
		return nil, fmt.Errorf("%w: %q is no TMDB list", ErrListNotFound, options[tmdbListField])
	}
	return readPages(maxListPages, func(page int) (tmdb.ListPage, error) { return l.Client.List(ctx, id, page) })
}

// TMDBTrending is the shows trending on TMDB.
type TMDBTrending struct{ Client TMDBLists }

// Entries implements Source.
func (t TMDBTrending) Entries(ctx context.Context, options Options) ([]media.ListEntry, error) {
	window := option(options, trendingFields, windowField)
	return readPages(maxChartPages, func(page int) (tmdb.ListPage, error) {
		return t.Client.Trending(ctx, tmdb.MediaTV, window, page)
	})
}

// TMDBPopular is the most popular shows on TMDB.
type TMDBPopular struct{ Client TMDBLists }

// Entries implements Source.
func (p TMDBPopular) Entries(ctx context.Context, _ Options) ([]media.ListEntry, error) {
	return readPages(maxChartPages, func(page int) (tmdb.ListPage, error) { return p.Client.Popular(ctx, tmdb.MediaTV, page) })
}

// readPages reads pages until the last, or the bound.
func readPages(bound int, read func(page int) (tmdb.ListPage, error)) ([]media.ListEntry, error) {
	var entries []media.ListEntry
	for page := 1; page <= bound; page++ {
		found, err := read(page)
		switch {
		case errors.Is(err, tmdb.ErrNoKey), errors.Is(err, tmdb.ErrBadKey):
			return nil, fmt.Errorf("%w: %w", ErrNeedsKey, err)
		case errors.Is(err, tmdb.ErrNotFound):
			return nil, fmt.Errorf("%w: %w", ErrListNotFound, err)
		case err != nil:
			return nil, fmt.Errorf("%w: %w", ErrSourceUnavailable, err)
		}
		for _, item := range found.Items {
			entries = append(entries, tmdbEntry(item))
		}
		if page >= found.TotalPages {
			break
		}
	}
	return entries, nil
}

func tmdbEntry(item tmdb.ListItem) media.ListEntry {
	title, date := item.Name, item.FirstAirDate
	if item.MediaType == tmdb.MediaMovie {
		title, date = item.Title, item.ReleaseDate
	}
	entry := media.ListEntry{
		Title: title, Form: item.MediaType, IDs: []media.ProviderID{{Provider: providerTMDB, ExternalID: strconv.FormatInt(item.ID, 10)}},
	}
	if year, err := strconv.Atoi(strings.SplitN(date, "-", 2)[0]); err == nil {
		entry.Year = year
	}
	if item.PosterPath != "" {
		entry.PosterURL = tmdb.ImageURL(item.PosterPath)
	}
	return entry
}
