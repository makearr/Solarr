package indexer

import (
	"context"
	"strings"
)

// Driver is one configured indexer as every search reads it: what it can search, a search, and fetching the file a
// result links to. Client (Newznab/Torznab) is one; every API indexer embeds Base and adds its own Search.
type Driver interface {
	// Capabilities answers what the indexer searches; an API indexer also proves its address and key with it.
	Capabilities(ctx context.Context) (Capabilities, error)
	Search(ctx context.Context, request SearchRequest) ([]RawItem, error)
	Fetch(ctx context.Context, link string) ([]byte, error)
}

var _ Driver = Client{}

// Privacy is who may use an indexer, as Prowlarr labels it.
type Privacy string

// The privacy levels of Prowlarr's catalog.
const (
	PrivacyPublic      Privacy = "public"
	PrivacySemiPrivate Privacy = "semi-private"
	PrivacyPrivate     Privacy = "private"
)

// The indexer implementations, by settings' impl value. One implementation can serve several catalog entries
// (every Newznab preset is newznab).
const (
	ImplNewznab         = "newznab"
	ImplTorznab         = "torznab"
	ImplThePirateBay    = "thepiratebay"
	ImplNyaa            = "nyaa"
	ImplYTS             = "yts"
	ImplKnaben          = "knaben"
	ImplInternetArchive = "internetarchive"
	ImplSubsPlease      = "subsplease"
	ImplRedacted        = "redacted"
	ImplOrpheus         = "orpheus"
	ImplFileList        = "filelist"
	ImplHDBits          = "hdbits"
	ImplPassThePopcorn  = "passthepopcorn"
	ImplBeyondHD        = "beyondhd"
	ImplBroadcastheNet  = "broadcasthenet"
	ImplSoulseek        = "soulseek"
)

// ProtocolOf is how the releases of an implementation are downloaded: candidate.protocol's vocabulary.
func ProtocolOf(impl string) string {
	switch impl {
	case ImplNewznab:
		return "usenet"
	case ImplSoulseek:
		return "ddl"
	case ImplTorznab, ImplThePirateBay, ImplNyaa, ImplYTS, ImplKnaben, ImplInternetArchive, ImplSubsPlease, ImplRedacted,
		ImplOrpheus, ImplFileList, ImplHDBits, ImplPassThePopcorn, ImplBeyondHD, ImplBroadcastheNet:
		return "torrent"
	}
	return "usenet"
}

// Credentials are what an indexer signs in with: a user name (or a second key) and a secret, stored together as
// "user:secret" when both are asked for.
type Credentials struct {
	// UserLabel names the first part ("User name", "API user"); empty for an indexer that has none.
	UserLabel string
	// SecretLabel names the secret ("API key", "Passkey"); empty for an indexer that needs none.
	SecretLabel string
	// Optional is a secret an indexer works without (a public Newznab feed).
	Optional bool
}

// Media types a catalog entry is listed under.
const (
	MediaMusic  = "music"
	MediaTV     = "tv"
	MediaMovies = "movies"
	MediaAnime  = "anime"
	MediaBooks  = "books"
)

// Definition is one entry of the indexer catalog, as Prowlarr's add list shows it.
type Definition struct {
	// ID is the catalog's own name for the entry, stable across releases: "nzbgeek", "thepiratebay".
	ID          string
	Name        string
	Description string
	Impl        string
	Privacy     Privacy
	Language    string
	// BaseURLs are the addresses the indexer answers at; the first is the default. Empty for a generic entry, which
	// asks for one.
	BaseURLs    []string
	MediaTypes  []string
	Credentials Credentials
	// CompanionDownloader is the download client its releases need, offered when it is added ("slskd").
	CompanionDownloader string
}

// Protocol is how the entry's releases are downloaded.
func (d Definition) Protocol() string { return ProtocolOf(d.Impl) }

// Connection is what an indexer is built from: its address and its secret ("user:secret" for two-part credentials).
type Connection struct {
	BaseURL string
	APIKey  string
}

// SplitCredentials reads "user:secret" back; a value without a colon is all secret.
func SplitCredentials(credentials string) (user, secret string) {
	user, secret, found := strings.Cut(credentials, ":")
	if !found {
		return "", credentials
	}
	return user, secret
}
