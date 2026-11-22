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

// MediaServer is one connection to a media server; the credential stays sealed until DecryptedMediaServerCredential.
type MediaServer struct {
	ID        int64
	Kind      string
	Name      string
	BaseURL   string
	Username  string
	Enabled   bool
	CreatedAt int64
	UpdatedAt int64
	LastOKAt  *int64
	LastError string
	MediaServerUse
}

// MediaServerUse holds the switches of a connection; the kind decides which of them mean anything.
type MediaServerUse struct {
	AsMetadata   bool
	PushFiles    bool
	Announce     bool
	SyncPlayback bool
}

// MediaServerRequest is a connection's own facts; the credential has its own path.
type MediaServerRequest struct {
	Kind     string
	Name     string
	BaseURL  string
	Username string
	Enabled  bool
	MediaServerUse
}

// MediaServerLibrary maps one Solarr library to one library of the server.
type MediaServerLibrary struct {
	CategoryID      int64
	ServerLibraryID string
	ServerLibrary   string
}

// MediaServerPath reads a Solarr path prefix as the server's own.
type MediaServerPath struct {
	SolarrPrefix string
	ServerPrefix string
}

// MediaServerUser is a user of the server, optionally tied to a Solarr user.
type MediaServerUser struct {
	ServerUserID string
	Name         string
	UserID       *int64
	Counted      bool
}

func validateMediaServer(request MediaServerRequest) config.Problems {
	var problems config.Problems
	if strings.TrimSpace(request.Kind) == "" {
		problems = append(problems, config.Problem{Path: "kind", Expectation: "not empty"})
	}
	if strings.TrimSpace(request.Name) == "" {
		problems = append(problems, config.Problem{Path: "name", Expectation: "not empty"})
	}
	if strings.TrimSpace(request.BaseURL) == "" {
		problems = append(problems, config.Problem{Path: "base_url", Expectation: "not empty"})
	}
	return problems
}

// MediaServers reads every connection.
func (c *Cache) MediaServers(ctx context.Context) ([]MediaServer, error) {
	rows, err := c.queries.ListMediaServers(ctx)
	if err != nil {
		return nil, fmt.Errorf("settings: list media servers: %w", err)
	}
	servers := make([]MediaServer, len(rows))
	for index, row := range rows {
		servers[index] = convertMediaServer(row)
	}
	return servers, nil
}

// GetMediaServer answers one connection; a missing id wraps sql.ErrNoRows.
func (c *Cache) GetMediaServer(ctx context.Context, id int64) (MediaServer, error) {
	row, err := c.queries.GetMediaServer(ctx, id)
	if err != nil {
		return MediaServer{}, fmt.Errorf("settings: get media server %d: %w", id, err)
	}
	return convertMediaServer(row), nil
}

// CreateMediaServer adds one connection, sealing the credential at rest.
func (c *Cache) CreateMediaServer(ctx context.Context, request MediaServerRequest, credential secrets.Secret, at int64) (MediaServer, error) {
	if c.cipher == nil {
		return MediaServer{}, ErrSecretsUnavailable
	}
	problems := validateMediaServer(request)
	if credential.IsZero() && request.Username == "" {
		problems = append(problems, config.Problem{Path: "credential", Expectation: "not empty without a user name"})
	}
	if len(problems) > 0 {
		return MediaServer{}, problems
	}
	sealed, err := c.cipher.Seal(credential)
	if err != nil {
		return MediaServer{}, fmt.Errorf("settings: seal media server credential: %w", err)
	}
	var created settingsdb.MediaServer
	err = c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		created, err = c.queries.WithTx(tx).CreateMediaServer(ctx, settingsdb.CreateMediaServerParams{
			Kind: request.Kind, Name: request.Name, BaseUrl: request.BaseURL, Username: request.Username, Credential: sealed,
			Enabled: boolToInt(request.Enabled), AsMetadata: boolToInt(request.AsMetadata), PushFiles: boolToInt(request.PushFiles),
			Announce: boolToInt(request.Announce), SyncPlayback: boolToInt(request.SyncPlayback), CreatedAt: at, UpdatedAt: at,
		})
		return err
	})
	if err != nil {
		return MediaServer{}, fmt.Errorf("settings: create media server: %w", err)
	}
	return convertMediaServer(created), nil
}

