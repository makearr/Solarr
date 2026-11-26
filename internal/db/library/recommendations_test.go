package library_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
)

func poolItem(id string, popularity float64) library.PoolItem {
	return library.PoolItem{Kind: "movie", Provider: "tmdb", ExternalID: id, Title: "Film " + id, Popularity: popularity, Genres: []string{"Drama"}}
}

func ids(rows []library.PoolRecommendation) []string {
	out := []string{}
	for _, row := range rows {
		out = append(out, row.ExternalID)
	}
	return out
}

func TestRecommendations_PagingByPopularityHasNoGapsOrRepeatsOnTies(t *testing.T) {
	opened, _ := newLibrary(t)
	pool := library.NewRecommendations(opened)
	ref := library.FeedRef{CategoryID: 1, Kind: "movie", Type: "trending"}
	items := []library.PoolItem{poolItem("a", 5), poolItem("b", 5), poolItem("c", 5), poolItem("d", 9), poolItem("e", 1)}
	if err := pool.ReplaceFeed(t.Context(), ref, items, library.FeedState{FeedRef: ref, PagesRead: 1}, 100); err != nil {
		t.Fatal(err)
	}
	query := library.RecommendationQuery{CategoryIDs: []int64{1}, UserID: 7, Limit: 2}
	var seen []string
	for range 10 {
		page, err := pool.List(t.Context(), query)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		seen = append(seen, ids(page)...)
		last := page[len(page)-1]
		query.After = &library.RecommendationCursor{Popularity: last.Popularity, ID: last.ID}
	}
	if want := []string{"d", "c", "b", "a", "e"}; !reflect.DeepEqual(seen, want) {
		t.Errorf("order = %v, want %v", seen, want)
	}
}

