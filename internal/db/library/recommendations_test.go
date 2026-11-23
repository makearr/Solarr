package library_test

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
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
