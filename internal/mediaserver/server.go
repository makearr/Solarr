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
