package tv

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

func listed(provider, id, title string, held bool, episodes ...Episode) listedSeries {
	return listedSeries{provider: provider, externalID: id, title: title, held: held, episodes: episodes}
}

func aired(season, number int, title, date string) Episode {
	return Episode{Season: season, Number: number, Title: title, AirDate: date}
}

func TestFindCrossoverEventsJoinsTheSameTitleOfSeveralSeriesAndListsTheOnesTheLibraryLacks(t *testing.T) {
	family := []listedSeries{
		listed("tvdb", "1", "Invented Show", true, aired(1, 5, "Great Night (1)", "2018-12-09"), aired(1, 6, "A Quiet Hour", "2018-12-16")),
		listed("tvdb", "2", "Invented Spinoff", false, aired(2, 3, "Great Night (2)", "2018-12-10")),
		listed("tvdb", "3", "Invented Sequel", true, aired(1, 1, "Great Night", "2018-12-11")),
		listed("tvdb", "4", "Invented Other", true, aired(1, 1, "Great Night", "2020-01-01")),
	}
	events := findCrossoverEvents(family)
	want := []library.ProviderEvent{{Name: "Great Night", Parts: []library.ProviderEventPart{
		{Number: 1, Provider: "tvdb", ExternalID: "1", SeriesTitle: "Invented Show", Season: 1, Episode: 5, Title: "Great Night (1)", Date: "2018-12-09"},
		{Number: 2, Provider: "tvdb", ExternalID: "2", SeriesTitle: "Invented Spinoff", Season: 2, Episode: 3, Title: "Great Night (2)", Date: "2018-12-10"},
		{Provider: "tvdb", ExternalID: "3", SeriesTitle: "Invented Sequel", Season: 1, Episode: 1, Title: "Great Night", Date: "2018-12-11"},
	}}}
	if !reflect.DeepEqual(events, want) {
		t.Errorf("events = %+v; want %+v (the episode of 2020 is another broadcast)", events, want)
	}
}

func TestFindCrossoverEventsLeavesALoneBroadcastBothSidesHoldToTheSuggestions(t *testing.T) {
	both := []listedSeries{
		listed("tvdb", "1", "Invented Show", true, aired(1, 5, "A Night Out Together", "2018-12-09")),
		listed("tvdb", "2", "Invented Spinoff", true, aired(2, 3, "A Night Out Together", "2018-12-09")),
	}
	if events := findCrossoverEvents(both); len(events) != 0 {
		t.Errorf("events = %+v; want none, the library suggests the pair", events)
	}
	both[1].held = false
	if events := findCrossoverEvents(both); len(events) != 1 || len(events[0].Parts) != 2 {
		t.Errorf("events = %+v; want the pair, so the series the library lacks can be offered", events)
	}
}

func TestFindCrossoverEventsFindsAnEpisodeNamedForAnotherSeries(t *testing.T) {
	family := []listedSeries{
		listed("tvdb", "1", "Invented Quest", true, aired(2, 4, "The Fall of Another Hero", "2019-03-04")),
		listed("tvdb", "2", "Another Hero", true, aired(1, 6, "Meeting Invented Quest", "2019-03-06")),
	}
	events := findCrossoverEvents(family)
	if len(events) != 1 || events[0].Name != "The Fall of Another Hero" || len(events[0].Parts) != 2 || events[0].Parts[1].Title != "Meeting Invented Quest" {
		t.Errorf("events = %+v; want the two episodes that name each other's series as one event", events)
	}
}

func TestFindCrossoverEventsCountsTheEpisodeThatIntroducesASpinOff(t *testing.T) {
	introducing := aired(3, 20, "Backdoor Night", "2019-05-01")
	introducing.Overview = "The team meets the agents of Invented Spinoff."
	family := []listedSeries{
		listed("tvdb", "1", "Invented Show", true, introducing, aired(3, 21, "Later On", "2019-05-08")),
		listed("tvdb", "2", "Invented Spinoff", true, aired(1, 1, "Pilot", "2019-05-07"), aired(1, 2, "Second Case", "2019-05-14")),
		listed("tvdb", "3", "Unrelated Drama", true, aired(1, 1, "Pilot", "2019-05-07")),
	}
	events := findCrossoverEvents(family)
	if len(events) != 1 || events[0].Name != "Backdoor Night" || events[0].Parts[0].ExternalID != "1" || events[0].Parts[1].ExternalID != "2" || events[0].Parts[1].Title != "Pilot" {
		t.Errorf("events = %+v; want the introducing episode and the spin-off's pilot as one event", events)
	}
}

func TestFindCrossoverEventsIgnoresSpecialsAndGenericTitles(t *testing.T) {
	special := aired(0, 1, "Great Night (1)", "2018-12-09")
	special.Special = true
	family := []listedSeries{
		listed("tvdb", "1", "Invented Show", true, special, aired(1, 1, "Christmas Special", "2018-12-09")),
		listed("tvdb", "2", "Invented Spinoff", true, aired(1, 1, "Great Night (2)", "2018-12-10"), aired(1, 2, "Christmas Special", "2018-12-09")),
	}
	if events := findCrossoverEvents(family); len(events) != 0 {
		t.Errorf("events = %+v; want none", events)
	}
}
