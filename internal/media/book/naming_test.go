package book_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media/book"
)

func render(t *testing.T, format book.Format, preset int, file book.FileNaming, original string) string {
	t.Helper()
	formats := format.NamingScheme().Presets[preset].Formats
	names := format.NamingFor(&settings.NamingProfile{Formats: formats, RenameFiles: true, ReplaceIllegalCharacters: true, ColonReplacement: "delete"})
	rendered, _, err := names.RenderBook(file, original)
	if err != nil {
		t.Fatal(err)
	}
	return rendered
}

func TestNaming_EveryPresetRendersABookAndItsParts(t *testing.T) {
	novel := book.FileNaming{Author: "Mara Quell", Title: "The Lantern Keeper: A Novel", Year: 1987}
	cases := []struct {
		format book.Format
		preset int
		file   book.FileNaming
		want   string
	}{
		{book.Ebook, 0, novel, "Mara Quell/The Lantern Keeper A Novel (1987)/Mara Quell - The Lantern Keeper A Novel.epub"},
		{book.Ebook, 1, novel, "Mara Quell/The Lantern Keeper A Novel/Mara Quell - The Lantern Keeper A Novel.epub"},
		{book.Audiobook, 0, book.FileNaming{Author: "Mara Quell", Title: "Ember Roads", Year: 2015, PartNumber: 3, PartCount: 12},
			"Mara Quell/Ember Roads (2015)/Ember Roads - 03.epub"},
		{book.Audiobook, 0, book.FileNaming{Author: "Mara Quell", Title: "Ember Roads", Year: 2015, PartNumber: 1, PartCount: 1},
			"Mara Quell/Ember Roads (2015)/Ember Roads.epub"},
		{book.Audiobook, 2, book.FileNaming{Author: "Mara Quell", Narrator: "Tobin Reyes", Title: "Ember Roads", SeriesPosition: "2", PartNumber: 3, PartCount: 12},
			"Mara Quell/2 - Ember Roads {Tobin Reyes}/03 - Ember Roads.epub"},
	}
	for _, c := range cases {
		if got := render(t, c.format, c.preset, c.file, "original.epub"); got != c.want {
			t.Errorf("%s preset %d: got %q, want %q", c.format.MediaType, c.preset, got, c.want)
		}
	}
}

func TestNaming_EveryPresetValidates(t *testing.T) {
	for _, format := range book.Formats {
		for _, preset := range format.NamingScheme().Presets {
			profile := format.DefaultNamingProfile()
			profile.Formats = preset.Formats
			if err := format.ValidateNaming(profile); err != nil {
				t.Errorf("%s %s: %v", format.MediaType, preset.Name, err)
			}
		}
	}
}

func TestFormat_IncludesItsFilesButNotSamples(t *testing.T) {
	cases := map[string]bool{
		"Mara Quell/Samples of Life.epub": true, "Mara Quell/Night Ferries.sample.epub": false, "x/._Night Ferries.epub": false,
		"Mara Quell/Night Ferries.m4b": false, "Mara Quell/Night Ferries.jpg": false,
	}
	for name, want := range cases {
		if got := book.Ebook.Includes(name); got != want {
			t.Errorf("Includes(%q) = %v, want %v", name, got, want)
		}
	}
	if !book.Audiobook.Includes("Mara Quell/Night Ferries/01.m4b") {
		t.Error("an audiobook's chapter is its file")
	}
}

func TestFormat_ProbesAnAudiobooksFilesButNoEbook(t *testing.T) {
	if !book.Audiobook.Probes("Author/Title/01.m4b") || book.Ebook.Probes("Author/Title/Title.epub") {
		t.Errorf("Probes: audiobook %v, ebook %v; want true, false", book.Audiobook.Probes("x.m4b"), book.Ebook.Probes("x.epub"))
	}
}