func TestRecommendations_DismissalHidesForOnePersonOnlyAndRestoreShowsItAgain(t *testing.T) {
	opened, _ := newLibrary(t)
	pool := library.NewRecommendations(opened)
	ref := library.FeedRef{CategoryID: 1, Kind: "movie", Type: "trending"}
	if err := pool.ReplaceFeed(t.Context(), ref, []library.PoolItem{poolItem("a", 2), poolItem("b", 1)}, library.FeedState{FeedRef: ref}, 100); err != nil {
		t.Fatal(err)
	}
	if err := pool.Dismiss(t.Context(), library.RecommendationDismissal{UserID: 7, MediaType: "movie", Kind: "movie", Provider: "tmdb", ExternalID: "a", Title: "Film a", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	listed := func(user int64) []string {
		rows, err := pool.List(t.Context(), library.RecommendationQuery{CategoryIDs: []int64{1}, UserID: user, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		return ids(rows)
	}
	if got := listed(7); !reflect.DeepEqual(got, []string{"b"}) {
		t.Errorf("dismisser sees %v", got)
	}
	if got := listed(8); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("another person sees %v", got)
	}
	if err := pool.Restore(t.Context(), 7, "movie", "tmdb", "a"); err != nil {
		t.Fatal(err)
	}
	if got := listed(7); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("after restore sees %v", got)
	}
}

func TestRecommendations_ReplacingAFeedDropsWhatItLostButKeepsWhatAnotherFeedNames(t *testing.T) {
	opened, _ := newLibrary(t)
	pool := library.NewRecommendations(opened)
	trending := library.FeedRef{CategoryID: 1, Kind: "movie", Type: "trending"}
	rated := library.FeedRef{CategoryID: 1, Kind: "movie", Type: "highest_rated"}
	if err := pool.ReplaceFeed(t.Context(), trending, []library.PoolItem{poolItem("a", 3), poolItem("b", 2)}, library.FeedState{FeedRef: trending}, 100); err != nil {
		t.Fatal(err)
	}
	if err := pool.ReplaceFeed(t.Context(), rated, []library.PoolItem{poolItem("b", 2)}, library.FeedState{FeedRef: rated}, 100); err != nil {
		t.Fatal(err)
	}
	if err := pool.ReplaceFeed(t.Context(), trending, []library.PoolItem{poolItem("c", 1)}, library.FeedState{FeedRef: trending}, 200); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.List(t.Context(), library.RecommendationQuery{CategoryIDs: []int64{1}, UserID: 1, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"b", "c"}; !reflect.DeepEqual(ids(rows), want) {
		t.Fatalf("pool = %v, want %v", ids(rows), want)
	}
	if want := []string{"highest_rated"}; !reflect.DeepEqual(rows[0].Types, want) {
		t.Errorf("b is named by %v, want %v", rows[0].Types, want)
	}
	only, err := pool.List(t.Context(), library.RecommendationQuery{CategoryIDs: []int64{1}, Type: "trending", UserID: 1, Limit: 10})
	if err != nil || !reflect.DeepEqual(ids(only), []string{"c"}) {
		t.Errorf("trending = %v, %v", ids(only), err)
	}
}

func TestRecommendations_TwoSourcesOfOneListKeepTheirOwnItemsAndStates(t *testing.T) {
	opened, _ := newLibrary(t)
	pool := library.NewRecommendations(opened)
	first := library.FeedRef{CategoryID: 1, Kind: "movie", Type: "trending", Source: "first"}
	second := library.FeedRef{CategoryID: 1, Kind: "movie", Type: "trending", Source: "second"}
	if err := pool.ReplaceFeed(t.Context(), first, []library.PoolItem{poolItem("a", 3), poolItem("b", 2)}, library.FeedState{FeedRef: first, PopularityMax: 50}, 100); err != nil {
		t.Fatal(err)
	}
	if err := pool.ReplaceFeed(t.Context(), second, []library.PoolItem{poolItem("b", 2), poolItem("c", 1)}, library.FeedState{FeedRef: second, PagesRead: 2}, 100); err != nil {
		t.Fatal(err)
	}
	if err := pool.ReplaceFeed(t.Context(), first, []library.PoolItem{poolItem("a", 3)}, library.FeedState{FeedRef: first, PopularityMax: 60}, 200); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.List(t.Context(), library.RecommendationQuery{CategoryIDs: []int64{1}, Type: "trending", UserID: 1, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "b", "c"}; !reflect.DeepEqual(ids(rows), want) {
		t.Fatalf("pool = %v, want %v", ids(rows), want)
	}
	if want := []string{"trending"}; !reflect.DeepEqual(rows[1].Types, want) {
		t.Errorf("b is named by %v, want %v", rows[1].Types, want)
	}
	states, err := pool.FeedStates(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	want := []library.FeedState{{FeedRef: first, PopularityMax: 60}, {FeedRef: second, PagesRead: 2}}
	if !reflect.DeepEqual(states, want) {
		t.Errorf("states = %+v, want %+v", states, want)
	}
}

func TestRecommendations_FourHundredFiftyItemsGoInChunksAndGenresFilter(t *testing.T) {
	opened, _ := newLibrary(t)
	pool := library.NewRecommendations(opened)
	ref := library.FeedRef{CategoryID: 2, Kind: "movie", Type: "classics"}
	items := make([]library.PoolItem, 450)
	for index := range items {
		items[index] = poolItem(fmt.Sprintf("m%03d", index), float64(index))
		if index%3 == 0 {
			items[index].Genres = []string{"Horror"}
		}
	}
	if err := pool.ReplaceFeed(t.Context(), ref, items, library.FeedState{FeedRef: ref, PagesRead: 3, Exhausted: true, RefreshedAt: 9}, 100); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.List(t.Context(), library.RecommendationQuery{CategoryIDs: []int64{2}, Genre: "Horror", UserID: 1, Limit: 500})
	if err != nil || len(rows) != 150 {
		t.Fatalf("horror rows = %d, %v", len(rows), err)
	}
	counts, err := pool.Genres(t.Context(), 2, "movie")
	if want := []library.GenreCount{{Genre: "Drama", Items: 300}, {Genre: "Horror", Items: 150}}; err != nil || !reflect.DeepEqual(counts, want) {
		t.Errorf("genres = %+v, %v", counts, err)
	}
	state, err := pool.FeedState(t.Context(), ref)
	if want := (library.FeedState{FeedRef: ref, PagesRead: 3, Exhausted: true, RefreshedAt: 9}); err != nil || state != want {
		t.Errorf("state = %+v, %v", state, err)
	}
	if err := pool.ForgetCategory(t.Context(), 2); err != nil {
		t.Fatal(err)
	}
	rows, _ = pool.List(t.Context(), library.RecommendationQuery{CategoryIDs: []int64{2}, UserID: 1, Limit: 10})
	if len(rows) != 0 {
		t.Errorf("after forget %d rows", len(rows))
	}
}

func TestRecommendations_AutoAddRecordCountsPerDayAndRemembers(t *testing.T) {
	opened, _ := newLibrary(t)
	pool := library.NewRecommendations(opened)
	record := library.AutoAddRecord{CategoryID: 1, Kind: "movie", Provider: "tmdb", ExternalID: "a", Title: "Film a", Type: "trending", Rating: 8, Votes: 900, AddedAt: 1000}
	later := record
	later.ExternalID, later.AddedAt = "b", 5000
	for _, one := range []library.AutoAddRecord{record, record, later} {
		if err := pool.RecordAutoAdd(t.Context(), one); err != nil {
			t.Fatal(err)
		}
	}
	if count, err := pool.AutoAddedSince(t.Context(), 1, 2000); err != nil || count != 1 {
		t.Errorf("since 2000 = %d, %v", count, err)
	}
	if count, err := pool.AutoAddedSince(t.Context(), 1, 0); err != nil || count != 2 {
		t.Errorf("since 0 = %d, %v", count, err)
	}
	if was, err := pool.WasAutoAdded(t.Context(), 1, "movie", "tmdb", "a"); err != nil || !was {
		t.Errorf("was added = %v, %v", was, err)
	}
	if was, _ := pool.WasAutoAdded(t.Context(), 2, "movie", "tmdb", "a"); was {
		t.Error("another library was told it was added")
	}
}

func TestRecommendations_HeldIsEmptyForAnUnknownProviderID(t *testing.T) {
	opened, _ := newLibrary(t)
	workID, monitored, err := library.NewRecommendations(opened).Held(t.Context(), "tmdb:movie", "404")
	if err != nil || workID != 0 || monitored {
		t.Errorf("held = %d, %v, %v", workID, monitored, err)
	}
}

func TestRecommendations_CollectionCandidatesAreUnwantedMembersOfCollectionsTheLibraryHolds(t *testing.T) {
	opened, _ := newLibrary(t)
	pool := library.NewRecommendations(opened)
	err := opened.Write(t.Context(), store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		for _, statement := range []string{
			`INSERT INTO work (id, media_type, canonical_title, sort_title, original_language, year, added_at, updated_at) VALUES
			  (1, 'movie', 'Saga One', 'saga one', 'en', 2001, 1, 1), (2, 'movie', 'Saga Two', 'saga two', 'en', 2003, 1, 1),
			  (3, 'movie', 'Saga Three', 'saga three', 'en', 2005, 1, 1), (4, 'movie', 'Lone', 'lone', 'en', 2005, 1, 1)`,
			`INSERT INTO work_external_id (provider, external_id, work_id, first_seen_at) VALUES ('tmdb', '1', 1, 1), ('tmdb', '2', 2, 1), ('tmdb', '3', 3, 1), ('tmdb', '4', 4, 1)`,
			`INSERT INTO collection (id, media_type, name, kind, source, added_at) VALUES (1, 'movie', 'Saga', 'franchise', 'tmdb', 1), (2, 'movie', 'Other', 'franchise', 'tmdb', 1)`,
			`INSERT INTO collection_external_id (provider, external_id, collection_id) VALUES ('tmdb', 'c1', 1), ('tmdb', 'c2', 2)`,
			`INSERT INTO collection_member (collection_id, work_id, ord) VALUES (1, 1, 1), (1, 2, 2), (1, 3, 3), (2, 4, 1)`,
			`INSERT INTO desired_item (work_id, category_id, profile_id, monitored, state) VALUES (1, 5, 1, 1, 'COMPLETE'), (3, 5, 1, 1, 'MISSING')`,
		} {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := pool.CollectionCandidates(t.Context(), 5, "movie", 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []library.CollectionCandidate{{Collection: "Saga", Title: "Saga Two", OriginalLanguage: "en", Provider: "tmdb", ExternalID: "2", WorkID: 2, Year: 2003}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("candidates = %+v, want %+v", got, want)
	}
}

func TestRecommendations_TasteIsTheMostHeldArtistsAndGenresOfOneLibrary(t *testing.T) {
	opened, _ := newLibrary(t)
	pool := library.NewRecommendations(opened)
	err := opened.Write(t.Context(), store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		for _, statement := range []string{
			`INSERT INTO work (id, media_type, canonical_title, sort_title, original_language, year, added_at, updated_at) VALUES
			  (1, 'music', 'One', 'one', '', 2001, 1, 1), (2, 'music', 'Two', 'two', '', 2003, 1, 1), (3, 'music', 'Three', 'three', '', 2005, 1, 1), (4, 'music', 'Elsewhere', 'elsewhere', '', 2005, 1, 1)`,
			`INSERT INTO person (id, name, sort_name, added_at) VALUES (1, 'Ada Quill', 'Quill, Ada', 1), (2, 'Bo Rend', 'Rend, Bo', 1)`,
			`INSERT INTO credit (person_id, work_id, role) VALUES (1, 1, 'artist'), (1, 2, 'artist'), (2, 3, 'artist'), (2, 4, 'artist')`,
			`INSERT INTO desired_item (work_id, category_id, profile_id, monitored, state) VALUES (1, 5, 1, 1, 'COMPLETE'), (2, 5, 1, 1, 'COMPLETE'), (3, 5, 1, 1, 'COMPLETE'), (4, 6, 1, 1, 'COMPLETE')`,
			`INSERT INTO media_object (id, size_bytes, quick_hash, first_seen_at) VALUES (1, 1, 'a', 1), (2, 1, 'b', 1), (3, 1, 'c', 1), (4, 1, 'd', 1)`,
			`INSERT INTO work_file (work_id, media_object_id, first_seen_at) VALUES (1, 1, 1), (2, 2, 1), (3, 3, 1), (4, 4, 1)`,
			`INSERT INTO tag (id, name, slug, source, genre_tree) VALUES (1, 'Folk', 'folk', 'test', 'music'), (2, 'Jazz', 'jazz', 'test', 'music'), (3, 'Owned', 'owned', 'test', '')`,
			`INSERT INTO tag_assignment (tag_id, subject_type, subject_id, source) VALUES (1, 'work', 1, 'test'), (1, 'work', 2, 'test'), (2, 'work', 3, 'test'), (3, 'work', 1, 'test'), (2, 'work', 4, 'test')`,
		} {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	artists, err := pool.TopHeldArtists(t.Context(), 5, 5)
	if want := []string{"Ada Quill", "Bo Rend"}; err != nil || !reflect.DeepEqual(artists, want) {
		t.Errorf("artists = %v, %v; want %v", artists, err, want)
	}
	genres, err := pool.TopGenres(t.Context(), 5, 5)
	if want := []string{"Folk", "Jazz"}; err != nil || !reflect.DeepEqual(genres, want) {
		t.Errorf("genres = %v, %v; want %v", genres, err, want)
	}
}

func TestRecommendations_IgnoreHeldWorkNamesEveryIDItHasAndUndoes(t *testing.T) {
	opened, _ := newLibrary(t)
	result, err := library.NewSeries(opened).Upsert(t.Context(), time.Unix(100, 0), library.SeriesWrite{
		Work: library.WorkIdentity{MediaType: "movie", CanonicalTitle: "Invented Film", SortTitle: "invented film"},
		IDs:  []library.ExternalIDWrite{{Provider: "tmdb", ExternalID: "11", Confidence: 1}, {Provider: "imdb", ExternalID: "tt11", Confidence: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	pool := library.NewRecommendations(opened)
	if _, err := pool.IgnoreHeld(t.Context(), 7, false, result.WorkID, "", true, 5); err != nil {
		t.Fatal(err)
	}
	keys, err := pool.IgnoredKeys(t.Context(), 7, "movie")
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]bool{"tmdb:11": true, "imdb:tt11": true}; !reflect.DeepEqual(keys, want) {
		t.Errorf("ignored = %v, want %v", keys, want)
	}
	if _, err := pool.IgnoreHeld(t.Context(), 7, false, result.WorkID, "", false, 6); err != nil {
		t.Fatal(err)
	}
	if keys, _ = pool.IgnoredKeys(t.Context(), 7, "movie"); len(keys) != 0 {
		t.Errorf("after undo = %v", keys)
	}
}

func TestRecommendations_StoppingAnIgnoreReportsWhetherTheWorkWasMonitoredAndStateListsIt(t *testing.T) {
	opened, _ := newLibrary(t)
	pool := library.NewRecommendations(opened)
	var ids [2]int64
	for index, monitored := range []int{1, 0} {
		result, err := library.NewSeries(opened).Upsert(t.Context(), time.Unix(100, 0), library.SeriesWrite{
			Work: library.WorkIdentity{MediaType: "movie", CanonicalTitle: fmt.Sprintf("Invented Film %d", index), SortTitle: fmt.Sprintf("invented film %d", index)},
			IDs:  []library.ExternalIDWrite{{Provider: "tmdb", ExternalID: fmt.Sprint(20 + index), Confidence: 1}},
		})
		if err != nil {
			t.Fatal(err)
		}
		ids[index] = result.WorkID
		err = opened.Write(t.Context(), store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO desired_item (work_id, category_id, profile_id, monitored, state) VALUES (?, 5, 1, ?, 'MISSING')`, result.WorkID, monitored)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.IgnoreHeld(t.Context(), 7, false, result.WorkID, "", true, 5); err != nil {
			t.Fatal(err)
		}
	}
	state, err := pool.IgnoredState(t.Context(), 7, "movie")
	if err != nil {
		t.Fatal(err)
	}
	if want := []library.IgnoredHeld{{ID: ids[0]}, {ID: ids[1]}}; !reflect.DeepEqual(state.Works, want) {
		t.Errorf("works = %v, want %v", state.Works, want)
	}
	for index, want := range []bool{true, false} {
		got, err := pool.IgnoreHeld(t.Context(), 7, false, ids[index], "", false, 6)
		if err != nil || got != want {
			t.Errorf("work %d: was monitored = %v, %v; want %v", index, got, err, want)
		}
	}
}