// UpdateMediaServer changes a connection's facts; its kind and credential stay.
func (c *Cache) UpdateMediaServer(ctx context.Context, id int64, request MediaServerRequest, at int64) (MediaServer, error) {
	if _, err := c.GetMediaServer(ctx, id); err != nil {
		return MediaServer{}, err
	}
	if problems := validateMediaServer(request); len(problems) > 0 {
		return MediaServer{}, problems
	}
	err := c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return c.queries.WithTx(tx).UpdateMediaServer(ctx, settingsdb.UpdateMediaServerParams{
			Name: request.Name, BaseUrl: request.BaseURL, Username: request.Username, Enabled: boolToInt(request.Enabled),
			AsMetadata: boolToInt(request.AsMetadata), PushFiles: boolToInt(request.PushFiles), Announce: boolToInt(request.Announce),
			SyncPlayback: boolToInt(request.SyncPlayback), UpdatedAt: at, ID: id,
		})
	})
	if err != nil {
		return MediaServer{}, fmt.Errorf("settings: update media server %d: %w", id, err)
	}
	return c.GetMediaServer(ctx, id)
}

// SetMediaServerCredential replaces the sealed credential.
func (c *Cache) SetMediaServerCredential(ctx context.Context, id int64, credential secrets.Secret, at int64) error {
	if c.cipher == nil {
		return ErrSecretsUnavailable
	}
	if credential.IsZero() {
		return config.Problems{{Path: "credential", Expectation: "not empty"}}
	}
	sealed, err := c.cipher.Seal(credential)
	if err != nil {
		return fmt.Errorf("settings: seal media server credential: %w", err)
	}
	err = c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return c.queries.WithTx(tx).SetMediaServerCredential(ctx, settingsdb.SetMediaServerCredentialParams{Credential: sealed, UpdatedAt: at, ID: id})
	})
	if err != nil {
		return fmt.Errorf("settings: set media server %d credential: %w", id, err)
	}
	return nil
}

// DecryptedMediaServerCredential opens the sealed credential for the one outbound call that sends it.
func (c *Cache) DecryptedMediaServerCredential(ctx context.Context, id int64) (secrets.Secret, error) {
	if c.cipher == nil {
		return "", ErrSecretsUnavailable
	}
	row, err := c.queries.GetMediaServer(ctx, id)
	if err != nil {
		return "", fmt.Errorf("settings: get media server %d: %w", id, err)
	}
	opened, err := c.cipher.Open(row.Credential)
	if err != nil {
		return "", fmt.Errorf("settings: open media server %d credential: %w", id, err)
	}
	return opened, nil
}

// SetMediaServerResult records the last contact; an empty message means it worked.
func (c *Cache) SetMediaServerResult(ctx context.Context, id, at int64, message string) error {
	err := c.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return c.queries.WithTx(tx).SetMediaServerResult(ctx, settingsdb.SetMediaServerResultParams{
			LastOkAt: sql.NullInt64{Int64: at, Valid: message == ""}, LastError: message, ID: id,
		})
	})
	if err != nil {
		return fmt.Errorf("settings: media server %d result: %w", id, err)
	}
	return nil
}

// DeleteMediaServer removes a connection and, by cascade, its mappings.
func (c *Cache) DeleteMediaServer(ctx context.Context, id int64) error {
	err := c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return c.queries.WithTx(tx).DeleteMediaServer(ctx, id)
	})
	if err != nil {
		return fmt.Errorf("settings: delete media server %d: %w", id, err)
	}
	return nil
}

// MediaServerLibraries answers the library mapping of one connection.
func (c *Cache) MediaServerLibraries(ctx context.Context, serverID int64) ([]MediaServerLibrary, error) {
	rows, err := c.queries.ListMediaServerLibraries(ctx, serverID)
	if err != nil {
		return nil, fmt.Errorf("settings: media server %d libraries: %w", serverID, err)
	}
	return convertMediaServerLibraries(rows), nil
}

// AllMediaServerLibraries answers every connection's mapping by server id.
func (c *Cache) AllMediaServerLibraries(ctx context.Context) (map[int64][]MediaServerLibrary, error) {
	rows, err := c.queries.ListAllMediaServerLibraries(ctx)
	if err != nil {
		return nil, fmt.Errorf("settings: media server libraries: %w", err)
	}
	byServer := map[int64][]MediaServerLibrary{}
	for _, row := range rows {
		byServer[row.ServerID] = append(byServer[row.ServerID], MediaServerLibrary{
			CategoryID: row.CategoryID, ServerLibraryID: row.ServerLibraryID, ServerLibrary: row.ServerLibrary,
		})
	}
	return byServer, nil
}

func convertMediaServerLibraries(rows []settingsdb.MediaServerLibrary) []MediaServerLibrary {
	libraries := make([]MediaServerLibrary, len(rows))
	for index, row := range rows {
		libraries[index] = MediaServerLibrary{CategoryID: row.CategoryID, ServerLibraryID: row.ServerLibraryID, ServerLibrary: row.ServerLibrary}
	}
	return libraries
}

