package parsebook_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/identify/parsebook"
)

func TestParsePath_AFolderBelowAnAuthorIsATitleWithItsSubtitle(t *testing.T) {
	got, ok := parsebook.ParsePath("Mara Quell/The Lantern Keeper - A Novel/01.mp3", true)
	want := parsebook.Info{Author: "Mara Quell", Title: "The Lantern Keeper", Titles: []string{"The Lantern Keeper", "The Lantern Keeper - A Novel", "A Novel"}, Formats: []string{"mp3"}}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestParse_ATitleThatStartsWithANumberKeepsItAsAReading(t *testing.T) {
	got, ok := parsebook.Parse("Mara Quell - 9 from the Lantern Worlds.epub")
	if !ok || got.Title != "from the Lantern Worlds" || !slices.Contains(got.Titles, "9 from the Lantern Worlds") {
		t.Errorf("got %+v", got)
	}
}

func TestParse_ReadsTheShapesReleasesUse(t *testing.T) {
	cases := []struct {
		name string
		want parsebook.Info
	}{
		{"Mara Quell - The Lantern Keeper (1987) [EPUB]", parsebook.Info{
			Author: "Mara Quell", Title: "The Lantern Keeper", Titles: []string{"The Lantern Keeper"}, Year: 1987, Formats: []string{"epub"}}},
		{"The Lantern Keeper by Mara Quell (Retail) (azw3)", parsebook.Info{
			Author: "Mara Quell", Title: "The Lantern Keeper", Titles: []string{"The Lantern Keeper"}, Formats: []string{"azw3"}, Retail: true}},
		{"Mara.Quell.The.Lantern.Keeper.2015.Unabridged.M4B.64k-GROUP", parsebook.Info{
			Title: "Mara Quell The Lantern Keeper", Titles: []string{"Mara Quell The Lantern Keeper"}, Year: 2015, Formats: []string{"m4b"},
			BitrateKbps: 64, Unabridged: true, ReleaseGroup: "GROUP"}},
		{"Mara Quell - The Lantern Cycle 02 - Ember Roads.epub", parsebook.Info{
			Author: "Mara Quell", Title: "Ember Roads", Titles: []string{"Ember Roads", "The Lantern Cycle 02 - Ember Roads"}, Formats: []string{"epub"}}},
		{"Mara Quell - The Lantern Keeper - A Novel.epub", parsebook.Info{
			Author: "Mara Quell", Title: "A Novel", Titles: []string{"A Novel", "The Lantern Keeper - A Novel", "The Lantern Keeper"}, Formats: []string{"epub"}}},
		{"Mara Quell - Night Ferries: A Novel - 9780000000002 [PDF]", parsebook.Info{
			Author: "Mara Quell", Title: "Night Ferries: A Novel", Titles: []string{"Night Ferries: A Novel", "Night Ferries"},
			Formats: []string{"pdf"}, ISBN: "9780000000002"}},
	}
	for _, c := range cases {
		got, ok := parsebook.Parse(c.name)
		if !ok {
			t.Errorf("%q did not parse", c.name)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q\n got %+v\nwant %+v", c.name, got, c.want)
		}
	}
}

func TestParsePath_AnAudiobookIsItsFolderAndItsAuthorTheOneAbove(t *testing.T) {
	got, ok := parsebook.ParsePath("Mara Quell/The Lantern Keeper (2015)/01 - Chapter One.mp3", true)
	if !ok || got.Author != "Mara Quell" || got.Title != "The Lantern Keeper" || got.Year != 2015 || got.Formats[0] != "mp3" {
		t.Errorf("got %+v", got)
	}
	got, ok = parsebook.ParsePath("Mara Quell/The Lantern Keeper/Mara Quell - The Lantern Keeper.epub", false)
	if !ok || got.Author != "Mara Quell" || got.Title != "The Lantern Keeper" {
		t.Errorf("got %+v", got)
	}
	got, _ = parsebook.ParsePath("Mara Quell/Ember Roads.epub", false)
	if got.Author != "Mara Quell" || got.Title != "Ember Roads" {
		t.Errorf("a file without an author takes its folder's: %+v", got)
	}
}

func TestNames_HoldsANameAgainstTheWantedBook(t *testing.T) {
	authors := []string{"Mara Quell"}
	cases := []struct {
		name string
		want bool
	}{
		{"Mara Quell - The Lantern Keeper (1987) [EPUB]", true},
		{"Quell, Mara - The Lantern Keeper", true},
		{"Mara.Quell.The.Lantern.Keeper.2015.Unabridged.M4B-GROUP", true},
		{"The Lantern Keeper: A Novel by M. Quell", true},
		{"Tobin Reyes - The Lantern Keeper", false},
		{"Mara Quell - The Lantern Keepers Daughter", false},
		{"Mara Quell - Ember Roads", false},
	}
	for _, c := range cases {
		if got := parsebook.Names(c.name, "The Lantern Keeper", authors); got != c.want {
			t.Errorf("Names(%q) = %v, want %v", c.name, got, c.want)
		}
	}
	if !parsebook.Names("Night Ferries [epub]", "Night Ferries: A Novel", nil) {
		t.Error("a title before its subtitle, with no author known, should name the book")
	}
}
