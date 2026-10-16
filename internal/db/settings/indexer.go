package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
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
	// MusicCategories are the Newznab categories music is asked for in (D17); absent is the default.
	MusicCategories []int `json:"music_categories,omitempty"`
}

// DefaultMusicCategories is Newznab's Audio: without a category an indexer's feed is mostly
// television (2026-09-30).
var DefaultMusicCategories = []int{3000}

// DefaultTVCategories is Newznab's TV parent category, which covers its sub-categories.
var DefaultTVCategories = []int{5000}

// DefaultMovieCategories is Newznab's Movies parent category, which covers its sub-categories.
var DefaultMovieCategories = []int{2000}

// MovieCategories are the categories movies are searched and read in.
func (i Indexer) MovieCategories() []int { return slices.Clone(DefaultMovieCategories) }

// TVCategories are the categories TV is searched and read in.
func (i Indexer) TVCategories() []int { return slices.Clone(DefaultTVCategories) }

// maxMusicCategories bounds the list; a real indexer's music tree has a dozen at most.
const maxMusicCategories = 32

// MusicCategories are the categories music is searched and read in, DefaultMusicCategories unless set.
func (i Indexer) MusicCategories() []int {
	if i.SettingsJSON != "" {
		var parsed indexerSettingsJSON
		if err := json.Unmarshal([]byte(i.SettingsJSON), &parsed); err == nil && len(parsed.MusicCategories) > 0 {
			return parsed.MusicCategories
		}
	}
	return slices.Clone(DefaultMusicCategories)
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

// buildIndexerSettingsJSON encodes the request's egress and categories into SettingsJSON, or "" when
// both are unset, so an indexer nobody configured still reads back with SettingsJSON == "".
func buildIndexerSettingsJSON(request IndexerRequest) string {
	if request.EgressProxyURL == "" && len(request.MusicCategories) == 0 {
		return ""
	}
	encoded, err := json.Marshal(indexerSettingsJSON{
		Egress: IndexerEgress{ProxyURL: request.EgressProxyURL}, MusicCategories: request.MusicCategories,
	})
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
	// MusicCategories empty means DefaultMusicCategories.
	MusicCategories []int
}

// defaultIndexerPriority is Lidarr's default, what a request without one gets.
const defaultIndexerPriority = 25

// withDefaults fills what a request may leave out.
func (request IndexerRequest) withDefaults() IndexerRequest {
	if request.Priority == 0 {
		request.Priority = defaultIndexerPriority
	}
	return request
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
	// Lidarr's range: lower is preferred when the same release is on several indexers.
	if request.Priority < 1 || request.Priority > 50 {
		problems = append(problems, config.Problem{Path: "priority", Value: fmt.Sprint(request.Priority),
			Expectation: "1 to 50, lower preferred"})
	}
	if len(request.MusicCategories) > maxMusicCategories {
		problems = append(problems, config.Problem{Path: "music_categories", Value: fmt.Sprint(len(request.MusicCategories)),
			Expectation: fmt.Sprintf("at most %d categories", maxMusicCategories)})
	}
	for _, category := range request.MusicCategories {
		if category < 1 || category > 999_999 {
			problems = append(problems, config.Problem{Path: "music_categories", Value: fmt.Sprint(category),
				Expectation: "Newznab category numbers, such as 3000 (Audio) or 3040 (Audio/Lossless)"})
		}
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
	if problems := validateIndexer(request.withDefaults()); len(problems) > 0 {
		return Indexer{}, problems
	}
	sealed, err := c.sealOptional(apiKey)
	if err != nil {
		return Indexer{}, fmt.Errorf("settings: seal indexer api key: %w", err)
	}

	settingsJSON := buildIndexerSettingsJSON(request)
	var created settingsdb.Indexer
	err = c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		created, err = c.queries.WithTx(tx).CreateIndexer(ctx, settingsdb.CreateIndexerParams{
			Name: request.Name, Impl: request.Impl, BaseUrl: request.BaseURL, ApiKey: sealed,
			Enabled: boolToInt(request.Enabled), Priority: request.withDefaults().Priority,
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
	if problems := validateIndexer(request.withDefaults()); len(problems) > 0 {
		return Indexer{}, problems
	}

	settingsJSON := buildIndexerSettingsJSON(request)
	err := c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return c.queries.WithTx(tx).UpdateIndexer(ctx, settingsdb.UpdateIndexerParams{
			Name: request.Name, Impl: request.Impl, BaseUrl: request.BaseURL,
			Enabled: boolToInt(request.Enabled), Priority: request.withDefaults().Priority,
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
