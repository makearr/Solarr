package settings_test

import (
	"errors"
	"testing"

	"github.com/makearr/MediaNexus/internal/db/settings"
	"github.com/makearr/MediaNexus/internal/platform/secrets"
)

func TestCreateIndexer_RoundTripsThroughSealAndOpen(t *testing.T) {
	cache, fake := newCache(t)
	cipher, err := secrets.New(testCipherKey())
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	cache.EnableSecrets(cipher)

	created, err := cache.CreateIndexer(t.Context(), settings.IndexerRequest{
		Name: "NZBgeek", Impl: "newznab", BaseURL: "https://api.nzbgeek.info", Enabled: true, Priority: 25,
	}, secrets.Secret("a-real-api-key"), fake.Now().Unix())
	if err != nil {
		t.Fatalf("CreateIndexer: %v", err)
	}
	if created.Name != "NZBgeek" || created.Impl != "newznab" || !created.HasAPIKey {
		t.Errorf("created = %+v, want name=NZBgeek impl=newznab HasAPIKey=true", created)
	}

	opened, err := cache.DecryptedIndexerAPIKey(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("DecryptedIndexerAPIKey: %v", err)
	}
	if opened.Reveal() != "a-real-api-key" {
		t.Errorf("DecryptedIndexerAPIKey = %q, want the original key back", opened.Reveal())
	}
}

// Unlike ArrInstance, an indexer's own api_key is nullable -- a public
// Torznab endpoint needs none at all, and creating one must not be refused
// for lacking a key, even with no cipher configured.
func TestCreateIndexer_AllowsNoAPIKeyEvenWithoutSecretsEnabled(t *testing.T) {
	cache, fake := newCache(t)
	// EnableSecrets deliberately not called.

	created, err := cache.CreateIndexer(t.Context(), settings.IndexerRequest{
		Name: "Public Torznab", Impl: "torznab", BaseURL: "https://example.org/torznab", Enabled: true,
	}, secrets.Secret(""), fake.Now().Unix())
	if err != nil {
		t.Fatalf("CreateIndexer: %v", err)
	}
	if created.HasAPIKey {
		t.Error("HasAPIKey = true, want false: no key was given")
	}

	opened, err := cache.DecryptedIndexerAPIKey(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("DecryptedIndexerAPIKey: %v", err)
	}
	if !opened.IsZero() {
		t.Errorf("DecryptedIndexerAPIKey = %q, want empty: no key was ever stored", opened.Reveal())
	}
}

func TestCreateIndexer_RefusesARealKeyWithoutSecretsEnabled(t *testing.T) {
	cache, fake := newCache(t)
	// EnableSecrets deliberately not called.

	_, err := cache.CreateIndexer(t.Context(), settings.IndexerRequest{
		Name: "NZBgeek", Impl: "newznab", BaseURL: "https://api.nzbgeek.info", Enabled: true,
	}, secrets.Secret("a-real-api-key"), fake.Now().Unix())
	if !errors.Is(err, settings.ErrSecretsUnavailable) {
		t.Errorf("err = %v, want ErrSecretsUnavailable", err)
	}
}

func TestCreateIndexer_RefusesAnEmptyName(t *testing.T) {
	cache, fake := newCache(t)

	_, err := cache.CreateIndexer(t.Context(), settings.IndexerRequest{
		Name: "  ", Impl: "newznab", BaseURL: "https://api.nzbgeek.info", Enabled: true,
	}, secrets.Secret(""), fake.Now().Unix())
	if err == nil {
		t.Fatal("CreateIndexer succeeded with a blank name")
	}
}

func TestSetIndexerAPIKey_ReplacesTheSealedValue(t *testing.T) {
	cache, fake := newCache(t)
	cipher, err := secrets.New(testCipherKey())
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	cache.EnableSecrets(cipher)

	created, err := cache.CreateIndexer(t.Context(), settings.IndexerRequest{
		Name: "NZBgeek", Impl: "newznab", BaseURL: "https://api.nzbgeek.info", Enabled: true,
	}, secrets.Secret("old-key"), fake.Now().Unix())
	if err != nil {
		t.Fatalf("CreateIndexer: %v", err)
	}

	if err := cache.SetIndexerAPIKey(t.Context(), created.ID, secrets.Secret("new-key"), fake.Now().Unix()); err != nil {
		t.Fatalf("SetIndexerAPIKey: %v", err)
	}
	opened, err := cache.DecryptedIndexerAPIKey(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("DecryptedIndexerAPIKey: %v", err)
	}
	if opened.Reveal() != "new-key" {
		t.Errorf("DecryptedIndexerAPIKey = %q, want %q", opened.Reveal(), "new-key")
	}
}

