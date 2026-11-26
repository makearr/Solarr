package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// recommendationChunk bounds one transaction (200 rows).
const recommendationChunk = 200

// PoolItem is one recommendation as a refresh stores it.
type PoolItem struct {
	Kind, Provider, ExternalID, Title string
	Subtitle, Overview, PosterURL     string
	Year                              int64
	ReleasedOn                        string
	Popularity, Rating                float64
	Votes                             int64
	OriginalLanguage, Certification   string
	Networks, Genres                  []string
	// WorkID is the work already held for it (unmonitored), 0 when it is new.
	WorkID int64
}

// PoolRecommendation is a stored recommendation with the lists that name it.
type PoolRecommendation struct {
	ID, CategoryID int64
	PoolItem
	FetchedAt int64
	// Types are the recommendation lists that name it.
	Types []string
}

// FeedRef names one source's read of one list of one library: a recommendation type for one add kind.
type FeedRef struct {
	CategoryID int64
	Kind, Type string
	// Source is the provider key of the source that reads the list.
	Source string
}

// FeedState is how far a feed was read and how its last read ended.
type FeedState struct {
	FeedRef
	PagesRead   int64
	Exhausted   bool
	RefreshedAt int64
	// PopularityMax is the raw popularity a stored 100 stands for, so a later page scales like the first.
	PopularityMax float64
	LastError     string
}

// RecommendationCursor is the keyset position after the last row of a page.
type RecommendationCursor struct {
	Popularity float64
	ID         int64
}

// RecommendationQuery asks one page of the pool for one person; empty Kind, Type, Genre or Title do not narrow.
type RecommendationQuery struct {
	CategoryIDs              []int64
	Kind, Type, Genre, Title string
	UserID                   int64
	After                    *RecommendationCursor
	Limit                    int64
}

// RecommendationDismissal is one thing a person is not interested in.
type RecommendationDismissal struct {
	UserID                                       int64
	MediaType, Kind, Provider, ExternalID, Title string
	CreatedAt                                    int64
}

// AutoAddRecord is what auto-add added, kept so a removed work is never added again.
type AutoAddRecord struct {
	CategoryID                        int64
	Kind, Provider, ExternalID, Title string
	Type                              string
	Rating                            float64
	Votes, WorkID, AddedAt            int64
}

// Recommendations keeps Discover's pool, feed state, dismissals and auto-add record.
type Recommendations struct{ store *store.Store }

// NewRecommendations wraps an opened library database.
func NewRecommendations(opened *store.Store) Recommendations { return Recommendations{store: opened} }

// ReplaceFeed makes the items the whole of one feed: rows another read left behind go, but an item another feed still names stays.
func (r Recommendations) ReplaceFeed(ctx context.Context, ref FeedRef, items []PoolItem, state FeedState, at int64) error {
	ids, err := r.upsert(ctx, ref, items, 0, at)
	if err != nil {
		return err
	}
	keep, err := json.Marshal(ids)
	if err != nil {
		return fmt.Errorf("library: feed ids: %w", err)
	}
	err = r.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		if err := queries.DeleteStaleRecommendationFeedItems(ctx, librarydb.DeleteStaleRecommendationFeedItemsParams{
			Type: ref.Type, Source: ref.Source, CategoryID: ref.CategoryID, Kind: ref.Kind, KeepIds: string(keep),
		}); err != nil {
			return err
		}
		if err := queries.DeleteOrphanRecommendations(ctx, librarydb.DeleteOrphanRecommendationsParams{CategoryID: ref.CategoryID, Kind: ref.Kind}); err != nil {
			return err
		}
		return setFeedState(ctx, queries, ref, state)
	})
	if err != nil {
		return fmt.Errorf("library: replace feed: %w", err)
	}
	return nil
}

// AppendFeed adds the next page of a feed after the ranks it already holds (Load more).
func (r Recommendations) AppendFeed(ctx context.Context, ref FeedRef, items []PoolItem, firstRank int64, state FeedState, at int64) error {
	if _, err := r.upsert(ctx, ref, items, firstRank, at); err != nil {
		return err
	}
	return r.SetFeedState(ctx, ref, state)
}

