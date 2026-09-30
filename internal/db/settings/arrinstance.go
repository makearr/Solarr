package settings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/settingsdb"
	"github.com/makearr/Solarr/internal/platform/config"
	"github.com/makearr/Solarr/internal/platform/secrets"
)

// ArrApp names one *Arr application client (todo 4.13, ADR-52). Only Lidarr
// is fully wired at stage 4 (music exists) -- the other four connect,
// preview and park their own intent verbatim until their own stage ships.
type ArrApp string

// Every app a connection can name.
const (
	ArrAppLidarr   ArrApp = "lidarr"
	ArrAppSonarr   ArrApp = "sonarr"
	ArrAppRadarr   ArrApp = "radarr"
	ArrAppReadarr  ArrApp = "readarr"
	ArrAppWhisparr ArrApp = "whisparr"
	// ArrAppProwlarr has no library: its active indexers and download clients are used from here.
	ArrAppProwlarr ArrApp = "prowlarr"
)

// ErrSecretsUnavailable means Cache.EnableSecrets was never called -- an
// *Arr instance's own API key can be neither sealed nor opened, refused
// rather than ever stored in plaintext (Invariant 61).
var ErrSecretsUnavailable = errors.New("settings: no cipher configured, cannot seal or open a secret")

// ArrInstance is one connection: an app, a base URL and an API key. The raw
// key is never part of this type -- it exists only between Cipher.Open and
// the one outbound request that needs it (DecryptedAPIKey), and an API
// response shows secrets.Secret's own Mask() instead.
type ArrInstance struct {
	ID            int64
	App           ArrApp
	Name          string
	BaseURL       string
	CategoryID    *int64
	Enabled       bool
	CreatedAt     int64
	UpdatedAt     int64
	LastSyncedAt  *int64
	LastSyncError string
}

// ArrInstanceRequest is what CreateArrInstance/UpdateArrInstance accept for
// a connection's own facts -- never the API key, which each has its own
// dedicated path (Create's own apiKey argument, SetArrInstanceAPIKey).
type ArrInstanceRequest struct {
	App        ArrApp
	Name       string
	BaseURL    string
	CategoryID *int64
	Enabled    bool
}

func validateArrInstance(request ArrInstanceRequest) config.Problems {
	var problems config.Problems
	switch request.App {
	case ArrAppLidarr, ArrAppSonarr, ArrAppRadarr, ArrAppReadarr, ArrAppWhisparr, ArrAppProwlarr:
	default:
		problems = append(problems, config.Problem{
			Path: "app", Value: string(request.App),
			Expectation: "one of lidarr, sonarr, radarr, readarr, whisparr",
		})
	}
	if strings.TrimSpace(request.Name) == "" {
		problems = append(problems, config.Problem{Path: "name", Expectation: "not empty"})
	}
	if strings.TrimSpace(request.BaseURL) == "" {
		problems = append(problems, config.Problem{Path: "base_url", Expectation: "not empty"})
	}
	return problems
}

// ArrInstances reads every configured connection. Bypasses the snapshot,
// the same as DownloadFolders -- nothing else in this process reads one yet.
func (c *Cache) ArrInstances(ctx context.Context) ([]ArrInstance, error) {
	rows, err := c.queries.ListArrInstances(ctx)
	if err != nil {
		return nil, fmt.Errorf("settings: list arr instances: %w", err)
	}
	instances := make([]ArrInstance, len(rows))
	for index, row := range rows {
		instances[index] = convertArrInstance(row)
	}
	return instances, nil
}

// GetArrInstance answers one connection by id. A missing id surfaces
// sql.ErrNoRows, wrapped.
func (c *Cache) GetArrInstance(ctx context.Context, id int64) (ArrInstance, error) {
	row, err := c.queries.GetArrInstance(ctx, id)
	if err != nil {
		return ArrInstance{}, fmt.Errorf("settings: get arr instance %d: %w", id, err)
	}
	return convertArrInstance(row), nil
}

// CreateArrInstance adds one connection, sealing apiKey at rest.
func (c *Cache) CreateArrInstance(
	ctx context.Context, request ArrInstanceRequest, apiKey secrets.Secret, at int64,
) (ArrInstance, error) {
	if c.cipher == nil {
		return ArrInstance{}, ErrSecretsUnavailable
	}
	if request.CategoryID != nil {
		if _, found := c.Snapshot().Category(*request.CategoryID); !found {
			return ArrInstance{}, fmt.Errorf("%w: category %d", ErrNotFound, *request.CategoryID)
		}
	}
	problems := validateArrInstance(request)
	if apiKey.IsZero() {
		problems = append(problems, config.Problem{Path: "api_key", Expectation: "not empty"})
	}
	if len(problems) > 0 {
		return ArrInstance{}, problems
	}
	sealed, err := c.cipher.Seal(apiKey)
	if err != nil {
		return ArrInstance{}, fmt.Errorf("settings: seal arr instance api key: %w", err)
	}

	var created settingsdb.ArrInstance
	err = c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		created, err = c.queries.WithTx(tx).CreateArrInstance(ctx, settingsdb.CreateArrInstanceParams{
			App: string(request.App), Name: request.Name, BaseUrl: request.BaseURL, ApiKey: sealed,
			CategoryID: nullInt64(request.CategoryID), Enabled: boolToInt(request.Enabled),
			CreatedAt: at, UpdatedAt: at,
		})
		return err
	})
	if err != nil {
		return ArrInstance{}, fmt.Errorf("settings: create arr instance: %w", err)
	}
	return convertArrInstance(created), nil
}

