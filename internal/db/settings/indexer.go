package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/settingsdb"
	"github.com/makearr/Solarr/internal/platform/config"
	"github.com/makearr/Solarr/internal/platform/secrets"
)

// Indexer is one search provider connection (stage 5, ADR-37/43/47): Newznab
// or Torznab, spoken natively rather than through Cardigann (ADR-43). The
// raw key is never part of this type -- it exists only between Cipher.Open
// and the one outbound request that needs it (DecryptedIndexerAPIKey), the
// same shape ArrInstance already uses. Unlike ArrInstance's own key,
// HasAPIKey may be false: a public Torznab endpoint can need none at all.
type Indexer struct {
	ID        int64
	Name      string
	Impl      string
	BaseURL   string
	HasAPIKey bool
	Enabled   bool
	Priority  int64
	// CategoriesJSON is populated from the indexer's own t=caps response,
	// never assumed (ADR-43) -- empty until the first caps fetch.
	CategoriesJSON string
	CapsFetchedAt  *int64
	// SettingsJSON is ADR-47's own egress block ({proxy_url, flaresolverr_url}),
	// expert tier. FlareSolverr is not wired yet (needs its own cached-cookie
	// state, not just a client-construction change) -- Egress only ever reads
	// ProxyURL from it today.
	SettingsJSON string
	CreatedAt    int64
	UpdatedAt    int64
}

// IndexerEgress is ADR-47's own egress block, encoded into Indexer.
// SettingsJSON under the "egress" key. FlareSolverr's own cached-cookie
// state deliberately has no field here yet -- it needs more than a URL to
// represent (todo, private/questions.md).
type IndexerEgress struct {
	ProxyURL string `json:"proxy_url,omitempty"`
}

// indexerSettingsJSON is SettingsJSON's own top-level shape -- a wrapper
// object so a future sibling (metadata source selection, say) can share the
// same column without colliding with "egress".
type indexerSettingsJSON struct {
	Egress IndexerEgress `json:"egress,omitempty"`
}

// Egress parses SettingsJSON's own egress block. An empty or unparseable
// blob answers the zero value -- an indexer with no egress configured (or
// predating this field) behaves exactly as before ADR-47.
func (i Indexer) Egress() IndexerEgress {
	if i.SettingsJSON == "" {
		return IndexerEgress{}
	}
	var parsed indexerSettingsJSON
	if err := json.Unmarshal([]byte(i.SettingsJSON), &parsed); err != nil {
		return IndexerEgress{}
	}
	return parsed.Egress
}

// buildIndexerSettingsJSON encodes egressProxyURL into SettingsJSON's own
// shape, or "" for no egress configured -- kept out of the stored value
// entirely rather than an empty egress object, so an indexer nobody has
// ever configured egress for still reads back with SettingsJSON == "".
func buildIndexerSettingsJSON(egressProxyURL string) string {
	if egressProxyURL == "" {
		return ""
	}
	encoded, err := json.Marshal(indexerSettingsJSON{Egress: IndexerEgress{ProxyURL: egressProxyURL}})
	if err != nil {
		return ""
	}
	return string(encoded)
}

// IndexerRequest is what CreateIndexer/UpdateIndexer accept for a
// connection's own facts -- never the API key, which has its own dedicated
// path (Create's own apiKey argument, SetIndexerAPIKey), the same split
// ArrInstanceRequest already uses.
type IndexerRequest struct {
	Name           string
	Impl           string
	BaseURL        string
	Enabled        bool
	Priority       int64
	EgressProxyURL string
}

func validateIndexer(request IndexerRequest) config.Problems {
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
	return problems
}

// Indexers reads every configured connection. Bypasses the snapshot, the
// same as ArrInstances -- nothing else in this process reads one yet.
func (c *Cache) Indexers(ctx context.Context) ([]Indexer, error) {
	rows, err := c.queries.ListIndexers(ctx)
	if err != nil {
		return nil, fmt.Errorf("settings: list indexers: %w", err)
	}
	indexers := make([]Indexer, len(rows))
	for index, row := range rows {
		indexers[index] = convertIndexer(row)
	}
	return indexers, nil
}

// GetIndexer answers one connection by id. A missing id surfaces
// sql.ErrNoRows, wrapped.
func (c *Cache) GetIndexer(ctx context.Context, id int64) (Indexer, error) {
	row, err := c.queries.GetIndexer(ctx, id)
	if err != nil {
		return Indexer{}, fmt.Errorf("settings: get indexer %d: %w", id, err)
	}
	return convertIndexer(row), nil
}

// CreateIndexer adds one connection, sealing apiKey at rest when given --
// IsZero() stores no key at all, for a public endpoint that needs none.
func (c *Cache) CreateIndexer(ctx context.Context, request IndexerRequest, apiKey secrets.Secret, at int64) (Indexer, error) {
	if !apiKey.IsZero() && c.cipher == nil {
		return Indexer{}, ErrSecretsUnavailable
	}
	if problems := validateIndexer(request); len(problems) > 0 {
		return Indexer{}, problems
	}
	sealed, err := c.sealOptional(apiKey)
	if err != nil {
		return Indexer{}, fmt.Errorf("settings: seal indexer api key: %w", err)
	}

	settingsJSON := buildIndexerSettingsJSON(request.EgressProxyURL)
	var created settingsdb.Indexer
	err = c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		created, err = c.queries.WithTx(tx).CreateIndexer(ctx, settingsdb.CreateIndexerParams{
			Name: request.Name, Impl: request.Impl, BaseUrl: request.BaseURL, ApiKey: sealed,
			Enabled: boolToInt(request.Enabled), Priority: request.Priority,
			SettingsJson: sql.NullString{String: settingsJSON, Valid: settingsJSON != ""},
			CreatedAt:    at, UpdatedAt: at,
		})
		return err
	})
	if err != nil {
		return Indexer{}, fmt.Errorf("settings: create indexer: %w", err)
	}
	return convertIndexer(created), nil
}