func (r Recommendations) upsert(ctx context.Context, ref FeedRef, items []PoolItem, firstRank, at int64) ([]int64, error) {
	ids := make([]int64, 0, len(items))
	for start := 0; start < len(items); start += recommendationChunk {
		chunk := items[start:min(start+recommendationChunk, len(items))]
		err := r.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			queries := librarydb.New(tx)
			for offset, item := range chunk {
				networks, err := encodeStrings(item.Networks)
				if err != nil {
					return err
				}
				id, err := queries.UpsertRecommendation(ctx, librarydb.UpsertRecommendationParams{
					CategoryID: ref.CategoryID, Kind: item.Kind, Provider: item.Provider, ExternalID: item.ExternalID, Title: item.Title, Subtitle: item.Subtitle,
					Overview: item.Overview, PosterUrl: item.PosterURL, Year: item.Year, ReleasedOn: item.ReleasedOn, Popularity: item.Popularity, Rating: item.Rating,
					Votes: item.Votes, OriginalLanguage: item.OriginalLanguage, Certification: item.Certification, NetworksJson: networks, WorkID: item.WorkID, FetchedAt: at,
				})
				if err != nil {
					return err
				}
				if err := queries.DeleteRecommendationGenres(ctx, id); err != nil {
					return err
				}
				for _, genre := range item.Genres {
					if err := queries.AddRecommendationGenre(ctx, librarydb.AddRecommendationGenreParams{RecommendationID: id, Genre: genre}); err != nil {
						return err
					}
				}
				if err := queries.UpsertRecommendationFeedItem(ctx, librarydb.UpsertRecommendationFeedItemParams{
					RecommendationID: id, Type: ref.Type, Source: ref.Source, Rank: firstRank + int64(start+offset),
				}); err != nil {
					return err
				}
				ids = append(ids, id)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("library: store recommendations: %w", err)
		}
	}
	return ids, nil
}

func encodeStrings(values []string) (string, error) {
	if len(values) == 0 {
		return "", nil
	}
	raw, err := json.Marshal(values)
	return string(raw), err
}

func decodeStrings(raw string) []string {
	if raw == "" {
		return nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil
	}
	return values
}

func setFeedState(ctx context.Context, queries *librarydb.Queries, ref FeedRef, state FeedState) error {
	exhausted := int64(0)
	if state.Exhausted {
		exhausted = 1
	}
	return queries.SetRecommendationFeed(ctx, librarydb.SetRecommendationFeedParams{
		CategoryID: ref.CategoryID, Kind: ref.Kind, Type: ref.Type, Source: ref.Source, PagesRead: state.PagesRead, Exhausted: exhausted, RefreshedAt: state.RefreshedAt,
		PopularityMax: state.PopularityMax, LastError: state.LastError,
	})
}

// SetFeedState records how far a feed was read, also when the read failed.
func (r Recommendations) SetFeedState(ctx context.Context, ref FeedRef, state FeedState) error {
	err := r.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return setFeedState(ctx, librarydb.New(tx), ref, state)
	})
	if err != nil {
		return fmt.Errorf("library: set recommendation feed: %w", err)
	}
	return nil
}

