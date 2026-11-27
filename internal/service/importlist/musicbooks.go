package importlist

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/listenbrainz"
	"github.com/makearr/Solarr/internal/metadata/openlibrary"
)

const (
	listenBrainzUserField  = "user"
	listenBrainzRangeField = "range"
	openLibraryListField   = "list"
	// listenBrainzCount bounds one read of a user's top albums; openLibraryCount bounds one list.
	listenBrainzCount = 100
	openLibraryCount  = 500
	providerMBRelease = "musicbrainz:release-group"
	providerOpenLib   = "openlibrary"
)

// ListenBrainzTops is the ListenBrainz client as the top albums of a user need it.
type ListenBrainzTops interface {
	UserReleaseGroups(ctx context.Context, user, period string, offset, count int) ([]listenbrainz.ChartReleaseGroup, error)
}

// OpenLibraryLists is the Open Library client as a reader's list needs it.
type OpenLibraryLists interface {
	ListSeeds(ctx context.Context, listPath string, limit int) ([]openlibrary.Seed, error)
}

var listenBrainzFields = []Field{
	{Key: listenBrainzUserField, Label: "ListenBrainz user", Kind: FieldText, Required: true, Help: "The user name; their listening statistics must be public."},
	{Key: listenBrainzRangeField, Label: "Range", Kind: FieldChoice, Default: "all_time", Choices: []media.Choice{
		{Key: "week", Label: "Last week"}, {Key: "month", Label: "Last month"}, {Key: "quarter", Label: "Last quarter"},
		{Key: "year", Label: "Last year"}, {Key: "all_time", Label: "All time"},
	}},
}

// ListenBrainzTop is the albums a ListenBrainz user listened to most.
type ListenBrainzTop struct{ Client ListenBrainzTops }

// Entries implements Source.
func (l ListenBrainzTop) Entries(ctx context.Context, options Options) ([]media.ListEntry, error) {
	user := strings.TrimSpace(options[listenBrainzUserField])
	period := options[listenBrainzRangeField]
	if period == "" {
		period = "all_time"
	}
	rows, err := l.Client.UserReleaseGroups(ctx, user, period, 0, listenBrainzCount)
	switch {
	case errors.Is(err, listenbrainz.ErrNotFound):
		return nil, fmt.Errorf("%w: %w", ErrListNotFound, err)
	case err != nil:
		return nil, fmt.Errorf("%w: %w", ErrSourceUnavailable, err)
	}
	entries := make([]media.ListEntry, 0, len(rows))
	for _, row := range rows {
		if row.MBID == "" {
			continue
		}
		entries = append(entries, media.ListEntry{
			Title: row.Title, Form: "release-group", IDs: []media.ProviderID{{Provider: providerMBRelease, ExternalID: row.MBID}},
		})
	}
	return entries, nil
}

// WithListenBrainz adds a ListenBrainz user's top albums over the client given.
func (c Catalogue) WithListenBrainz(client ListenBrainzTops) Catalogue {
	kinds := append([]Kind(nil), c.kinds...)
	kinds = append(kinds, Kind{
		Type: media.ListListenBrainzTop, Label: "ListenBrainz top albums", Fields: listenBrainzFields, Source: ListenBrainzTop{Client: client},
		Help: "The albums a ListenBrainz user listened to most, over the range chosen.",
	})
	return Catalogue{kinds: kinds}
}

var openLibraryFields = []Field{{Key: openLibraryListField, Label: "List", Kind: FieldText, Required: true,
	Help: "A public Open Library list: its link, https://openlibrary.org/people/<user>/lists/OL1L"}}

// openLibraryLink is a reader's list page on openlibrary.org.
var openLibraryLink = regexp.MustCompile(`^https?://(www\.)?openlibrary\.org(/people/[A-Za-z0-9_-]+/lists/OL\d+L)/?([?#].*)?$`)

// OpenLibraryListPath reads a list's path from its link, never fetching the link.
func OpenLibraryListPath(value string) (string, bool) {
	match := openLibraryLink.FindStringSubmatch(strings.TrimSpace(value))
	if match == nil {
		return "", false
	}
	return match[2], true
}

// OpenLibraryList is a public list of a reader at Open Library; its works are read, authors and subjects are left out.
type OpenLibraryList struct{ Client OpenLibraryLists }

// Entries implements Source.
func (l OpenLibraryList) Entries(ctx context.Context, options Options) ([]media.ListEntry, error) {
	path, ok := OpenLibraryListPath(options[openLibraryListField])
	if !ok {
		return nil, fmt.Errorf("%w: %q is no Open Library list", ErrListNotFound, options[openLibraryListField])
	}
	seeds, err := l.Client.ListSeeds(ctx, path, openLibraryCount)
	switch {
	case errors.Is(err, openlibrary.ErrNotFound):
		return nil, fmt.Errorf("%w: %w", ErrListNotFound, err)
	case err != nil:
		return nil, fmt.Errorf("%w: %w", ErrSourceUnavailable, err)
	}
	var entries []media.ListEntry
	for _, seed := range seeds {
		if id := seed.WorkID(); id != "" {
			entries = append(entries, media.ListEntry{Title: seed.Title, Form: "work", IDs: []media.ProviderID{{Provider: providerOpenLib, ExternalID: id}}})
		}
	}
	return entries, nil
}

// WithOpenLibrary adds a reader's Open Library list over the client given.
func (c Catalogue) WithOpenLibrary(client OpenLibraryLists) Catalogue {
	kinds := append([]Kind(nil), c.kinds...)
	kinds = append(kinds, Kind{
		Type: media.ListOpenLibrary, Label: "Open Library list", Fields: openLibraryFields, Source: OpenLibraryList{Client: client},
		Help: "The works on a public Open Library list.",
	})
	return Catalogue{kinds: kinds}
}
