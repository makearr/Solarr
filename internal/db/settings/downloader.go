package settings

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/settingsdb"
	"github.com/makearr/Solarr/internal/platform/config"
	"github.com/makearr/Solarr/internal/platform/secrets"
)

// Downloader is one download client connection (stage 5, ADR-46/57): SABnzbd
// today, more as later stages need them. The raw credential is never part
// of this type -- it exists only between Cipher.Open and the one outbound
// request that needs it (DecryptedDownloaderCredentials), the same shape
// Indexer already uses. HasCredentials may be false while a connection is
// still being set up, the same way an indexer's own key can be absent.
type Downloader struct {
	ID             int64
	Name           string
	Impl           string
	BaseURL        string
	HasCredentials bool
	Enabled        bool
	Protocol       string
	// SettingsJSON carries whatever this impl needs beyond the shared
	// fields -- SABnzbd has none yet, present so a future impl needs no
	// further migration.
	SettingsJSON string
	CreatedAt    int64
	UpdatedAt    int64
}

// DownloaderRequest is what CreateDownloader/UpdateDownloader accept for a
// connection's own facts -- never its credential, which has its own
// dedicated path (Create's own credentials argument, SetDownloaderCredentials),
// the same split IndexerRequest already uses.
type DownloaderRequest struct {
	Name         string
	Impl         string
	BaseURL      string
	Enabled      bool
	Protocol     string
	SettingsJSON string
}

func validateDownloader(request DownloaderRequest) config.Problems {
	var problems config.Problems
	if strings.TrimSpace(request.Name) == "" {
		problems = append(problems, config.Problem{Path: "name", Expectation: "not empty"})
	}
	if strings.TrimSpace(request.Impl) == "" {
		problems = append(problems, config.Problem{Path: "impl", Expectation: "not empty"})
	}
	if strings.TrimSpace(request.BaseURL) == "" {
		problems = append(problems, config.Problem{Path: "base_url", Expectation: "not empty"})
	}
	switch request.Protocol {
	case "usenet", "torrent", "ddl":
	default:
		problems = append(problems, config.Problem{
			Path: "protocol", Value: request.Protocol, Expectation: "one of usenet, torrent, ddl",
		})
	}
	return problems
}

// Downloaders reads every configured connection. Bypasses the snapshot, the
// same as Indexers -- nothing else in this process reads one yet.
func (c *Cache) Downloaders(ctx context.Context) ([]Downloader, error) {
	rows, err := c.queries.ListDownloaders(ctx)
	if err != nil {
		return nil, fmt.Errorf("settings: list downloaders: %w", err)
	}
	downloaders := make([]Downloader, len(rows))
	for index, row := range rows {
		downloaders[index] = convertDownloader(row)
	}
	return downloaders, nil
}

// GetDownloader answers one connection by id. A missing id surfaces
// sql.ErrNoRows, wrapped.
func (c *Cache) GetDownloader(ctx context.Context, id int64) (Downloader, error) {
	row, err := c.queries.GetDownloader(ctx, id)
	if err != nil {
		return Downloader{}, fmt.Errorf("settings: get downloader %d: %w", id, err)
	}
	return convertDownloader(row), nil
}

// CreateDownloader adds one connection, sealing credentials at rest when
// given -- IsZero() stores none at all, the same optional shape Indexer's
// own api_key already uses.
func (c *Cache) CreateDownloader(ctx context.Context, request DownloaderRequest, credentials secrets.Secret, at int64) (Downloader, error) {
	if !credentials.IsZero() && c.cipher == nil {
		return Downloader{}, ErrSecretsUnavailable
	}
	if problems := validateDownloader(request); len(problems) > 0 {
		return Downloader{}, problems
	}
	sealed, err := c.sealOptional(credentials)
	if err != nil {
		return Downloader{}, fmt.Errorf("settings: seal downloader credentials: %w", err)
	}

	var created settingsdb.Downloader
	err = c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		created, err = c.queries.WithTx(tx).CreateDownloader(ctx, settingsdb.CreateDownloaderParams{
			Name: request.Name, Impl: request.Impl, BaseUrl: request.BaseURL, Credentials: sealed,
			Enabled: boolToInt(request.Enabled), Protocol: request.Protocol,
			CreatedAt: at, UpdatedAt: at,
		})
		return err
	})
	if err != nil {
		return Downloader{}, fmt.Errorf("settings: create downloader: %w", err)
	}
	return convertDownloader(created), nil
}

