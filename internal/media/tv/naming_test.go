package tv_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/moving/naming"
)

func render(t *testing.T, n tv.Naming, episode tv.EpisodeNaming, original string) string {
	t.Helper()
	rendered, _, err := n.RenderEpisode(episode, original)
	if err != nil {
		t.Fatal(err)
	}
	return rendered
}

func episodes(numbers ...int) []tv.EpisodeNumbering {
	titles := []string{"Part A", "Part B", "Part C", "Part D"}
	out := make([]tv.EpisodeNumbering, len(numbers))
	for i, n := range numbers {
		out[i] = tv.EpisodeNumbering{Number: n, Title: titles[i]}
	}
	return out
}

// The shipped default reproduces how the user's own library is laid out: unpadded season folders, Specials,
// S00E00 numbering and several titles joined with +.
func TestDefaultNamingReproducesTheLibraryLayout(t *testing.T) {
	n := tv.NamingFor(nil)
	cases := []struct {
		name     string
		episode  tv.EpisodeNaming
		original string
		want     string
	}{
		{"single", tv.EpisodeNaming{SeriesTitle: "Example Show", Season: 3, Episodes: episodes(7)}, "x.mp4",
			"Example Show/Season 3/Example Show - S03E07 - Part A.mp4"},
		{"two-part file", tv.EpisodeNaming{SeriesTitle: "Example Show", Season: 3, Episodes: episodes(10, 11)}, "x.mp4",
			"Example Show/Season 3/Example Show - S03E10-E11 - Part A + Part B.mp4"},
		{"four-part file", tv.EpisodeNaming{SeriesTitle: "Example Show", Season: 3, Episodes: episodes(18, 19, 20, 21)}, "x.mp4",
			"Example Show/Season 3/Example Show - S03E18-E21 - Part A + Part B + Part C + Part D.mp4"},
		{"special", tv.EpisodeNaming{SeriesTitle: "Example Show", Season: 0, Episodes: episodes(29)}, "x.mkv",
			"Example Show/Specials/Example Show - S00E29 - Part A.mkv"},
		{"daily", tv.EpisodeNaming{SeriesType: tv.SeriesDaily, SeriesTitle: "Late Night", Season: 2020, AirDate: "2020-05-06", Episodes: episodes(1)}, "x.mkv",
			"Late Night/Season 2020/Late Night - 2020-05-06 - Part A.mkv"},
	}
	for _, c := range cases {
		if got := render(t, n, c.episode, c.original); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestMultiEpisodeStyles(t *testing.T) {
	base := tv.EpisodeNaming{SeriesTitle: "Show", Season: 1, Episodes: episodes(1, 2, 3)}
	want := map[tv.MultiEpisodeStyle]string{
		tv.StyleExtend:        "Show/Season 1/Show - S01E01-02-03 - Part A + Part B + Part C.mkv",
		tv.StyleDuplicate:     "Show/Season 1/Show - S01E01 - S01E02 - S01E03 - Part A + Part B + Part C.mkv",
		tv.StyleRepeat:        "Show/Season 1/Show - S01E01E02E03 - Part A + Part B + Part C.mkv",
		tv.StyleScene:         "Show/Season 1/Show - S01E01-E02-E03 - Part A + Part B + Part C.mkv",
		tv.StyleRange:         "Show/Season 1/Show - S01E01-03 - Part A + Part B + Part C.mkv",
		tv.StylePrefixedRange: "Show/Season 1/Show - S01E01-E03 - Part A + Part B + Part C.mkv",
	}
	for style, expected := range want {
		n := tv.NamingFor(nil)
		n.MultiEpisode = style
		if got := render(t, n, base, "a.mkv"); got != expected {
			t.Errorf("%s: %q, want %q", style, got, expected)
		}
	}
}

func TestEpisodeTitlesDifferingOnlyByPartNumberAreOne(t *testing.T) {
	n := tv.NamingFor(nil)
	episode := tv.EpisodeNaming{SeriesTitle: "Show", Season: 1, Episodes: []tv.EpisodeNumbering{{Number: 1, Title: "The Finale (1)"}, {Number: 2, Title: "The Finale (2)"}}}
	if got, want := render(t, n, episode, "a.mkv"), "Show/Season 1/Show - S01E01-E02 - The Finale.mkv"; got != want {
		t.Errorf("%q, want %q", got, want)
	}
}

func TestPresetsRenderLiteralBracesAndBrackets(t *testing.T) {
	presets := map[string]string{}
	for _, preset := range tv.NamingScheme.Presets {
		n := tv.Naming{Formats: preset.Formats, Options: naming.DefaultOptions, RenameFiles: true, MultiEpisode: tv.StylePrefixedRange}
		episode := tv.EpisodeNaming{SeriesTitle: "Show", SeriesYear: 2010, IDs: tv.IDs{TVDB: 12345}, Season: 2, Episodes: episodes(3), QualityTitle: "WEBDL-1080p"}
		presets[preset.Name] = render(t, n, episode, "a.mkv")
	}
	want := map[string]string{
		"Solarr default": "Show/Season 2/Show - S02E03 - Part A.mkv",
		"Sonarr default": "Show/Season 2/Show - S02E03 - Part A WEBDL-1080p.mkv",
		"Plex":           "Show (2010) {tvdb-12345}/Season 02/Show (2010) - S02E03 - Part A.mkv",
		"Jellyfin":       "Show (2010) [tvdbid-12345]/Season 02/Show - S02E03 - Part A.mkv",
	}
	for name, expected := range want {
		if presets[name] != expected {
			t.Errorf("%s: %q, want %q", name, presets[name], expected)
		}
	}
}

func TestEveryPresetValidates(t *testing.T) {
	for _, preset := range tv.NamingScheme.Presets {
		if err := tv.NamingScheme.Validate(preset.Formats); err != nil {
			t.Errorf("%s: %v", preset.Name, err)
		}
	}
}

func TestRenameOffKeepsTheFileName(t *testing.T) {
	n := tv.NamingFor(nil)
	n.RenameFiles = false
	got := render(t, n, tv.EpisodeNaming{SeriesTitle: "Show", Season: 1, Episodes: episodes(1)}, "incoming/whatever.name.mkv")
	if want := "Show/Season 1/whatever.name.mkv"; got != want {
		t.Errorf("%q, want %q", got, want)
	}
}

func TestStoredProfileCarriesTheMultiEpisodeStyle(t *testing.T) {
	profile := tv.DefaultNamingProfile()
	if err := tv.ValidateNaming(profile); err != nil {
		t.Fatalf("the default profile must validate: %v", err)
	}
	profile.Formats[tv.FormatMultiEpisodeStyle] = string(tv.StyleRange)
	if style := tv.NamingFor(&profile).MultiEpisode; style != tv.StyleRange {
		t.Errorf("style = %q, want range", style)
	}
	profile.Formats[tv.FormatMultiEpisodeStyle] = "sideways"
	if err := tv.ValidateNaming(profile); err == nil {
		t.Error("an unknown multi-episode style must be refused on save")
	}
}

func TestNamingExamplesRenderEverySlot(t *testing.T) {
	examples := tv.NamingExamples(tv.NamingFor(nil))
	if len(examples) != 5 {
		t.Fatalf("examples = %+v", examples)
	}
	if want := "The Series Title's!/Season 1/The Series Title's! - S01E01-E03 - Episode's Title 1 + Episode's Title 2 + Episode's Title 3.mkv"; examples[1].Path != want {
		t.Errorf("multi example = %q, want %q", examples[1].Path, want)
	}
}
