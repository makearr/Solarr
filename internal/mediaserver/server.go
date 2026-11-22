package mediaserver

import (
	"context"
	"errors"
	"slices"
)

// Info is what Test learns of a server.
type Info struct {
	Name    string
	Version string
	// Admin says the credential may start scans and list users; false where the kind has no such notion.
	Admin bool
}

// Library is one library of the server.
type Library struct {
	ID    string
	Name  string
	Type  string
	Paths []string
}

// User is one account on the server.
type User struct {
	ID   string
	Name string
}

// Server is what every kind's client does.
type Server interface {
	Test(ctx context.Context) (Info, error)
	Libraries(ctx context.Context) ([]Library, error)
}

// UserLister is implemented by the kinds that have users other than the credential's own.
type UserLister interface {
	Users(ctx context.Context) ([]User, error)
}

// ErrNotAdmin means the credential works but is not an administrator's, which this call needs.
var ErrNotAdmin = errors.New("mediaserver: the credential is not an administrator's")

// ErrBusy says the server is working already; the paths stay queued and are tried again without counting as a failure.
var ErrBusy = errors.New("mediaserver: the server is busy scanning")

// LibraryIDsOf answers the distinct server libraries the changes belong to, in the order first seen.
func LibraryIDsOf(changes []Change) []string {
	var ids []string
	for _, change := range changes {
		for _, id := range change.LibraryIDs {
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// ErrItemNotFound means the server holds no item under that id.
var ErrItemNotFound = errors.New("mediaserver: the server has no such item")

// ErrRejected means the server refused the credential.
var ErrRejected = errors.New("mediaserver: the server refused the credential")

// Change is one path a media server is told of; Path is in the server's own terms.
type Change struct {
	Path string
	// Kind is "created", "modified" or "deleted".
	Kind string
	// LibraryIDs are the server's libraries the path belongs to, for a server that can only scan a library.
	LibraryIDs []string
}

// Announcer is implemented by the kinds that can show a message to the people watching right now.
type Announcer interface {
	Announce(ctx context.Context, text string) error
}

// Pusher is implemented by the kinds that can be told of new files; the kind decides between a targeted update and a scan.
type Pusher interface {
	Changed(ctx context.Context, changes []Change) error
}

// Play is one item a user has played or started, in the server's own terms.
type Play struct {
	// Path is the item's file on the server; empty where the kind lists none.
	Path string
	// ProviderIDs are the item's ids by lower-case provider name, as Solarr names them where the kind knows.
	ProviderIDs map[string]string
	// Whole says the play is of the whole work (an album), whatever file Path names.
	Whole      bool
	Played     bool
	PlayCount  int64
	PositionMS int64
	DurationMS int64
	// LastPlayed is unix seconds; zero when unknown.
	LastPlayed int64
}

// PlaybackReader is implemented by the kinds that tell what a user has played; reads only.
type PlaybackReader interface {
	// Played answers a page of the user's items, newest play first, and whether the end or an item older than since
	// (unix seconds) was reached. userID is empty for a kind that serves only the signed-in user.
	Played(ctx context.Context, userID string, since int64, offset, limit int) (items []Play, done bool, err error)
}

// Credit is a person on an item and what they did there.
type Credit struct {
	Name string
	Role string
	// Kind is "actor", "director", "writer" or the server's own word.
	Kind string
}

// Image is a picture of an item; URL is the server's, fetched with the connection's guard.
type Image struct {
	Kind string
	URL  string
}

// ItemRef names an item's parent: the series of an episode, the album of a track.
type ItemRef struct {
	ID    string
	Title string
}

// Item is what a media server knows of one work, in its own words; a type's FromServer turns it into the type's record.
type Item struct {
	ID            string
	MediaType     string
	Title         string
	OriginalTitle string
	SortTitle     string
	Overview      string
	Year          int
	// Date is the premiere or release day, YYYY-MM-DD, or empty.
	Date      string
	Genres    []string
	Tags      []string
	Rating    float64
	AgeRating string
	Language  string
	RuntimeMS int64
	// IDs are the provider ids by lower-case name (tmdb, imdb, tvdb, musicbrainzalbum, asin, ...).
	IDs           map[string]string
	People        []Credit
	Organisations []string
	Images        []Image
	Parent        *ItemRef
	Index         int
	ParentIndex   int
	Path          string
}

// Catalog is implemented by the kinds that answer what they know of a work; reads only.
type Catalog interface {
	// ItemByPath answers the item whose file is serverPath, in the server's own terms.
	ItemByPath(ctx context.Context, serverPath string) (Item, bool, error)
	Search(ctx context.Context, mediaType, query string, limit int) ([]Item, error)
	Item(ctx context.Context, id string) (Item, error)
}