// UpdateIndexer changes a connection's own facts -- never its api_key,
// SetIndexerAPIKey's own job.
func (c *Cache) UpdateIndexer(ctx context.Context, id int64, request IndexerRequest, at int64) (Indexer, error) {
	if _, err := c.GetIndexer(ctx, id); err != nil {
		return Indexer{}, err
	}
	if problems := validateIndexer(request); len(problems) > 0 {
		return Indexer{}, problems
	}

	settingsJSON := buildIndexerSettingsJSON(request.EgressProxyURL)
	err := c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return c.queries.WithTx(tx).UpdateIndexer(ctx, settingsdb.UpdateIndexerParams{
			Name: request.Name, Impl: request.Impl, BaseUrl: request.BaseURL,
			Enabled: boolToInt(request.Enabled), Priority: request.Priority,
			SettingsJson: sql.NullString{String: settingsJSON, Valid: settingsJSON != ""},
			UpdatedAt:    at, ID: id,
		})
	})
	if err != nil {
		return Indexer{}, fmt.Errorf("settings: update indexer %d: %w", id, err)
	}
	return c.GetIndexer(ctx, id)
}

// SetIndexerAPIKey reseals one connection's own credential, or clears it
// (apiKey.IsZero()) -- a separate statement from UpdateIndexer, the same
// reasoning ArrInstance's own SetArrInstanceAPIKey gives: Seal generates a
// fresh nonce every call, so writing it back unconditionally on every
// unrelated edit would be silent, pointless churn.
func (c *Cache) SetIndexerAPIKey(ctx context.Context, id int64, apiKey secrets.Secret, at int64) error {
	if !apiKey.IsZero() && c.cipher == nil {
		return ErrSecretsUnavailable
	}
	sealed, err := c.sealOptional(apiKey)
	if err != nil {
		return fmt.Errorf("settings: seal indexer api key: %w", err)
	}
	err = c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return c.queries.WithTx(tx).SetIndexerAPIKey(ctx, settingsdb.SetIndexerAPIKeyParams{
			ApiKey: sealed, UpdatedAt: at, ID: id,
		})
	})
	if err != nil {
		return fmt.Errorf("settings: set indexer %d api key: %w", id, err)
	}
	return nil
}

// DecryptedIndexerAPIKey opens id's own sealed key -- the one place the raw
// credential exists, for the one call site (the search/caps client) that
// must actually send it. Every other reader sees only HasAPIKey. Returns a
// zero Secret, no error, for an indexer that has no key configured at all.
func (c *Cache) DecryptedIndexerAPIKey(ctx context.Context, id int64) (secrets.Secret, error) {
	row, err := c.queries.GetIndexer(ctx, id)
	if err != nil {
		return "", fmt.Errorf("settings: get indexer %d: %w", id, err)
	}
	if len(row.ApiKey) == 0 {
		return "", nil
	}
	if c.cipher == nil {
		return "", ErrSecretsUnavailable
	}
	opened, err := c.cipher.Open(row.ApiKey)
	if err != nil {
		return "", fmt.Errorf("settings: open indexer %d api key: %w", id, err)
	}
	return opened, nil
}

// SetIndexerCaps records what the indexer's own t=caps response answered
// (ADR-43: categories are taken from the response, never assumed).
func (c *Cache) SetIndexerCaps(ctx context.Context, id int64, categoriesJSON string, at int64) error {
	err := c.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return c.queries.WithTx(tx).SetIndexerCaps(ctx, settingsdb.SetIndexerCapsParams{
			CategoriesJson: sql.NullString{String: categoriesJSON, Valid: categoriesJSON != ""},
			CapsFetchedAt:  sql.NullInt64{Int64: at, Valid: true},
			UpdatedAt:      at, ID: id,
		})
	})
	if err != nil {
		return fmt.Errorf("settings: set indexer %d caps: %w", id, err)
	}
	return nil
}

// DeleteIndexer removes one connection. Deleting one that does not exist is
// not an error -- DELETE already answers "gone" either way.
func (c *Cache) DeleteIndexer(ctx context.Context, id int64) error {
	err := c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return c.queries.WithTx(tx).DeleteIndexer(ctx, id)
	})
	if err != nil {
		return fmt.Errorf("settings: delete indexer %d: %w", id, err)
	}
	return nil
}

// sealOptional seals a secret, or returns a nil BLOB for an empty one --
// indexer.api_key is nullable, unlike ArrInstance's own NOT NULL column.
func (c *Cache) sealOptional(secret secrets.Secret) ([]byte, error) {
	if secret.IsZero() {
		return nil, nil
	}
	return c.cipher.Seal(secret)
}

func convertIndexer(row settingsdb.Indexer) Indexer {
	return Indexer{
		ID: row.ID, Name: row.Name, Impl: row.Impl, BaseURL: row.BaseUrl,
		HasAPIKey: len(row.ApiKey) > 0, Enabled: row.Enabled != 0, Priority: row.Priority,
		CategoriesJSON: row.CategoriesJson.String, CapsFetchedAt: int64Ptr(row.CapsFetchedAt),
		SettingsJSON: row.SettingsJson.String, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}
