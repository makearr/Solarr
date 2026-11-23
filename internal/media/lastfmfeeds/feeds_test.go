package lastfmfeeds_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/lastfmfeeds"
	"github.com/makearr/Solarr/internal/metadata/lastfm"
	"github.com/makearr/Solarr/internal/platform/clock"
)

type charts struct {
	artists   []lastfm.ChartArtist
	tagAlbums map[string][]lastfm.ChartAlbum
	similar   map[string][]lastfm.SimilarArtist
	topAlbums map[string][]lastfm.ChartAlbum
	asked     []string
	err       error
}

func (c *charts) TopArtists(_ context.Context, page, _ int) ([]lastfm.ChartArtist, bool, error) {
	c.asked = append(c.asked, "top-artists")
	return c.artists, page < 2, c.err
}
func (c *charts) TagTopAlbums(_ context.Context, tag string, _, _ int) ([]lastfm.ChartAlbum, bool, error) {
	c.asked = append(c.asked, "tag:"+tag)
	return c.tagAlbums[tag], false, c.err
}
func (c *charts) Similar(_ context.Context, artist string, _ int) ([]lastfm.SimilarArtist, error) {
	c.asked = append(c.asked, "similar:"+artist)
	return c.similar[artist], c.err
}
func (c *charts) ArtistTopAlbums(_ context.Context, artist string, _ int) ([]lastfm.ChartAlbum, error) {
	return c.topAlbums[artist], c.err
}

type names struct {
	artists map[string]string
	albums  map[string]album
}

type album struct {
	id   string
	year int
}

func (n names) Artist(_ context.Context, name string) (string, bool, error) {
	id, found := n.artists[name]
	return id, found, nil
}
func (n names) Album(_ context.Context, artist, title string) (string, int, bool, error) {
	found, ok := n.albums[artist+"/"+title]
	return found.id, found.year, ok, nil
}

type taste struct{ artists, genres []string }

func (t taste) Artists(context.Context, int64, int) ([]string, error) { return t.artists, nil }
func (t taste) Genres(context.Context, int64, int) ([]string, error)  { return t.genres, nil }

func feeds(c *charts, n names, t taste, configured bool) lastfmfeeds.Feeds {
	return lastfmfeeds.Feeds{
		Charts: c, Names: n, Taste: t, ArtistProvider: "artist-id", AlbumProvider: "group-id",
		Clock: clock.NewFake(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)), Configured: func() bool { return configured },
	}
}

func feedOf(kind string, listType media.RecommendationType) media.RecommendationFeed {
	return media.RecommendationFeed{Type: listType, Kind: kind, Source: "Last.fm"}
}

func TestNoKeyMakesEveryFeedUnavailableWithTheReason(t *testing.T) {
	usable, unavailable := feeds(&charts{}, names{}, taste{}, false).RecommendationFeeds(t.Context())
	if len(usable) != 0 || unavailable[feedOf("album", media.RecommendTrending)] != lastfmfeeds.NoKeyReason {
		t.Errorf("usable %v, unavailable %v", usable, unavailable)
	}
}

func TestUpcomingIsNeverUsableAndNoFeedClaimsARating(t *testing.T) {
	usable, unavailable := feeds(&charts{}, names{}, taste{}, true).RecommendationFeeds(t.Context())
	for _, one := range usable {
		if one.Type == media.RecommendUpcoming || one.HasRating {
			t.Errorf("usable feed %+v", one)
		}
	}
	if unavailable[feedOf("album", media.RecommendUpcoming)] != lastfmfeeds.NoCalendarNote || len(usable) != 5 {
		t.Errorf("usable %d, unavailable %v", len(usable), unavailable)
	}
}

func TestTrendingArtistsResolveByNameAndDropWhatNothingResolves(t *testing.T) {
	c := &charts{artists: []lastfm.ChartArtist{{Name: "Ada Quill", MBID: "m-1", Listeners: 900}, {Name: "Bo Rend", Listeners: 500}, {Name: "Cy Fenn", Listeners: 100}}}
	got, more, err := feeds(c, names{artists: map[string]string{"Bo Rend": "m-2"}}, taste{}, true).Recommend(t.Context(),
		media.RecommendQuery{Feed: feedOf("artist", media.RecommendTrending), Page: 1})
	want := []media.Recommendation{
		{Kind: "artist", Provider: "artist-id", ExternalID: "m-1", Title: "Ada Quill", Popularity: 900, Votes: 900},
		{Kind: "artist", Provider: "artist-id", ExternalID: "m-2", Title: "Bo Rend", Popularity: 500, Votes: 500},
	}
	if err != nil || !more || !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v more=%v err=%v", got, more, err)
	}
}