// FeedState answers how far one feed was read; the zero state for a feed never read.
func (r Recommendations) FeedState(ctx context.Context, ref FeedRef) (FeedState, error) {
	row, err := librarydb.New(r.store.Read()).GetRecommendationFeed(ctx, librarydb.GetRecommendationFeedParams{
		CategoryID: ref.CategoryID, Kind: ref.Kind, Type: ref.Type, Source: ref.Source,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return FeedState{FeedRef: ref}, nil
	}
	if err != nil {
		return FeedState{}, fmt.Errorf("library: recommendation feed: %w", err)
	}
	return feedStateOf(row), nil
}

func feedStateOf(row librarydb.RecommendationFeed) FeedState {
	return FeedState{
		FeedRef: FeedRef{CategoryID: row.CategoryID, Kind: row.Kind, Type: row.Type, Source: row.Source}, PagesRead: row.PagesRead, Exhausted: row.Exhausted == 1,
		RefreshedAt: row.RefreshedAt, PopularityMax: row.PopularityMax, LastError: row.LastError,
	}
}

// FeedStates answers every feed of a library that was read.
func (r Recommendations) FeedStates(ctx context.Context, categoryID int64) ([]FeedState, error) {
	rows, err := librarydb.New(r.store.Read()).ListRecommendationFeeds(ctx, categoryID)
	if err != nil {
		return nil, fmt.Errorf("library: recommendation feeds: %w", err)
	}
	states := make([]FeedState, len(rows))
	for index, row := range rows {
		states[index] = feedStateOf(row)
	}
	return states, nil
}

// List answers a page of the pool for one person, most popular first, without what they dismissed or already monitor.
func (r Recommendations) List(ctx context.Context, query RecommendationQuery) ([]PoolRecommendation, error) {
	ids, err := json.Marshal(append([]int64{}, query.CategoryIDs...))
	if err != nil {
		return nil, fmt.Errorf("library: recommendation libraries: %w", err)
	}
	params := librarydb.ListRecommendationsParams{
		CategoryIds: string(ids), Kind: query.Kind, Type: query.Type, Genre: query.Genre, Title: query.Title, HasCursor: int64(0),
		UserID: query.UserID, PageSize: query.Limit,
	}
	if query.After != nil {
		params.HasCursor, params.AfterPopularity, params.AfterID = int64(1), query.After.Popularity, query.After.ID
	}
	queries := librarydb.New(r.store.Read())
	rows, err := queries.ListRecommendations(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("library: list recommendations: %w", err)
	}
	found := make([]int64, len(rows))
	for index, row := range rows {
		found[index] = row.ID
	}
	rawIDs, err := json.Marshal(found)
	if err != nil {
		return nil, fmt.Errorf("library: recommendation ids: %w", err)
	}
	genres, err := queries.ListRecommendationGenresOf(ctx, string(rawIDs))
	if err != nil {
		return nil, fmt.Errorf("library: recommendation genres: %w", err)
	}
	types, err := queries.ListRecommendationFeedTypesOf(ctx, string(rawIDs))
	if err != nil {
		return nil, fmt.Errorf("library: recommendation types: %w", err)
	}
	genresOf, typesOf := map[int64][]string{}, map[int64][]string{}
	for _, row := range genres {
		genresOf[row.RecommendationID] = append(genresOf[row.RecommendationID], row.Genre)
	}
	for _, row := range types {
		typesOf[row.RecommendationID] = append(typesOf[row.RecommendationID], row.Type)
	}
	out := make([]PoolRecommendation, len(rows))
	for index, row := range rows {
		out[index] = PoolRecommendation{
			ID: row.ID, CategoryID: row.CategoryID, FetchedAt: row.FetchedAt, Types: typesOf[row.ID],
			PoolItem: PoolItem{
				Kind: row.Kind, Provider: row.Provider, ExternalID: row.ExternalID, Title: row.Title, Subtitle: row.Subtitle, Overview: row.Overview,
				PosterURL: row.PosterUrl, Year: row.Year, ReleasedOn: row.ReleasedOn, Popularity: row.Popularity, Rating: row.Rating, Votes: row.Votes,
				OriginalLanguage: row.OriginalLanguage, Certification: row.Certification, Networks: decodeStrings(row.NetworksJson), Genres: genresOf[row.ID], WorkID: row.WorkID,
			},
		}
	}
	return out, nil
}

// GenreCount is one genre of a library's pool and how many recommendations carry it.
type GenreCount struct {
	Genre string
	Items int64
}

// Genres answers the genre facet of one library and kind, most carried first.
func (r Recommendations) Genres(ctx context.Context, categoryID int64, kind string) ([]GenreCount, error) {
	rows, err := librarydb.New(r.store.Read()).ListRecommendationGenreCounts(ctx, librarydb.ListRecommendationGenreCountsParams{CategoryID: categoryID, Kind: kind})
	if err != nil {
		return nil, fmt.Errorf("library: recommendation genres: %w", err)
	}
	counts := make([]GenreCount, len(rows))
	for index, row := range rows {
		counts[index] = GenreCount{Genre: row.Genre, Items: row.Items}
	}
	return counts, nil
}

// Dismiss hides a recommendation for one person; the same item in every library of theirs.
func (r Recommendations) Dismiss(ctx context.Context, dismissal RecommendationDismissal) error {
	err := r.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).DismissRecommendation(ctx, librarydb.DismissRecommendationParams{
			UserID: dismissal.UserID, MediaType: dismissal.MediaType, Kind: dismissal.Kind, Provider: dismissal.Provider,
			ExternalID: dismissal.ExternalID, Title: dismissal.Title, CreatedAt: dismissal.CreatedAt,
		})
	})
	if err != nil {
		return fmt.Errorf("library: dismiss recommendation: %w", err)
	}
	return nil
}

