package importlist

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/hardcover"
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

// ErrNeedsHardcoverKey says the list cannot be read before Hardcover's token is stored.
var ErrNeedsHardcoverKey = errors.New("importlist: this list needs the Hardcover token, under Settings, Metadata")

// HardcoverLists is the Hardcover client as the signed-in account's lists need it.
type HardcoverLists interface {
	MyLists(ctx context.Context) ([]hardcover.List, error)
	ListBooks(ctx context.Context, listID int64, limit, offset int) ([]hardcover.ListBook, error)
}

const (
	hardcoverListField = "list"
	hardcoverPageSize  = 100
	maxHardcoverPages  = 10
	providerHardcover  = "hardcover"
)

var hardcoverFields = []Field{{Key: hardcoverListField, Label: "List", Kind: FieldText, Required: true,
	Help: "One of your own Hardcover lists: its name, its number or its link (https://hardcover.app/@you/lists/name). Read only."}}

// hardcoverListLink is the slug of a list in a hardcover.app link.
var hardcoverListLink = regexp.MustCompile(`^https?://(www\.)?hardcover\.app/@[^/\s]+/lists/([^/?#\s]+)/?([?#].*)?$`)

// HardcoverList is a list of the account the Hardcover token belongs to.
type HardcoverList struct{ Client HardcoverLists }

// Entries implements Source.
func (l HardcoverList) Entries(ctx context.Context, options Options) ([]media.ListEntry, error) {
	want := strings.TrimSpace(options[hardcoverListField])
	if match := hardcoverListLink.FindStringSubmatch(want); match != nil {
		want = match[2]
	}
	mine, err := l.Client.MyLists(ctx)
	if err != nil {
		return nil, hardcoverFailure(err)
	}
	var chosen *hardcover.List
	for index, one := range mine {
		if strings.EqualFold(one.Name, want) || strings.EqualFold(one.Slug, want) || fmt.Sprint(one.ID) == want {
			chosen = &mine[index]
			break
		}
	}
	if chosen == nil {
		return nil, fmt.Errorf("%w: %q is none of your Hardcover lists", ErrListNotFound, want)
	}
	var entries []media.ListEntry
	for page := range maxHardcoverPages {
		books, err := l.Client.ListBooks(ctx, chosen.ID, hardcoverPageSize, page*hardcoverPageSize)
		if err != nil {
			return nil, hardcoverFailure(err)
		}
		for _, book := range books {
			entries = append(entries, media.ListEntry{Title: book.Title, Year: book.Year, Form: "book", IDs: []media.ProviderID{{Provider: providerHardcover, ExternalID: fmt.Sprint(book.ID)}}})
		}
		if len(books) < hardcoverPageSize {
			break
		}
	}
	return entries, nil
}

func hardcoverFailure(err error) error {
	if errors.Is(err, hardcover.ErrNoKey) {
		return fmt.Errorf("%w: %w", ErrNeedsHardcoverKey, err)
	}
	return fmt.Errorf("%w: %w", ErrSourceUnavailable, err)
}

// WithHardcover adds the signed-in Hardcover account's lists over the client given.
func (c Catalogue) WithHardcover(client HardcoverLists) Catalogue {
	kinds := append([]Kind(nil), c.kinds...)
	kinds = append(kinds, Kind{
		Type: media.ListHardcover, Label: "Hardcover list", Fields: hardcoverFields, Source: HardcoverList{Client: client},
		Help: "The books on one of your own Hardcover lists; Solarr only reads it.",
	})
	return Catalogue{kinds: kinds}
}

// CollectionAlbum is one release group of a MusicBrainz collection.
type CollectionAlbum struct{ MBID, Title string }

// MusicBrainzCollections reads a public collection's release groups through the one MusicBrainz provider, so its rate limit is shared.
type MusicBrainzCollections interface {
	CollectionAlbums(ctx context.Context, collectionMBID string) ([]CollectionAlbum, error)
	// IsNotFound says the error is MusicBrainz answering that the collection does not exist or is private.
	IsNotFound(err error) bool
}

const musicBrainzCollectionField = "collection"

var musicBrainzCollectionFields = []Field{{Key: musicBrainzCollectionField, Label: "Collection", Kind: FieldText, Required: true,
	Help: "A public MusicBrainz collection of release groups: its id or its link, https://musicbrainz.org/collection/<id>"}}

// musicBrainzCollectionRef is a collection's id, bare or inside its link.
var musicBrainzCollectionRef = regexp.MustCompile(`(?i)^(?:https?://(?:www\.)?musicbrainz\.org/collection/)?([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})/?([?#].*)?$`)

// MusicBrainzCollectionID reads a collection's id from the id or its link, never fetching the link.
func MusicBrainzCollectionID(value string) (string, bool) {
	match := musicBrainzCollectionRef.FindStringSubmatch(strings.TrimSpace(value))
	if match == nil {
		return "", false
	}
	return strings.ToLower(match[1]), true
}

// MusicBrainzCollection is a public MusicBrainz collection of release groups.
type MusicBrainzCollection struct{ Client MusicBrainzCollections }

// Entries implements Source.
func (l MusicBrainzCollection) Entries(ctx context.Context, options Options) ([]media.ListEntry, error) {
	id, ok := MusicBrainzCollectionID(options[musicBrainzCollectionField])
	if !ok {
		return nil, fmt.Errorf("%w: %q is no MusicBrainz collection", ErrListNotFound, options[musicBrainzCollectionField])
	}
	albums, err := l.Client.CollectionAlbums(ctx, id)
	switch {
	case err != nil && l.Client.IsNotFound(err):
		return nil, fmt.Errorf("%w: %w", ErrListNotFound, err)
	case err != nil:
		return nil, fmt.Errorf("%w: %w", ErrSourceUnavailable, err)
	}
	entries := make([]media.ListEntry, 0, len(albums))
	for _, album := range albums {
		entries = append(entries, media.ListEntry{Title: album.Title, Form: "release-group", IDs: []media.ProviderID{{Provider: providerMBRelease, ExternalID: album.MBID}}})
	}
	return entries, nil
}

// WithMusicBrainzCollections adds a public MusicBrainz collection over the client given.
func (c Catalogue) WithMusicBrainzCollections(client MusicBrainzCollections) Catalogue {
	kinds := append([]Kind(nil), c.kinds...)
	kinds = append(kinds, Kind{
		Type: media.ListMusicBrainzCollection, Label: "MusicBrainz collection", Fields: musicBrainzCollectionFields, Source: MusicBrainzCollection{Client: client},
		Help: "The release groups of a public MusicBrainz collection.",
	})
	return Catalogue{kinds: kinds}
}
