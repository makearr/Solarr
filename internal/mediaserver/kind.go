// Package mediaserver describes the media servers Solarr can be connected to (Jellyfin, Navidrome, ...), what each kind can do,
// and the interfaces their clients implement. A kind's own package holds its wire format; nothing outside it names a kind.
package mediaserver

import "sort"

// Kind says what a media server kind can do; the interface offers a switch only where the kind can.
type Kind struct {
	Key   string
	Label string
	// Credential is how the connection signs in: CredentialKey (one key or token) or CredentialPassword (user name and password).
	Credential CredentialShape
	// CanPush says the server can be told of new files, PushByPath that it takes the exact path rather than only a library scan.
	CanPush    bool
	PushByPath bool
	// CanAnnounce says the server shows a message to people watching.
	CanAnnounce bool
	// CanPlayback says the server keeps watch state, PerUserPlayback that it keeps one per user.
	CanPlayback     bool
	PerUserPlayback bool
	CanMetadata     bool
	HasUsers        bool
	NeedsAdmin      bool
	MediaTypes      []string
	DefaultPortHint string
}

// CredentialShape is what a connection asks the person for.
type CredentialShape string

// The shapes of a credential.
const (
	CredentialKey      CredentialShape = "key"
	CredentialPassword CredentialShape = "password"
	// CredentialKeyOrPassword is a key alone, or a user name with a password the server may leave empty (Jellyfin, Kavita, Stash).
	CredentialKeyOrPassword CredentialShape = "key_or_password"
	// CredentialOptionalPassword is a password the server may not need (Calibre without authentication).
	CredentialOptionalPassword CredentialShape = "optional_password"
)

// Takes says the kind serves libraries of mediaType.
func (k Kind) Takes(mediaType string) bool {
	for _, taken := range k.MediaTypes {
		if taken == mediaType {
			return true
		}
	}
	return false
}

var kinds = map[string]Kind{
	"jellyfin": {
		Key: "jellyfin", Label: "Jellyfin", Credential: CredentialKeyOrPassword, CanPush: true, PushByPath: true, CanAnnounce: true,
		CanPlayback: true, PerUserPlayback: true, CanMetadata: true, HasUsers: true, NeedsAdmin: true,
		MediaTypes: []string{"tv", "movie", "music"}, DefaultPortHint: "8096",
	},
	"emby": {
		Key: "emby", Label: "Emby", Credential: CredentialKeyOrPassword, CanPush: true, PushByPath: true, CanAnnounce: true,
		CanPlayback: true, PerUserPlayback: true, CanMetadata: true, HasUsers: true, NeedsAdmin: true,
		MediaTypes: []string{"tv", "movie", "music"}, DefaultPortHint: "8096",
	},
	"navidrome": {
		Key: "navidrome", Label: "Navidrome", Credential: CredentialPassword, CanPush: true,
		CanPlayback: true, PerUserPlayback: true, CanMetadata: true,
		MediaTypes: []string{"music"}, DefaultPortHint: "4533",
	},
	"audiobookshelf": {
		Key: "audiobookshelf", Label: "Audiobookshelf", Credential: CredentialKey, CanPush: true,
		CanPlayback: true, PerUserPlayback: true, CanMetadata: true, HasUsers: true, NeedsAdmin: true,
		MediaTypes: []string{"audiobook", "book"}, DefaultPortHint: "13378",
	},
	"kavita": {
		Key: "kavita", Label: "Kavita", Credential: CredentialKeyOrPassword, CanPush: true, HasUsers: true,
		MediaTypes: []string{"book", "comic"}, DefaultPortHint: "5000",
	},
	"komga": {
		Key: "komga", Label: "Komga", Credential: CredentialKeyOrPassword, CanPush: true,
		MediaTypes: []string{"book", "comic"}, DefaultPortHint: "25600",
	},
	"calibre": {
		Key: "calibre", Label: "Calibre content server", Credential: CredentialOptionalPassword, CanMetadata: true,
		MediaTypes: []string{"book"}, DefaultPortHint: "8080",
	},
	"stash": {
		Key: "stash", Label: "Stash", Credential: CredentialKeyOrPassword, CanPush: true, PushByPath: true, CanMetadata: true,
		MediaTypes: []string{"adult"}, DefaultPortHint: "9999",
	},
}

// KindOf answers the kind under key.
func KindOf(key string) (Kind, bool) {
	kind, found := kinds[key]
	return kind, found
}

// Kinds lists every kind, ordered by key.
func Kinds() []Kind {
	all := make([]Kind, 0, len(kinds))
	for _, kind := range kinds {
		all = append(all, kind)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Key < all[j].Key })
	return all
}
