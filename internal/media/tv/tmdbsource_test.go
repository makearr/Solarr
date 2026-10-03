package tv_test

import (
	"context"
	"testing"

	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/metadata/skyhook"
	"github.com/makearr/Solarr/internal/metadata/tmdb"
)

func sampleTMDB() tmdb.Series {
	var series tmdb.Series
	series.ID, series.Name, series.OriginalName, series.Overview = 7, "Example Show", "Beispiel", "Richer plot."
	series.FirstAirDate, series.Status, series.OriginCountry = "2020-01-05", "Returning Series", []string{"US"}
	series.Genres = []tmdb.Genre{{ID: 1, Name: "Drama"}, {ID: 2, Name: "Crime"}}
	series.Networks = []tmdb.Network{{ID: 9, Name: "Example Net", OriginCountry: "US", LogoPath: "/n.png"}, {ID: 10, Name: "Stream Co"}}
	series.CreatedBy = []tmdb.Creator{{ID: 5, Name: "A Creator"}}
	series.PosterPath, series.BackdropPath = "/p.jpg", "/b.jpg"
	series.ExternalIDs.TVDB, series.ExternalIDs.IMDb = 1234, "tt0000042"
	series.AggregateCredits.Cast = []tmdb.CastMember{{ID: 11, Name: "A Person", Order: 0, TotalEpisodeCount: 8, Roles: []tmdb.Role{{Character: "A Role", EpisodeCount: 8}}}}
	series.AggregateCredits.Crew = []tmdb.CrewMember{
		{ID: 12, Name: "B Person", Jobs: []tmdb.Job{{Job: "Executive Producer"}, {Job: "Gaffer"}}},
	}
	series.ContentRatings.Results = []struct {
		Country string `json:"iso_3166_1"`
		Rating  string `json:"rating"`
	}{{Country: "US", Rating: "TV-14"}}
	series.Seasons = []tmdb.SeasonSummary{{SeasonNumber: 1, PosterPath: "/s1.jpg"}}
	return series
}

func sampleTMDBSeason() tmdb.Season {
	return tmdb.Season{SeasonNumber: 1, Episodes: []tmdb.Episode{{
		SeasonNumber: 1, EpisodeNumber: 1, AirDate: "2020-01-05",
		GuestStars: []tmdb.GuestStar{{ID: 21, Name: "G Star", Character: "A Guest", Order: 500}},
		Crew:       []tmdb.EpisodeCrew{{ID: 22, Name: "D Rector", Job: "Director"}, {ID: 23, Name: "Cam Era", Job: "Camera Operator"}},
	}}}
}

func TestFromTMDBCarriesCreditsNetworksAndNoEpisodeList(t *testing.T) {
	series := tv.FromTMDB(sampleTMDB(), []tmdb.Season{sampleTMDBSeason()}, "")
	if len(series.Episodes) != 0 {
		t.Errorf("episodes = %d: TMDB's order is not the numbering basis", len(series.Episodes))
	}
	if series.IDs.TMDB != 7 || series.IDs.TVDB != 1234 || series.Status != tv.StatusContinuing || series.OriginalCountry != "us" || series.ContentRating != "TV-14" {
		t.Errorf("series = %+v", series)
	}
	roles := map[string]int{}
	for _, credit := range series.Credits {
		roles[credit.Role]++
	}
	want := map[string]int{tv.RoleCreator: 1, tv.RoleActor: 1, tv.RoleExecutiveProducer: 1, tv.RoleGuestStar: 1, tv.RoleDirector: 1}
	for role, count := range want {
		if roles[role] != count {
			t.Errorf("%s credits = %d, want %d (all: %v)", role, roles[role], count, roles)
		}
	}
	if len(series.Credits) != 5 || len(series.Networks) != 2 || series.Network != "Example Net" {
		t.Errorf("credits = %d, networks = %+v: jobs outside the vocabulary are not stored", len(series.Credits), series.Networks)
	}
}

type fixedSkyhook struct{ series skyhook.Series }

func (f fixedSkyhook) Series(context.Context, int64) (skyhook.Series, error) { return f.series, nil }
func (f fixedSkyhook) Search(context.Context, string) ([]skyhook.Series, error) {
	return nil, nil
}

type fakeTMDB struct {
	series  tmdb.Series
	seasons []tmdb.Season
}