// UpdateDownloader changes a connection's own facts -- never its
// credentials, SetDownloaderCredentials's own job.
func (c *Cache) UpdateDownloader(ctx context.Context, id int64, request DownloaderRequest, at int64) (Downloader, error) {
	if _, err := c.GetDownloader(ctx, id); err != nil {
		return Downloader{}, err
	}
	if problems := validateDownloader(request); len(problems) > 0 {
		return Downloader{}, problems
	}

	err := c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return c.queries.WithTx(tx).UpdateDownloader(ctx, settingsdb.UpdateDownloaderParams{
			Name: request.Name, Impl: request.Impl, BaseUrl: request.BaseURL,
			Enabled: boolToInt(request.Enabled), Protocol: request.Protocol,
			SettingsJson: sql.NullString{String: request.SettingsJSON, Valid: request.SettingsJSON != ""},
			UpdatedAt:    at, ID: id,
		})
	})
	if err != nil {
		return Downloader{}, fmt.Errorf("settings: update downloader %d: %w", id, err)
	}
	return c.GetDownloader(ctx, id)
}

// SetDownloaderCredentials reseals one connection's own credential, or
// clears it (credentials.IsZero()) -- a separate statement from
// UpdateDownloader, the same reasoning Indexer's own SetIndexerAPIKey gives.
func (c *Cache) SetDownloaderCredentials(ctx context.Context, id int64, credentials secrets.Secret, at int64) error {
	if !credentials.IsZero() && c.cipher == nil {
		return ErrSecretsUnavailable
	}
	sealed, err := c.sealOptional(credentials)
	if err != nil {
		return fmt.Errorf("settings: seal downloader credentials: %w", err)
	}
	err = c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return c.queries.WithTx(tx).SetDownloaderCredentials(ctx, settingsdb.SetDownloaderCredentialsParams{
			Credentials: sealed, UpdatedAt: at, ID: id,
		})
	})
	if err != nil {
		return fmt.Errorf("settings: set downloader %d credentials: %w", id, err)
	}
	return nil
}

// DecryptedDownloaderCredentials opens id's own sealed credential -- the one
// place the raw secret exists, for the one call site (the downloader
// client) that must actually send it. Every other reader sees only
// HasCredentials. Returns a zero Secret, no error, for a connection that
// has none configured at all.
func (c *Cache) DecryptedDownloaderCredentials(ctx context.Context, id int64) (secrets.Secret, error) {
	row, err := c.queries.GetDownloader(ctx, id)
	if err != nil {
		return "", fmt.Errorf("settings: get downloader %d: %w", id, err)
	}
	if len(row.Credentials) == 0 {
		return "", nil
	}
	if c.cipher == nil {
		return "", ErrSecretsUnavailable
	}
	opened, err := c.cipher.Open(row.Credentials)
	if err != nil {
		return "", fmt.Errorf("settings: open downloader %d credentials: %w", id, err)
	}
	return opened, nil
}

// DeleteDownloader removes one connection. Deleting one that does not exist
// is not an error -- DELETE already answers "gone" either way.
func (c *Cache) DeleteDownloader(ctx context.Context, id int64) error {
	err := c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return c.queries.WithTx(tx).DeleteDownloader(ctx, id)
	})
	if err != nil {
		return fmt.Errorf("settings: delete downloader %d: %w", id, err)
	}
	return nil
}

func convertDownloader(row settingsdb.Downloader) Downloader {
	return Downloader{
		ID: row.ID, Name: row.Name, Impl: row.Impl, BaseURL: row.BaseUrl,
		HasCredentials: len(row.Credentials) > 0, Enabled: row.Enabled != 0, Protocol: row.Protocol,
		SettingsJSON: row.SettingsJson.String, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}