// SetMediaServerLibraries replaces the library mapping of one connection.
func (c *Cache) SetMediaServerLibraries(ctx context.Context, serverID int64, libraries []MediaServerLibrary) error {
	for _, library := range libraries {
		if _, found := c.Snapshot().Category(library.CategoryID); !found {
			return fmt.Errorf("%w: category %d", ErrNotFound, library.CategoryID)
		}
	}
	err := c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := c.queries.WithTx(tx)
		if err := queries.DeleteMediaServerLibraries(ctx, serverID); err != nil {
			return err
		}
		for _, library := range libraries {
			if err := queries.AddMediaServerLibrary(ctx, settingsdb.AddMediaServerLibraryParams{
				ServerID: serverID, CategoryID: library.CategoryID, ServerLibraryID: library.ServerLibraryID, ServerLibrary: library.ServerLibrary,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("settings: set media server %d libraries: %w", serverID, err)
	}
	return nil
}

// MediaServerPaths answers the path pairs of one connection.
func (c *Cache) MediaServerPaths(ctx context.Context, serverID int64) ([]MediaServerPath, error) {
	rows, err := c.queries.ListMediaServerPaths(ctx, serverID)
	if err != nil {
		return nil, fmt.Errorf("settings: media server %d paths: %w", serverID, err)
	}
	paths := make([]MediaServerPath, len(rows))
	for index, row := range rows {
		paths[index] = MediaServerPath{SolarrPrefix: row.SolarrPrefix, ServerPrefix: row.ServerPrefix}
	}
	return paths, nil
}

// SetMediaServerPaths replaces the path pairs of one connection.
func (c *Cache) SetMediaServerPaths(ctx context.Context, serverID int64, paths []MediaServerPath) error {
	err := c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := c.queries.WithTx(tx)
		if err := queries.DeleteMediaServerPaths(ctx, serverID); err != nil {
			return err
		}
		for _, pair := range paths {
			if err := queries.AddMediaServerPath(ctx, settingsdb.AddMediaServerPathParams{
				ServerID: serverID, SolarrPrefix: pair.SolarrPrefix, ServerPrefix: pair.ServerPrefix,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("settings: set media server %d paths: %w", serverID, err)
	}
	return nil
}

// MediaServerUsers answers the users of one connection.
func (c *Cache) MediaServerUsers(ctx context.Context, serverID int64) ([]MediaServerUser, error) {
	rows, err := c.queries.ListMediaServerUsers(ctx, serverID)
	if err != nil {
		return nil, fmt.Errorf("settings: media server %d users: %w", serverID, err)
	}
	users := make([]MediaServerUser, len(rows))
	for index, row := range rows {
		users[index] = MediaServerUser{ServerUserID: row.ServerUserID, Name: row.Name, UserID: int64Ptr(row.UserID), Counted: row.Counted != 0}
	}
	return users, nil
}

// SetMediaServerUsers replaces the users of one connection.
func (c *Cache) SetMediaServerUsers(ctx context.Context, serverID int64, users []MediaServerUser) error {
	err := c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := c.queries.WithTx(tx)
		if err := queries.DeleteMediaServerUsers(ctx, serverID); err != nil {
			return err
		}
		for _, user := range users {
			if err := queries.AddMediaServerUser(ctx, settingsdb.AddMediaServerUserParams{
				ServerID: serverID, ServerUserID: user.ServerUserID, Name: user.Name, UserID: nullInt64(user.UserID), Counted: boolToInt(user.Counted),
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("settings: set media server %d users: %w", serverID, err)
	}
	return nil
}

func convertMediaServer(row settingsdb.MediaServer) MediaServer {
	return MediaServer{
		ID: row.ID, Kind: row.Kind, Name: row.Name, BaseURL: row.BaseUrl, Username: row.Username, Enabled: row.Enabled != 0,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, LastOKAt: int64Ptr(row.LastOkAt), LastError: row.LastError,
		MediaServerUse: MediaServerUse{
			AsMetadata: row.AsMetadata != 0, PushFiles: row.PushFiles != 0, Announce: row.Announce != 0, SyncPlayback: row.SyncPlayback != 0,
		},
	}
}

// KeyMediaServerPushDebounce is the instance setting for how long a queued push waits, in seconds.
const KeyMediaServerPushDebounce = "media_server.push_debounce_seconds"

// MediaServerPushDebounceSeconds is the configured wait; zero means the service default.
func (c *Cache) MediaServerPushDebounceSeconds() int64 {
	return c.Snapshot().policyInt(KeyMediaServerPushDebounce, 0)
}
