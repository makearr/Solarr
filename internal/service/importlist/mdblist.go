package importlist

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/mdblist"
)

// ErrNeedsMDBListKey says the list cannot be read before MDBList's key is stored.
var ErrNeedsMDBListKey = errors.New("importlist: this list needs the MDBList key, under Settings, Metadata")

// Page bounds: 100 items a request, a list's first thousand.
const (
	mdblistPageSize = 100
	maxMDBListPages = 10
	mdblistListKey  = "mdblist"
)

var mdblistFields = []Field{{Key: mdblistListKey, Label: "List", Kind: FieldText, Required: true,
	Help: "A public MDBList: its number, user/name, or its link https://mdblist.com/lists/... Import an IMDb list into MDBList to read it here."}}

// MDBListLists is the MDBList client as its lists need it.
type MDBListLists interface {
	ListItems(ctx context.Context, list string, offset, limit int) (mdblist.ListPage, error)
}

// mdblistLink is a list's page on mdblist.com: /lists/<user>/<name>.
var mdblistLink = regexp.MustCompile(`^https?://(www\.)?mdblist\.com/lists/([^/?#\s]+)/([^/?#\s]+)/?([?#].*)?$`)

var mdblistNamed = regexp.MustCompile(`^[^/\s]+/[^/\s]+$`)

// MDBListRef reads a list's number or "user/name" from either itself or its link, never fetching the link.
func MDBListRef(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if id, err := strconv.ParseInt(value, 10, 64); err == nil && id > 0 {
		return value, true
	}
	if match := mdblistLink.FindStringSubmatch(value); match != nil {
		return match[2] + "/" + match[3], true
	}
	return value, mdblistNamed.MatchString(value)
}

// MDBList is a public MDBList list; its shows are read, its films are left to a movie library.
type MDBList struct{ Client MDBListLists }

// Entries implements Source.
func (l MDBList) Entries(ctx context.Context, options Options) ([]media.ListEntry, error) {
	ref, ok := MDBListRef(options[mdblistListKey])
	if !ok {
		return nil, fmt.Errorf("%w: %q is no MDBList list", ErrListNotFound, options[mdblistListKey])
	}
	var entries []media.ListEntry
	for page := range maxMDBListPages {
		found, err := l.Client.ListItems(ctx, ref, page*mdblistPageSize, mdblistPageSize)
		switch {
		case errors.Is(err, mdblist.ErrNoKey):
			return nil, fmt.Errorf("%w: %w", ErrNeedsMDBListKey, err)
		case errors.Is(err, mdblist.ErrNotFound):
			return nil, fmt.Errorf("%w: %w", ErrListNotFound, err)
		case err != nil:
			return nil, fmt.Errorf("%w: %w", ErrSourceUnavailable, err)
		}
		for _, item := range found.Shows {
			entries = append(entries, mdblistEntry(item))
		}
		if !found.Pagination.HasMore {
			break
		}
	}
	return entries, nil
}

// mdblistEntry names the show by MDBList first, then the ids a TV library adds by: TheTVDB's, else TMDB's.
func mdblistEntry(item mdblist.ListItem) media.ListEntry {
	entry := media.ListEntry{Title: item.Title, Year: item.Year, Form: "tv",
		IDs: []media.ProviderID{{Provider: "mdblist", ExternalID: item.IDs.MDBList}}}
	if item.IDs.TVDB > 0 {
		entry.IDs = append(entry.IDs, media.ProviderID{Provider: "tvdb", ExternalID: strconv.FormatInt(item.IDs.TVDB, 10)})
	}
	if item.IDs.TMDB > 0 {
		entry.IDs = append(entry.IDs, media.ProviderID{Provider: providerTMDB, ExternalID: strconv.FormatInt(item.IDs.TMDB, 10)})
	}
	return entry
}

// WithMDBList adds the MDBList list over the client given.
func (c Catalogue) WithMDBList(client MDBListLists) Catalogue {
	kinds := append([]Kind(nil), c.kinds...)
	kinds = append(kinds, Kind{
		Type: media.ListMDBList, Label: "MDBList list", Fields: mdblistFields, Source: MDBList{Client: client},
		Help: "A public MDBList list, such as one you imported from IMDb there; its films are left out of a TV library.",
	})
	return Catalogue{kinds: kinds}
}
