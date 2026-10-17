package library_test

import (
	"fmt"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

type crossoverFixture struct {
	t          *testing.T
	series     library.Series
	desired    library.Desired
	crossovers library.Crossovers
}

func newCrossoverFixture(t *testing.T) crossoverFixture {
	opened, _ := newLibrary(t)
	return crossoverFixture{t: t, series: library.NewSeries(opened), desired: library.NewDesired(opened), crossovers: library.NewCrossovers(opened)}
}

func minutes(m int64) *int64 { v := m * 60_000; return &v }

// show adds a series of the parts, each with the people credited on it.
func (f crossoverFixture) show(title, id, network string, parts map[string]crossoverPart) {
	write := sampleSeries()
	write.Work.CanonicalTitle, write.Work.SortTitle = title, title
	write.IDs = []library.ExternalIDWrite{{Provider: "tvdb", ExternalID: id}}
	write.Network = network
	write.Parts = nil
	write.Credits = nil
	for ref, part := range parts {
		write.Parts = append(write.Parts, library.SeriesPartWrite{MediaRef: ref, SortKey: ref, Title: part.title, RuntimeMs: minutes(part.minutes), Date: "2016-03-31", MonitoredIfNew: true})
		for _, person := range part.people {
			write.Credits = append(write.Credits, library.SeriesCreditWrite{
				Provider: "tmdb", ExternalID: person, Name: "Person " + person, Role: "guest_star", PartRef: ref,
			})
		}
	}
	result, err := f.series.Upsert(f.t.Context(), seriesAt, write)
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.desired.EnsureSeasonItem(f.t.Context(), library.SeasonRequest{WorkID: result.WorkID, MediaRef: "s01", SortKey: "s01", CategoryID: 1, ProfileID: 1, Monitored: true}); err != nil {
		f.t.Fatal(err)
	}
}

type crossoverPart struct {
	title   string
	minutes int64
	people  []string
}

func cast(n int, skip ...int) []string {
	var out []string
next:
	for i := 1; i <= n; i++ {
		for _, s := range skip {
			if s == i {
				continue next
			}
		}
		out = append(out, fmt.Sprint("p", i))
	}
	return out
}

func TestCrossoverEpisodesAreFoundByCastLengthAndAnEpisodeTitle(t *testing.T) {
	f := newCrossoverFixture(t)
	f.show("Show A", "1", "Channel One", map[string]crossoverPart{
		"s01:e01": {"Pilot", 45, cast(5)},
		"s01:e02": {"The Big Crossover", 44, cast(5)},
		"s01:e03": {"Long Night", 40, cast(5)},
		"s01:e04": {"Same Name Different Cast", 45, cast(5)},
		"s01:e05": {"No Cast Data", 45, nil},
	})
	f.show("Show B", "2", "Channel Two", map[string]crossoverPart{
		"s01:e01": {"Pilot", 45, cast(5)},
		"s01:e02": {"The Big Crossover", 45, cast(5, 5)},
		"s01:e03": {"Long Night", 95, cast(5)},
		"s01:e04": {"Same Name Different Cast", 45, []string{"x1", "x2", "x3", "x4", "x5"}},
		"s01:e05": {"No Cast Data", 45, nil},
	})

	// Two series of one network that list one specific title on one day, of one length: the same broadcast, cast data or not.
	f.show("Docs", "3", "Channel One", map[string]crossoverPart{"s01:e01": {"A Long Night On The Moon", 59, nil}})
	f.show("Sunrise", "4", "Channel One", map[string]crossoverPart{"s01:e01": {"A Long Night On The Moon", 60, nil}})

	found, err := f.crossovers.Suggestions(t.Context(), "tv", 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	titles := map[string]bool{}
	for _, suggestion := range found {
		titles[suggestion.Title] = suggestion.Certain()
	}
	if len(titles) != 3 || !titles["The Big Crossover"] || titles["No Cast Data"] || !titles["A Long Night On The Moon"] {
		t.Errorf("suggestions = %v; want the crossover and the same-network one certain, the one with no cast data only a suggestion, not a pilot, not a 40 and a 95 minute episode, not casts that share no one", titles)
	}

	linked, err := f.crossovers.LinkCertain(t.Context(), "tv", 1)
	if err != nil || linked != 2 {
		t.Fatalf("linked %d, %v; want the two pairs the evidence settles", linked, err)
	}
	after, _ := f.crossovers.Suggestions(t.Context(), "tv", 1, 50)
	if len(after) != 1 || after[0].Title != "No Cast Data" {
		t.Fatalf("after linking: %+v", after)
	}

	// A person who unlinks it is believed: detection does not link the pair again.
	pair := found[0]
	for _, suggestion := range found {
		if suggestion.Certain() {
			pair = suggestion
		}
	}
	if err := f.crossovers.UnlinkSameContent(t.Context(), pair.PartID, pair.OtherPartID); err != nil {
		t.Fatal(err)
	}
	if again, err := f.crossovers.LinkCertain(t.Context(), "tv", 1); err != nil || again != 0 {
		t.Errorf("an unlinked pair was linked again: %d, %v", again, err)
	}
}