// UpdateArrInstance changes a connection's own facts -- never its api_key,
// SetArrInstanceAPIKey's own job.
func (c *Cache) UpdateArrInstance(ctx context.Context, id int64, request ArrInstanceRequest, at int64) (ArrInstance, error) {
	if _, err := c.GetArrInstance(ctx, id); err != nil {
		return ArrInstance{}, err
	}
	if request.CategoryID != nil {
		if _, found := c.Snapshot().Category(*request.CategoryID); !found {
			return ArrInstance{}, fmt.Errorf("%w: category %d", ErrNotFound, *request.CategoryID)
		}
	}
	if problems := validateArrInstance(request); len(problems) > 0 {
		return ArrInstance{}, problems
	}

	err := c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return c.queries.WithTx(tx).UpdateArrInstance(ctx, settingsdb.UpdateArrInstanceParams{
			Name: request.Name, BaseUrl: request.BaseURL, CategoryID: nullInt64(request.CategoryID),
			Enabled: boolToInt(request.Enabled), UpdatedAt: at, ID: id,
		})
	})
	if err != nil {
		return ArrInstance{}, fmt.Errorf("settings: update arr instance %d: %w", id, err)
	}
	return c.GetArrInstance(ctx, id)
}

// SetArrInstanceAPIKey reseals one connection's own credential -- a separate
// statement from UpdateArrInstance (Seal generates a fresh nonce every
// call, so writing it back unconditionally on every unrelated edit would be
// silent, pointless churn).
func (c *Cache) SetArrInstanceAPIKey(ctx context.Context, id int64, apiKey secrets.Secret, at int64) error {
	if c.cipher == nil {
		return ErrSecretsUnavailable
	}
	if apiKey.IsZero() {
		return config.Problems{{Path: "api_key", Expectation: "not empty"}}
	}
	sealed, err := c.cipher.Seal(apiKey)
	if err != nil {
		return fmt.Errorf("settings: seal arr instance api key: %w", err)
	}
	err = c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return c.queries.WithTx(tx).SetArrInstanceAPIKey(ctx, settingsdb.SetArrInstanceAPIKeyParams{
			ApiKey: sealed, UpdatedAt: at, ID: id,
		})
	})
	if err != nil {
		return fmt.Errorf("settings: set arr instance %d api key: %w", id, err)
	}
	return nil
}

// DecryptedAPIKey opens id's own sealed key -- the one place the raw
// credential exists, for the one call site (the sync job's own REST client)
// that must actually send it. Every other reader sees only Mask().
func (c *Cache) DecryptedAPIKey(ctx context.Context, id int64) (secrets.Secret, error) {
	if c.cipher == nil {
		return "", ErrSecretsUnavailable
	}
	row, err := c.queries.GetArrInstance(ctx, id)
	if err != nil {
		return "", fmt.Errorf("settings: get arr instance %d: %w", id, err)
	}
	opened, err := c.cipher.Open(row.ApiKey)
	if err != nil {
		return "", fmt.Errorf("settings: open arr instance %d api key: %w", id, err)
	}
	return opened, nil
}

// SetArrInstanceSyncResult records a sync run's own outcome, informational
// only -- errMsg empty means the last run succeeded.
func (c *Cache) SetArrInstanceSyncResult(ctx context.Context, id int64, at int64, errMsg string) error {
	err := c.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return c.queries.WithTx(tx).SetArrInstanceSyncResult(ctx, settingsdb.SetArrInstanceSyncResultParams{
			LastSyncedAt:  sql.NullInt64{Int64: at, Valid: true},
			LastSyncError: sql.NullString{String: errMsg, Valid: errMsg != ""},
			ID:            id,
		})
	})
	if err != nil {
		return fmt.Errorf("settings: set arr instance %d sync result: %w", id, err)
	}
	return nil
}

// DeleteArrInstance removes one connection. Deleting one that does not
// exist is not an error -- DELETE already answers "gone" either way.
func (c *Cache) DeleteArrInstance(ctx context.Context, id int64) error {
	err := c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return c.queries.WithTx(tx).DeleteArrInstance(ctx, id)
	})
	if err != nil {
		return fmt.Errorf("settings: delete arr instance %d: %w", id, err)
	}
	return nil
}

func convertArrInstance(row settingsdb.ArrInstance) ArrInstance {
	return ArrInstance{
		ID: row.ID, App: ArrApp(row.App), Name: row.Name, BaseURL: row.BaseUrl,
		CategoryID: int64Ptr(row.CategoryID), Enabled: row.Enabled != 0,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		LastSyncedAt: int64Ptr(row.LastSyncedAt), LastSyncError: row.LastSyncError.String,
	}
}
