package discover_test

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/service/discover"
)

func ageOf(certification string) (int64, bool) {
	switch certification {
	case "G":
		return 0, true
	case "PG-13":
		return 13, true
	case "R":
		return 17, true
	}
	return 0, false
}

func film() media.Recommendation {
	return media.Recommendation{
		Kind: "movie", Provider: "tmdb", ExternalID: "1", Title: "Example Film", Rating: 8.2, Votes: 5000, Networks: []string{"Example Studio"},
		Traits: media.Traits{Genres: []string{"Drama", "Comedy"}, OriginalLanguage: "en", Rating: "PG-13"},
	}
}

func TestAdmitOneRulePerReason(t *testing.T) {
	open := discover.Rules{Rating: discover.RatingLimit{MaxAge: -1, AgeOf: ageOf}}
	cases := []struct {
		name  string
		rules discover.Rules
		facts discover.Facts
		item  func(*media.Recommendation)
		want  discover.Decision
	}{
		{"everything fine", open, discover.Facts{}, nil, discover.Decision{Admitted: true}},
		{"held and monitored", open, discover.Facts{Monitored: true}, nil, discover.Decision{Rejections: []discover.Rejection{{Reason: discover.ReasonHeldMonitored}}}},
		{"in the exclusion list", open, discover.Facts{Excluded: true}, nil, discover.Decision{Rejections: []discover.Rejection{{Reason: discover.ReasonExcluded}}}},
		{"above the maximum rating", discover.Rules{Rating: discover.RatingLimit{MaxAge: 12, AgeOf: ageOf}}, discover.Facts{}, nil,
			discover.Decision{Rejections: []discover.Rejection{{Reason: discover.ReasonAboveRating, Value: "PG-13"}}}},
		{"unrated refused when the library refuses unrated", discover.Rules{Rating: discover.RatingLimit{MaxAge: 12, AgeOf: ageOf}}, discover.Facts{},
			func(r *media.Recommendation) { r.Traits.Rating = "" },
			discover.Decision{Rejections: []discover.Rejection{{Reason: discover.ReasonAboveRating}}}},
		{"unrated admitted when allowed", discover.Rules{Rating: discover.RatingLimit{MaxAge: 12, AllowUnrated: true, AgeOf: ageOf}}, discover.Facts{},
			func(r *media.Recommendation) { r.Traits.Rating = "" }, discover.Decision{Admitted: true}},
		{"excluded genre, any case", discover.Rules{ExcludeGenres: []string{"comedy"}, Rating: open.Rating}, discover.Facts{}, nil,
			discover.Decision{Rejections: []discover.Rejection{{Reason: discover.ReasonGenreExcluded, Value: "Comedy"}}}},
		{"excluded network", discover.Rules{ExcludeNetworks: []string{"Example Studio"}, Rating: open.Rating}, discover.Facts{}, nil,
			discover.Decision{Rejections: []discover.Rejection{{Reason: discover.ReasonNetworkExcluded, Value: "Example Studio"}}}},
		{"language not wanted", discover.Rules{OriginalLanguages: []string{"ja", "ko"}, Rating: open.Rating}, discover.Facts{}, nil,
			discover.Decision{Rejections: []discover.Rejection{{Reason: discover.ReasonLanguageUnwanted, Value: "en"}}}},
		{"a wanted language", discover.Rules{OriginalLanguages: []string{"EN"}, Rating: open.Rating}, discover.Facts{}, nil, discover.Decision{Admitted: true}},
		{"another library fits better", open, discover.Facts{OtherLibrary: true}, nil, discover.Decision{Rejections: []discover.Rejection{{Reason: discover.ReasonOtherLibrary}}}},
	}
	for _, c := range cases {
		item := film()
		if c.item != nil {
			c.item(&item)
		}
		if got := discover.Admit(item, c.rules, c.facts); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestAdmitReportsEveryReasonAtOnce(t *testing.T) {
	rules := discover.Rules{
		ExcludeGenres: discover.Union([]string{"Drama"}, []string{"drama", "Comedy"}), ExcludeNetworks: []string{"Example Studio"},
		OriginalLanguages: []string{"ja"}, Rating: discover.RatingLimit{MaxAge: 0, AgeOf: ageOf},
	}
	got := discover.Admit(film(), rules, discover.Facts{Monitored: true, Excluded: true, OtherLibrary: true})
	want := discover.Decision{Rejections: []discover.Rejection{
		{Reason: discover.ReasonHeldMonitored}, {Reason: discover.ReasonExcluded}, {Reason: discover.ReasonAboveRating, Value: "PG-13"},
		{Reason: discover.ReasonGenreExcluded, Value: "Drama"}, {Reason: discover.ReasonGenreExcluded, Value: "Comedy"},
		{Reason: discover.ReasonNetworkExcluded, Value: "Example Studio"}, {Reason: discover.ReasonLanguageUnwanted, Value: "en"}, {Reason: discover.ReasonOtherLibrary},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("decision = %+v\nwant %+v", got, want)
	}
}

func TestUnionJoinsGlobalAndLibraryExclusionsOnce(t *testing.T) {
	got := discover.Union([]string{"Reality", " Talk "}, nil, []string{"reality", "News", ""})
	if want := []string{"Reality", "Talk", "News"}; !reflect.DeepEqual(got, want) {
		t.Errorf("union = %v, want %v", got, want)
	}
}

func TestAutoAdmitGates(t *testing.T) {
	gates := discover.AutoAddGates{MinRating: 7.5, MinVotes: 1000, PerDay: 3}
	cases := []struct {
		name  string
		item  func(*media.Recommendation)
		facts discover.AutoAddFacts
		want  discover.Decision
	}{
		{"passes", nil, discover.AutoAddFacts{HasRating: true}, discover.Decision{Admitted: true}},
		{"below rating", func(r *media.Recommendation) { r.Rating = 7.4 }, discover.AutoAddFacts{HasRating: true},
			discover.Decision{Rejections: []discover.Rejection{{Reason: discover.ReasonBelowRating, Value: "7.4"}}}},
		{"a feed without ratings skips the rating gate", func(r *media.Recommendation) { r.Rating = 0 }, discover.AutoAddFacts{}, discover.Decision{Admitted: true}},
		{"below votes", func(r *media.Recommendation) { r.Votes = 999 }, discover.AutoAddFacts{HasRating: true},
			discover.Decision{Rejections: []discover.Rejection{{Reason: discover.ReasonBelowVotes, Value: "999"}}}},
		{"added before", nil, discover.AutoAddFacts{HasRating: true, AddedBefore: true}, discover.Decision{Rejections: []discover.Rejection{{Reason: discover.ReasonAlreadyAutoAdded}}}},
		{"daily ceiling reached", nil, discover.AutoAddFacts{HasRating: true, AddedToday: 3}, discover.Decision{Rejections: []discover.Rejection{{Reason: discover.ReasonDailyCap, Value: "3"}}}},
		{"two short", func(r *media.Recommendation) { r.Rating, r.Votes = 5, 1 }, discover.AutoAddFacts{HasRating: true},
			discover.Decision{Rejections: []discover.Rejection{{Reason: discover.ReasonBelowRating, Value: "5.0"}, {Reason: discover.ReasonBelowVotes, Value: "1"}}}},
	}
	for _, c := range cases {
		item := film()
		if c.item != nil {
			c.item(&item)
		}
		if got := discover.AutoAdmit(item, gates, c.facts); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
	if got := discover.AutoAdmit(film(), discover.AutoAddGates{MinRating: 7, MinVotes: 1}, discover.AutoAddFacts{HasRating: true, AddedToday: 99}); !got.Admitted {
		t.Errorf("no ceiling: %+v", got)
	}
}