func TestSimilarArtistsFollowOneHeldArtistPerPage(t *testing.T) {
	c := &charts{similar: map[string][]lastfm.SimilarArtist{"Ada Quill": {{Name: "Bo Rend", MBID: "m-2", Match: 0.9}}}}
	f := feeds(c, names{}, taste{artists: []string{"Ada Quill", "Dee Lorn"}}, true)
	got, more, err := f.Recommend(t.Context(), media.RecommendQuery{Feed: feedOf("artist", media.RecommendHighestRated), Page: 1})
	want := []media.Recommendation{{Kind: "artist", Provider: "artist-id", ExternalID: "m-2", Title: "Bo Rend", Subtitle: "Similar to Ada Quill", Popularity: 900}}
	if err != nil || !more || !reflect.DeepEqual(got, want) {
		t.Errorf("page 1: got %+v more=%v err=%v", got, more, err)
	}
	if _, more, _ := f.Recommend(t.Context(), media.RecommendQuery{Feed: feedOf("artist", media.RecommendHighestRated), Page: 2}); more {
		t.Error("the last seed's page said more")
	}
	if got, more, _ := f.Recommend(t.Context(), media.RecommendQuery{Feed: feedOf("artist", media.RecommendHighestRated), Page: 3}); len(got) != 0 || more {
		t.Errorf("past the seeds: %+v %v", got, more)
	}
}

func TestAlbumsOfTheLibrarysGenresRotateAndClassicsKeepOnlyOldOnes(t *testing.T) {
	c := &charts{tagAlbums: map[string][]lastfm.ChartAlbum{
		"folk": {{Name: "Old Maps", Artist: "Ada Quill"}, {Name: "New Maps", Artist: "Bo Rend"}, {Name: "Lost", Artist: "Nobody"}},
		"jazz": {{Name: "Blue Hour", Artist: "Cy Fenn"}},
	}}
	n := names{albums: map[string]album{
		"Ada Quill/Old Maps": {id: "g-1", year: 1990}, "Bo Rend/New Maps": {id: "g-2", year: 2024}, "Cy Fenn/Blue Hour": {id: "g-3", year: 1970},
	}}
	f := feeds(c, n, taste{genres: []string{"folk", "jazz"}}, true)
	trending, _, err := f.Recommend(t.Context(), media.RecommendQuery{Feed: feedOf("album", media.RecommendTrending), Page: 1})
	if err != nil || len(trending) != 2 || trending[0].ExternalID != "g-1" || trending[0].Traits.Genres[0] != "folk" || trending[0].Subtitle != "Ada Quill" {
		t.Errorf("trending page 1: %+v %v", trending, err)
	}
	classics, _, _ := f.Recommend(t.Context(), media.RecommendQuery{Feed: feedOf("album", media.RecommendClassics), Page: 1, ClassicAgeYears: 25})
	if len(classics) != 1 || classics[0].ExternalID != "g-1" {
		t.Errorf("classics page 1: %+v", classics)
	}
	second, _, _ := f.Recommend(t.Context(), media.RecommendQuery{Feed: feedOf("album", media.RecommendTrending), Page: 2})
	if len(second) != 1 || second[0].ExternalID != "g-3" || c.asked[len(c.asked)-1] != "tag:jazz" {
		t.Errorf("trending page 2: %+v asked %v", second, c.asked)
	}
}

func TestSimilarAlbumsTakeTheTopAlbumOfEachAlikeArtist(t *testing.T) {
	c := &charts{
		similar:   map[string][]lastfm.SimilarArtist{"Ada Quill": {{Name: "Bo Rend", Match: 0.8}, {Name: "Cy Fenn", Match: 0.4}}},
		topAlbums: map[string][]lastfm.ChartAlbum{"Bo Rend": {{Name: "New Maps"}}, "Cy Fenn": {{Name: "Blue Hour"}}},
	}
	n := names{albums: map[string]album{"Bo Rend/New Maps": {id: "g-2", year: 2024}}}
	got, _, err := feeds(c, n, taste{artists: []string{"Ada Quill"}}, true).Recommend(t.Context(), media.RecommendQuery{Feed: feedOf("album", media.RecommendHighestRated), Page: 1})
	want := []media.Recommendation{{Kind: "album", Provider: "group-id", ExternalID: "g-2", Title: "New Maps", Subtitle: "Bo Rend", Year: 2024, Popularity: 800}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v err=%v", got, err)
	}
}

func TestAKeyThatVanishedIsASourceUnavailableAndAFeedItDoesNotHaveIsNotWired(t *testing.T) {
	f := feeds(&charts{err: lastfm.ErrNoKey}, names{}, taste{}, true)
	if _, _, err := f.Recommend(t.Context(), media.RecommendQuery{Feed: feedOf("artist", media.RecommendTrending), Page: 1}); !errors.Is(err, media.ErrSourceUnavailable) {
		t.Errorf("no key: err = %v", err)
	}
	if _, _, err := f.Recommend(t.Context(), media.RecommendQuery{Feed: feedOf("album", media.RecommendUpcoming), Page: 1}); !errors.Is(err, media.ErrNotWired) {
		t.Errorf("upcoming: err = %v", err)
	}
}