// IgnoreHeld ignores a held work, or a held person for a media type, under every provider id it has; ignored false undoes it.
func (r Recommendations) IgnoreHeld(ctx context.Context, userID int64, person bool, id int64, mediaType string, ignored bool, at int64) error {
	err := r.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		switch {
		case person && ignored:
			return queries.IgnoreHeldPerson(ctx, librarydb.IgnoreHeldPersonParams{UserID: userID, MediaType: mediaType, CreatedAt: at, PersonID: id})
		case person:
			return queries.UnignoreHeldPerson(ctx, librarydb.UnignoreHeldPersonParams{UserID: userID, MediaType: mediaType, PersonID: id})
		case ignored:
			return queries.IgnoreHeldWork(ctx, librarydb.IgnoreHeldWorkParams{UserID: userID, CreatedAt: at, WorkID: id})
		}
		return queries.UnignoreHeldWork(ctx, librarydb.UnignoreHeldWorkParams{UserID: userID, WorkID: id})
	})
	if err != nil {
		return fmt.Errorf("library: ignore held: %w", err)
	}
	return nil
}

// Restore shows a dismissed recommendation to the person again.
func (r Recommendations) Restore(ctx context.Context, userID int64, kind, provider, externalID string) error {
	err := r.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).RestoreRecommendation(ctx, librarydb.RestoreRecommendationParams{UserID: userID, Provider: provider, ExternalID: externalID, Kind: kind})
	})
	if err != nil {
		return fmt.Errorf("library: restore recommendation: %w", err)
	}
	return nil
}

// IgnoredKeys answers what the person ignores of a media type, and what an administrator ignores for everyone, each as
// "provider:external id".
func (r Recommendations) IgnoredKeys(ctx context.Context, userID int64, mediaType string) (map[string]bool, error) {
	rows, err := librarydb.New(r.store.Read()).ListIgnoredKeys(ctx, librarydb.ListIgnoredKeysParams{UserID: userID, MediaType: mediaType})
	if err != nil {
		return nil, fmt.Errorf("library: ignored keys: %w", err)
	}
	keys := make(map[string]bool, len(rows))
	for _, row := range rows {
		keys[row.Provider+":"+row.ExternalID] = true
	}
	return keys, nil
}

// Dismissals answers what one person is not interested in, newest first.
func (r Recommendations) Dismissals(ctx context.Context, userID int64) ([]RecommendationDismissal, error) {
	rows, err := librarydb.New(r.store.Read()).ListRecommendationDismissals(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("library: recommendation dismissals: %w", err)
	}
	out := make([]RecommendationDismissal, len(rows))
	for index, row := range rows {
		out[index] = RecommendationDismissal{
			UserID: row.UserID, MediaType: row.MediaType, Kind: row.Kind, Provider: row.Provider, ExternalID: row.ExternalID, Title: row.Title, CreatedAt: row.CreatedAt,
		}
	}
	return out, nil
}

// RecordAutoAdd remembers what auto-add added; one already recorded keeps its first record.
func (r Recommendations) RecordAutoAdd(ctx context.Context, record AutoAddRecord) error {
	err := r.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).RecordRecommendationAutoAdd(ctx, librarydb.RecordRecommendationAutoAddParams{
			CategoryID: record.CategoryID, Kind: record.Kind, Provider: record.Provider, ExternalID: record.ExternalID, Title: record.Title, Type: record.Type,
			Rating: record.Rating, Votes: record.Votes, WorkID: record.WorkID, AddedAt: record.AddedAt,
		})
	})
	if err != nil {
		return fmt.Errorf("library: record auto-add: %w", err)
	}
	return nil
}

// AutoAddedSince counts what auto-add added to a library since the time, for its daily ceiling.
func (r Recommendations) AutoAddedSince(ctx context.Context, categoryID, since int64) (int64, error) {
	count, err := librarydb.New(r.store.Read()).CountRecommendationAutoAddsSince(ctx, librarydb.CountRecommendationAutoAddsSinceParams{CategoryID: categoryID, AddedAt: since})
	if err != nil {
		return 0, fmt.Errorf("library: count auto-adds: %w", err)
	}
	return count, nil
}

// WasAutoAdded is whether auto-add ever added the item to the library.
func (r Recommendations) WasAutoAdded(ctx context.Context, categoryID int64, kind, provider, externalID string) (bool, error) {
	count, err := librarydb.New(r.store.Read()).WasRecommendationAutoAdded(ctx, librarydb.WasRecommendationAutoAddedParams{
		CategoryID: categoryID, Kind: kind, Provider: provider, ExternalID: externalID,
	})
	if err != nil {
		return false, fmt.Errorf("library: was auto-added: %w", err)
	}
	return count > 0, nil
}

// ForgetCategory drops everything Discover holds for a library that was deleted.
func (r Recommendations) ForgetCategory(ctx context.Context, categoryID int64) error {
	err := r.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		if err := queries.DeleteRecommendationsOfCategory(ctx, categoryID); err != nil {
			return err
		}
		if err := queries.DeleteRecommendationFeedsOfCategory(ctx, categoryID); err != nil {
			return err
		}
		return queries.DeleteRecommendationAutoAddsOfCategory(ctx, categoryID)
	})
	if err != nil {
		return fmt.Errorf("library: forget recommendations: %w", err)
	}
	return nil
}

