package importlist

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/stashbox"
)

// ErrNeedsStashKey is a stash-box list without the owner's key for that server.
var ErrNeedsStashKey = errors.New("importlist: this list needs the server's API key, under Settings, Metadata")

// stashPageSize is how many scenes one request of a favourites list asks for.
const stashPageSize = 50

const (
	stashServerField = "server"
	stashFavField    = "favourites"
	stashNewestField = "newest"
)

// StashServer is one stash-box client as a favourites list needs it.
type StashServer interface {
	Configured() bool
	FavouriteScenes(ctx context.Context, favourite string, page, perPage int) ([]stashbox.Scene, error)
}

var stashFields = []Field{
	{Key: stashServerField, Label: "Server", Kind: FieldChoice, Default: "stashdb", Choices: []media.Choice{{Key: "stashdb", Label: "StashDB"}, {Key: "fansdb", Label: "FansDB"}},
		Help: "Whose favourites: the account your API key belongs to."},
	{Key: stashFavField, Label: "Favourite", Kind: FieldChoice, Default: stashbox.FavouritePerformers, Choices: []media.Choice{
		{Key: stashbox.FavouritePerformers, Label: "Performers"}, {Key: stashbox.FavouriteStudios, Label: "Studios"}, {Key: stashbox.FavouriteEither, Label: "Performers or studios"}}},
	{Key: stashNewestField, Label: "Newest scenes", Kind: FieldChoice, Default: "50", Choices: []media.Choice{
		{Key: "50", Label: "50"}, {Key: "100", Label: "100"}, {Key: "200", Label: "200"}, {Key: "500", Label: "500"}},
		Help: "How many of the newest scenes the list reads each time; the ones already added are skipped."},
}

// StashFavourites is the newest scenes of an account's favourite performers and studios at a stash-box server: Whisparr's
// StashDB list.
type StashFavourites struct{ Servers map[string]StashServer }

// Entries implements Source.
func (s StashFavourites) Entries(ctx context.Context, options Options) ([]media.ListEntry, error) {
	name := option(options, stashFields, stashServerField)
	server, known := s.Servers[name]
	if !known || server == nil {
		return nil, fmt.Errorf("%w: no server %q", ErrListNotFound, name)
	}
	if !server.Configured() {
		return nil, ErrNeedsStashKey
	}
	newest, _ := strconv.Atoi(option(options, stashFields, stashNewestField))
	var entries []media.ListEntry
	for page := 1; len(entries) < newest; page++ {
		scenes, err := server.FavouriteScenes(ctx, option(options, stashFields, stashFavField), page, stashPageSize)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrSourceUnavailable, err)
		}
		for _, scene := range scenes {
			entries = append(entries, stashEntry(name, scene))
		}
		if len(scenes) < stashPageSize {
			break
		}
	}
	return entries[:min(len(entries), newest)], nil
}

func stashEntry(server string, scene stashbox.Scene) media.ListEntry {
	entry := media.ListEntry{Title: scene.Title, Form: "scene", IDs: []media.ProviderID{{Provider: server, ExternalID: scene.ID}}}
	if len(scene.ReleaseDate) >= 4 {
		entry.Year, _ = strconv.Atoi(scene.ReleaseDate[:4])
	}
	if len(scene.Images) > 0 {
		entry.PosterURL = scene.Images[0].URL
	}
	return entry
}

// WithStash adds the stash-box favourites list over the servers named.
func (c Catalogue) WithStash(servers map[string]StashServer) Catalogue {
	kinds := append([]Kind(nil), c.kinds...)
	kinds = append(kinds, Kind{
		Type: media.ListStashFavourites, Label: "Stash-box favourites", Fields: stashFields, Source: StashFavourites{Servers: servers},
		Help: "The newest scenes of the performers and studios you favourited at StashDB or FansDB.",
	})
	return Catalogue{kinds: kinds}
}