func (f fakeTMDB) Configured() bool { return true }
func (f fakeTMDB) Series(context.Context, int64) (tmdb.Series, error) {
	return f.series, nil
}
func (f fakeTMDB) Season(_ context.Context, _ int64, number int) (tmdb.Season, error) {
	for _, season := range f.seasons {
		if season.SeasonNumber == number {
			return season, nil
		}
	}
	return tmdb.Season{}, tmdb.ErrNotFound
}
func (f fakeTMDB) FindByTVDB(context.Context, int64) (tmdb.Hit, error) {
	return tmdb.Hit{ID: f.series.ID}, nil
}
func (f fakeTMDB) FindByIMDb(context.Context, string) (tmdb.Hit, error) {
	return tmdb.Hit{}, tmdb.ErrNotFound
}

func skyhookShow() skyhook.Series {
	return skyhook.Series{TVDBID: 1234, Title: "Example Show (US)", Overview: "Plain plot.", Genres: []string{"Drama"}, FirstAired: "2020-01-05",
		Episodes: []skyhook.Episode{{SeasonNumber: 1, EpisodeNumber: 1, Title: "Pilot", AirDate: "2020-01-05"}, {SeasonNumber: 1, EpisodeNumber: 2, Title: "Two", AirDate: "2020-01-12"}}}
}

func TestFetchKeepsTheNumberingBasisIdentityAndTakesTMDBForTheRest(t *testing.T) {
	season := sampleTMDBSeason()
	season.Episodes = append(season.Episodes, tmdb.Episode{SeasonNumber: 1, EpisodeNumber: 2, AirDate: "2019-12-31",
		GuestStars: []tmdb.GuestStar{{ID: 31, Name: "Wrong Date", Character: "X"}}})
	season.Episodes = append(season.Episodes, tmdb.Episode{SeasonNumber: 1, EpisodeNumber: 9, GuestStars: []tmdb.GuestStar{{ID: 32, Name: "No Such Episode"}}})
	service := tv.Service{Skyhook: fixedSkyhook{skyhookShow()}, TMDB: fakeTMDB{series: sampleTMDB(), seasons: []tmdb.Season{season}}}

	series, err := service.Fetch(t.Context(), tv.IDs{TVDB: 1234})
	if err != nil {
		t.Fatal(err)
	}
	if series.Title != "Example Show (US)" || len(series.Episodes) != 2 || series.Episodes[0].Title != "Pilot" {
		t.Errorf("title = %q, episodes = %d: identity and numbering stay with Skyhook", series.Title, len(series.Episodes))
	}
	if series.Overview != "Richer plot." || len(series.Genres) != 2 || series.Images[0].Source != "tmdb" || series.IDs.TMDB != 7 {
		t.Errorf("overview = %q, genres = %v, images = %+v, ids = %+v", series.Overview, series.Genres, series.Images, series.IDs)
	}
	guests := 0
	for _, credit := range series.Credits {
		if credit.Role == tv.RoleGuestStar {
			guests++
			if credit.TMDBID != 21 {
				t.Errorf("guest star %d kept: its episode aired on another date or does not exist under the basis", credit.TMDBID)
			}
		}
	}
	if guests != 1 {
		t.Errorf("guest stars = %d, want only the one whose episode matches", guests)
	}
}

func TestFetchWithoutATMDBKeyStaysOnTheOtherSources(t *testing.T) {
	service := tv.Service{Skyhook: fixedSkyhook{skyhookShow()}}
	series, err := service.Fetch(t.Context(), tv.IDs{TVDB: 1234})
	if err != nil || series.Overview != "Plain plot." || len(series.Credits) != 0 {
		t.Errorf("series = %+v, err = %v", series, err)
	}
}

func TestFetchFromATMDBIDFindsTheTVDBIDForSkyhook(t *testing.T) {
	service := tv.Service{Skyhook: fixedSkyhook{skyhookShow()}, TMDB: fakeTMDB{series: sampleTMDB(), seasons: []tmdb.Season{sampleTMDBSeason()}}}
	series, err := service.Fetch(t.Context(), tv.IDs{TMDB: 7})
	if err != nil {
		t.Fatal(err)
	}
	if series.IDs.TVDB != 1234 || len(series.Episodes) != 2 || series.IDs.TMDB != 7 {
		t.Errorf("ids = %+v, episodes = %d: the TVDB id comes from TMDB's external ids and Skyhook then lists the episodes", series.IDs, len(series.Episodes))
	}
}