// ForgetExternalID drops a recommendation from every library's pool, as a work added by any path is no longer one.
func (r Recommendations) ForgetExternalID(ctx context.Context, provider, externalID string) error {
	err := r.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).DeleteRecommendationsByExternalID(ctx, librarydb.DeleteRecommendationsByExternalIDParams{Provider: provider, ExternalID: externalID})
	})
	if err != nil {
		return fmt.Errorf("library: forget recommendation: %w", err)
	}
	return nil
}

// Held answers the work a provider id names in the library and whether any of its items is monitored; 0 for none.
func (r Recommendations) Held(ctx context.Context, provider, externalID string) (workID int64, monitored bool, err error) {
	row, err := librarydb.New(r.store.Read()).GetHeldWorkByExternalID(ctx, librarydb.GetHeldWorkByExternalIDParams{Provider: provider, ExternalID: externalID})
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("library: held work: %w", err)
	}
	return row.WorkID, row.Monitored == 1, nil
}

// CollectionCandidate is a member of a collection the library holds a part of, not wanted yet.
type CollectionCandidate struct {
	Collection, Title, Overview, OriginalLanguage, Provider, ExternalID string
	WorkID, Year                                                        int64
}

// CollectionCandidates answers up to limit unmonitored members of the collections a library holds a member of.
func (r Recommendations) CollectionCandidates(ctx context.Context, categoryID int64, mediaType string, limit int64) ([]CollectionCandidate, error) {
	rows, err := librarydb.New(r.store.Read()).ListCollectionRecommendationCandidates(ctx, librarydb.ListCollectionRecommendationCandidatesParams{
		MediaType: mediaType, CategoryID: categoryID, PageSize: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("library: collection candidates: %w", err)
	}
	out := make([]CollectionCandidate, 0, len(rows))
	for _, row := range rows {
		out = append(out, CollectionCandidate{
			Collection: row.CollectionName, Title: row.CanonicalTitle, Overview: row.Description, OriginalLanguage: row.OriginalLanguage, Provider: row.Provider, ExternalID: row.ExternalID,
			WorkID: row.WorkID, Year: row.Year,
		})
	}
	return out, nil
}

// Item answers one pool row by its id, false when it left the pool.
func (r Recommendations) Item(ctx context.Context, id int64) (PoolRecommendation, bool, error) {
	row, err := librarydb.New(r.store.Read()).GetRecommendation(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return PoolRecommendation{}, false, nil
	}
	if err != nil {
		return PoolRecommendation{}, false, fmt.Errorf("library: recommendation: %w", err)
	}
	return PoolRecommendation{
		ID: row.ID, CategoryID: row.CategoryID, FetchedAt: row.FetchedAt,
		PoolItem: PoolItem{
			Kind: row.Kind, Provider: row.Provider, ExternalID: row.ExternalID, Title: row.Title, Subtitle: row.Subtitle, Overview: row.Overview,
			PosterURL: row.PosterUrl, Year: row.Year, Popularity: row.Popularity, Rating: row.Rating, Votes: row.Votes, WorkID: row.WorkID,
		},
	}, true, nil
}

// TopHeldArtists are the names of a library's artists with the most works held, most first.
func (r Recommendations) TopHeldArtists(ctx context.Context, categoryID, limit int64) ([]string, error) {
	rows, err := librarydb.New(r.store.Read()).ListTopHeldArtistsOfCategory(ctx, librarydb.ListTopHeldArtistsOfCategoryParams{CategoryID: categoryID, PageSize: limit})
	if err != nil {
		return nil, fmt.Errorf("library: top held artists: %w", err)
	}
	names := make([]string, len(rows))
	for index, row := range rows {
		names[index] = row.Name
	}
	return names, nil
}

// TopGenres are the genres of the most works a library holds, most first.
func (r Recommendations) TopGenres(ctx context.Context, categoryID, limit int64) ([]string, error) {
	rows, err := librarydb.New(r.store.Read()).ListTopGenresOfCategory(ctx, librarydb.ListTopGenresOfCategoryParams{CategoryID: categoryID, PageSize: limit})
	if err != nil {
		return nil, fmt.Errorf("library: top genres: %w", err)
	}
	names := make([]string, len(rows))
	for index, row := range rows {
		names[index] = row.Name
	}
	return names, nil
}
