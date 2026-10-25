package download

import (
	"context"
	"errors"
)

// Protocol is how a client fetches what it is sent; a release goes only to a client of its own protocol.
type Protocol string

// The protocols a download client speaks.
const (
	ProtocolUsenet  Protocol = "usenet"
	ProtocolTorrent Protocol = "torrent"
	// ProtocolDDL is a direct download from a peer or host, Soulseek's included.
	ProtocolDDL Protocol = "ddl"
)

// FieldKey names one setting a client needs beyond the shared ones (Sonarr's per-client settings fields).
type FieldKey string

// The client-specific settings; each maps onto one Connection field.
const (
	FieldURLBase         FieldKey = "url_base"
	FieldDropFolder      FieldKey = "drop_folder"
	FieldWatchFolder     FieldKey = "watch_folder"
	FieldSaveMagnetFiles FieldKey = "save_magnet_files"
)

// FieldKind is the control a setting takes.
type FieldKind string

// The kinds a settings form knows how to draw.
const (
	FieldText FieldKind = "text"
	FieldPath FieldKind = "path"
	FieldBool FieldKind = "bool"
)

// Field is one client-specific setting, as the settings form shows it.
type Field struct {
	Key         FieldKey
	Kind        FieldKind
	Label       string
	Help        string
	Placeholder string
	Required    bool
}

// Definition is what a client kind declares about itself (Sonarr's download client schema): every kind is offered
// to the user from these, and nothing about a kind is decided anywhere else.
type Definition struct {
	// Impl is the *Arr implementation name, lower case, so a client read from Lidarr or Sonarr is recognised.
	Impl     string
	Label    string
	Protocol Protocol
	// AddressPlaceholder is an example address; empty for a client that has none (a blackhole folder).
	AddressPlaceholder string
	// UserName is a client that signs in with a user name besides its secret.
	UserName bool
	// SecretLabel names the secret ("API key", "Password"); empty for a client that takes none.
	SecretLabel string
	// KeepsCompleted keeps an imported download by default: a torrent goes on seeding.
	KeepsCompleted bool
	// Categories is a client that files downloads under a category or label.
	Categories bool
	Fields     []Field
}

// HasAddress is a client reached over the network rather than through a folder.
func (d Definition) HasAddress() bool { return d.AddressPlaceholder != "" }

// Connection is what a client is built from: the shared fields every client row has, and the client-specific ones.
type Connection struct {
	BaseURL string
	// Credentials is the secret, or "user:password" for a client with a UserName.
	Credentials string
	// URLBase is the path of the client's API below its address; empty takes the client's default.
	URLBase string
	// DropFolder is where a blackhole writes what it is sent, and WatchFolder where finished downloads appear.
	DropFolder  string
	WatchFolder string
	// SaveMagnetFiles lets a torrent blackhole write a magnet link to a file rather than refuse it.
	SaveMagnetFiles bool
}

// Probe is what a connection test learns from a client.
type Probe struct {
	// Version names the client and its version: "qBittorrent v5.2.3".
	Version string
	// DownloadFolder is where finished downloads are, as the client names it.
	DownloadFolder string
	// Categories are the client's categories; nil for a client that cannot list them.
	Categories []string
	// CreatesCategories is a client that creates a missing category when the first download is sent under it.
	CreatesCategories bool
	// Notes are anything else the person adding the client should know.
	Notes []string
}

// Prober is a client's connection test: read-only, it proves the address and the credentials.
type Prober interface {
	Probe(ctx context.Context) (Probe, error)
}

// Driver is a configured client as the registry builds it: everything Solarr does with it, and its test.
type Driver interface {
	Client
	Prober
}

// ErrNotSupported is a client asked for something its kind cannot do, such as pausing a blackhole.
var ErrNotSupported = errors.New("the download client cannot do that")