func TestSetIndexerCaps_RecordsCategoriesAndFetchTime(t *testing.T) {
	cache, fake := newCache(t)

	created, err := cache.CreateIndexer(t.Context(), settings.IndexerRequest{
		Name: "NZBgeek", Impl: "newznab", BaseURL: "https://api.nzbgeek.info", Enabled: true,
	}, secrets.Secret(""), fake.Now().Unix())
	if err != nil {
		t.Fatalf("CreateIndexer: %v", err)
	}
	if created.CapsFetchedAt != nil {
		t.Errorf("CapsFetchedAt = %v, want nil before any caps fetch", created.CapsFetchedAt)
	}

	at := fake.Now().Unix()
	if err := cache.SetIndexerCaps(t.Context(), created.ID, `{"3000":"Audio"}`, at); err != nil {
		t.Fatalf("SetIndexerCaps: %v", err)
	}

	found, err := cache.GetIndexer(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("GetIndexer: %v", err)
	}
	if found.CategoriesJSON != `{"3000":"Audio"}` {
		t.Errorf("CategoriesJSON = %q, want the caps response recorded", found.CategoriesJSON)
	}
	if found.CapsFetchedAt == nil || *found.CapsFetchedAt != at {
		t.Errorf("CapsFetchedAt = %v, want %d", found.CapsFetchedAt, at)
	}
}

func TestUpdateIndexer_ChangesFactsNeverTheKey(t *testing.T) {
	cache, fake := newCache(t)
	cipher, err := secrets.New(testCipherKey())
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	cache.EnableSecrets(cipher)

	created, err := cache.CreateIndexer(t.Context(), settings.IndexerRequest{
		Name: "NZBgeek", Impl: "newznab", BaseURL: "https://api.nzbgeek.info", Enabled: true, Priority: 50,
	}, secrets.Secret("the-key"), fake.Now().Unix())
	if err != nil {
		t.Fatalf("CreateIndexer: %v", err)
	}

	updated, err := cache.UpdateIndexer(t.Context(), created.ID, settings.IndexerRequest{
		Name: "NZBgeek Renamed", Impl: "newznab", BaseURL: "https://api.nzbgeek.info", Enabled: false, Priority: 10,
	}, fake.Now().Unix())
	if err != nil {
		t.Fatalf("UpdateIndexer: %v", err)
	}
	if updated.Name != "NZBgeek Renamed" || updated.Enabled || updated.Priority != 10 {
		t.Errorf("updated = %+v, want the new name/enabled/priority", updated)
	}

	opened, err := cache.DecryptedIndexerAPIKey(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("DecryptedIndexerAPIKey: %v", err)
	}
	if opened.Reveal() != "the-key" {
		t.Errorf("DecryptedIndexerAPIKey = %q, want the original key untouched by UpdateIndexer", opened.Reveal())
	}
}

func TestDeleteIndexer_RemovesIt(t *testing.T) {
	cache, fake := newCache(t)

	created, err := cache.CreateIndexer(t.Context(), settings.IndexerRequest{
		Name: "NZBgeek", Impl: "newznab", BaseURL: "https://api.nzbgeek.info", Enabled: true,
	}, secrets.Secret(""), fake.Now().Unix())
	if err != nil {
		t.Fatalf("CreateIndexer: %v", err)
	}

	if err := cache.DeleteIndexer(t.Context(), created.ID); err != nil {
		t.Fatalf("DeleteIndexer: %v", err)
	}
	if _, err := cache.GetIndexer(t.Context(), created.ID); err == nil {
		t.Error("GetIndexer succeeded after delete, want an error")
	}
}

func TestIndexers_ListsInPriorityThenNameOrder(t *testing.T) {
	cache, fake := newCache(t)

	if _, err := cache.CreateIndexer(t.Context(), settings.IndexerRequest{
		Name: "Second", Impl: "newznab", BaseURL: "https://b.example", Enabled: true, Priority: 50,
	}, secrets.Secret(""), fake.Now().Unix()); err != nil {
		t.Fatalf("CreateIndexer: %v", err)
	}
	if _, err := cache.CreateIndexer(t.Context(), settings.IndexerRequest{
		Name: "First", Impl: "newznab", BaseURL: "https://a.example", Enabled: true, Priority: 10,
	}, secrets.Secret(""), fake.Now().Unix()); err != nil {
		t.Fatalf("CreateIndexer: %v", err)
	}

	indexers, err := cache.Indexers(t.Context())
	if err != nil {
		t.Fatalf("Indexers: %v", err)
	}
	if len(indexers) != 2 || indexers[0].Name != "First" || indexers[1].Name != "Second" {
		t.Errorf("Indexers() = %+v, want First (priority 10) before Second (priority 50)", indexers)
	}
}
