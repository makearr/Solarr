package comic

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
)

// Every preset, rendered for an issue, a decimal and suffixed issue, a volume and a chapter.
func TestPresetsRenderEveryShape(t *testing.T) {
	file := FileNaming{Series: "Night Harbor", Year: 2019, ReleaseYear: 2020, Issue: "3", Volume: 2, Chapter: "12"}
	decimal, suffixed := file, file
	decimal.Issue, suffixed.Issue = "1.5", "1AU"
	cases := []struct {
		preset, slot string
		file         FileNaming
		want         string
	}{
		{"Solarr default", SlotIssueFile, file, "Night Harbor (2019)/Night Harbor (2019) #003.cbz"},
		{"Solarr default", SlotIssueFile, decimal, "Night Harbor (2019)/Night Harbor (2019) #001.5.cbz"},
		{"Solarr default", SlotIssueFile, suffixed, "Night Harbor (2019)/Night Harbor (2019) #001AU.cbz"},
		{"Solarr default", SlotVolumeFile, file, "Night Harbor (2019)/Night Harbor v02.cbz"},
		{"Solarr default", SlotChapterFile, file, "Night Harbor (2019)/Night Harbor c012.cbz"},
		{"Mylar and Issuarr", SlotIssueFile, file, "Night Harbor (2019)/Night Harbor 003 (2020).cbz"},
		{"Kapowarr", SlotIssueFile, file, "Night Harbor/Volume 02 (2019)/Night Harbor (2019) Volume 02 Issue 003.cbz"},
	}
	for _, preset := range NamingScheme().Presets {
		profile := NamingScheme().DefaultProfile(defaultOptions)
		profile.Formats = preset.Formats
		profile.RenameFiles = true
		for _, c := range cases {
			if c.preset != preset.Name {
				continue
			}
			got, _, err := NamingFor(&profile).RenderFile(c.slot, c.file, "original.cbz")
			if err != nil || got != c.want {
				t.Errorf("%s %s %q: got %q, %v; want %q", preset.Name, c.slot, c.file.Issue, got, err, c.want)
			}
		}
	}
}

func TestRenameOffKeepsTheOriginalName(t *testing.T) {
	profile := settings.NamingProfile{RenameFiles: false, Formats: NamingScheme().Presets[0].Formats}
	got, _, err := NamingFor(&profile).RenderFile(SlotIssueFile, FileNaming{Series: "Night Harbor", Year: 2019, Issue: "3"}, "dl/whatever_03.cbz")
	if err != nil || got != "Night Harbor (2019)/whatever_03.cbz" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestModuleNamingIsValidAndExampled(t *testing.T) {
	var m media.Naming = Module{}
	if err := m.ValidateNaming(m.DefaultNamingProfile()); err != nil {
		t.Fatalf("default profile invalid: %v", err)
	}
	profile := m.DefaultNamingProfile()
	profile.RenameFiles = true
	examples := m.NamingExamples(profile)
	if len(examples) != 5 || examples[1].Path != "Night Harbor (2019)/Night Harbor (2019) #001.5.cbz" {
		t.Fatalf("examples = %+v", examples)
	}
}

func TestAMangaLibraryWithoutAProfileHasNoYearInItsSeriesFolder(t *testing.T) {
	facts := FileNaming{Series: "Night Harbor", Year: 2019, Volume: 2}
	manga, _, _ := NamingForKind(nil, KindManga).RenderFile(SlotVolumeFile, facts, "x.cbz")
	comics, _, _ := NamingForKind(nil, KindComics).RenderFile(SlotVolumeFile, facts, "x.cbz")
	if manga != "Night Harbor/Night Harbor v02.cbz" || comics != "Night Harbor (2019)/Night Harbor v02.cbz" {
		t.Fatalf("manga %q, comics %q", manga, comics)
	}
}
