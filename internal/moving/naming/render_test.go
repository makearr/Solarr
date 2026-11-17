package naming_test

import (
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/moving/naming"
)

func renderPath(t *testing.T, template string, ctx naming.Context, opts naming.Options) string {
	t.Helper()
	path, _, err := naming.RenderPath(mustCompile(t, template), ctx, opts, ".flac")
	if err != nil {
		t.Fatalf("RenderPath(%q): %v", template, err)
	}
	return path
}

// The user's own default reproduces the test library's layout byte for byte.
func TestRenderPath_TheUsersDefault(t *testing.T) {
	ctx := naming.Context{
		"Artist Name": "Vessa Lund and The Low Tides", "Album Title": "Quiet Harbour",
		"Release Year": "1988", "track": "5", "Track Title": "The Furab Keke",
	}
	got := renderPath(t, "{Artist Name}/{Album Title} ({Release Year})/{Artist Name} - {track:00} - {Track Title}",
		ctx, naming.DefaultOptions)
	want := "Vessa Lund and The Low Tides/Quiet Harbour (1988)/Vessa Lund and The Low Tides - 05 - The Furab Keke.flac"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// An album whose year is not known yet is "Quiet Harbour", never a "Quiet Harbour ()" folder beside the real one.
func TestRenderPath_AnUnknownYearLeavesNoEmptyBrackets(t *testing.T) {
	ctx := naming.Context{"Artist Name": "Heka", "Album Title": "Quiet Harbour", "track": "5", "Track Title": "Pihudoc Zaze"}
	got := renderPath(t, "{Album Title} ({Release Year})/{Artist Name} - {track:00} - {Track Title} [{Release Year}]",
		ctx, naming.DefaultOptions)
	want := "Quiet Harbour/Heka - 05 - Pihudoc Zaze.flac"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The user's multi-disc default has a literal colon; smart replacement turns it into the
// "1-05" the library already uses (answered 2026-09-24: literals are cleaned too).
func TestRenderPath_MultiDiscColonIsReplacedLikeAnyOther(t *testing.T) {
	ctx := naming.Context{"Artist Name": "Heka", "medium": "2", "track": "5", "Track Title": "Pihudoc Zaze"}
	template := "{Artist Name} - {medium:0}:{track:00} - {Track Title}"
	for mode, want := range map[naming.ColonReplacement]string{
		naming.ColonSmart:          "Heka - 2-05 - Pihudoc Zaze.flac",
		naming.ColonDelete:         "Heka - 205 - Pihudoc Zaze.flac",
		naming.ColonDash:           "Heka - 2-05 - Pihudoc Zaze.flac",
		naming.ColonSpaceDash:      "Heka - 2 -05 - Pihudoc Zaze.flac",
		naming.ColonSpaceDashSpace: "Heka - 2 - 05 - Pihudoc Zaze.flac",
	} {
		got := renderPath(t, template, ctx, naming.Options{ReplaceIllegalCharacters: true, ColonReplacement: mode})
		if got != want {
			t.Errorf("%s: got %q, want %q", mode, got, want)
		}
	}
}

func TestRenderPath_SmartColonInATitle(t *testing.T) {
	got := renderPath(t, "{Album Title}", naming.Context{"Album Title": "Live: 1986"}, naming.DefaultOptions)
	if got != "Live - 1986.flac" {
		t.Errorf("got %q, want %q", got, "Live - 1986.flac")
	}
}

// Lidarr's table: the test library's "(How Could She!)" and "Heka + The Kinih" are its work.
func TestRenderPath_IllegalCharacters(t *testing.T) {
	ctx := naming.Context{"Track Title": `How Could She? <"a*b|c">`}
	if got := renderPath(t, "{Track Title}", ctx, naming.DefaultOptions); got != "How Could She! a-bc.flac" {
		t.Errorf("replacing: got %q", got)
	}
	if got := renderPath(t, "{Track Title}", ctx, naming.Options{}); got != "How Could She abc.flac" {
		t.Errorf("not replacing: got %q", got)
	}
}

// An SMB share turned "*" and a trailing dot into 8.3 names; symbols such as "★" it keeps.
func TestRenderPath_NamesAnSMBShareKeeps(t *testing.T) {
	for artist, want := range map[string]string{
		"C*Bomlbor": "C-Bomlbor/C-Bomlbor - Rollercoaster.flac",
		"Cibiz M.":  "Cibiz M/Cibiz M. - Rollercoaster.flac",
		"A★Teens":   "A★Teens/A★Teens - Rollercoaster.flac",
		"Cibiz M. ": "Cibiz M/Cibiz M. - Rollercoaster.flac",
	} {
		ctx := naming.Context{"Artist Name": artist, "Track Title": "Rollercoaster"}
		if got := renderPath(t, "{Artist Name}/{Artist Name} - {Track Title}", ctx, naming.DefaultOptions); got != want {
			t.Errorf("%q: got %q, want %q", artist, got, want)
		}
	}
}

func TestRenderPath_AValueSlashNeverAddsAFolder(t *testing.T) {
	ctx := naming.Context{"Artist Name": "XT/QK", "Track Title": "Metup Nagug"}
	if got := renderPath(t, "{Artist Name}/{Track Title}", ctx, naming.DefaultOptions); got != "XT+QK/Metup Nagug.flac" {
		t.Errorf("got %q", got)
	}
}

// An empty token leaves separators behind; Lidarr collapses and trims them.
func TestRenderPath_EmptyTokenLeavesNoDanglingSeparators(t *testing.T) {
	ctx := naming.Context{"Artist Name": "Heka", "Track Title": "Covabapoz Fofu"}
	got := renderPath(t, "{Artist Name} - {Album Title} - {Track Title} - {Medium Format}", ctx, naming.DefaultOptions)
	if got != "Heka - - Covabapoz Fofu.flac" {
		t.Errorf("got %q", got)
	}
}

func TestRenderPath_EmptyFolderIsDropped(t *testing.T) {
	ctx := naming.Context{"Track Title": "Track"}
	if got := renderPath(t, "{Medium Format}/{Track Title}", ctx, naming.DefaultOptions); got != "Track.flac" {
		t.Errorf("got %q", got)
	}
}

func TestRenderPath_ExtensionSurvivesTruncation(t *testing.T) {
	ctx := naming.Context{"Track Title": "A Very Long Track Title That Would Otherwise Run On"}
	if got := renderPath(t, "{Track Title:.10}", ctx, naming.DefaultOptions); got != "A Very Lon.flac" {
		t.Errorf("got %q, want the extension preserved after truncation", got)
	}
}

// A file name that renders to nothing must not hand its extension to the last folder.
func TestRenderPath_EmptyFileNameIsAnError(t *testing.T) {
	ctx := naming.Context{"Artist Name": "Heka"}
	if _, _, err := naming.RenderPath(mustCompile(t, "{Artist Name}/{Track Title}"), ctx, naming.DefaultOptions, ".flac"); err == nil {
		t.Fatal("expected an error when the file name renders to nothing")
	}
}

func TestRenderPath_ReservedNameIsSanitised(t *testing.T) {
	ctx := naming.Context{"Artist Name": "CON", "Track Title": "Track"}
	if got := renderPath(t, "{Artist Name}/{Track Title}", ctx, naming.DefaultOptions); got != "CON_/Track.flac" {
		t.Errorf("got %q, want the Windows-reserved folder name sanitised", got)
	}
}

func TestRenderPath_LongPathWarnsButDoesNotFail(t *testing.T) {
	ctx := naming.Context{"Artist Name": strings.Repeat("Artist ", 10), "Track Title": strings.Repeat("A very long track title indeed ", 10)}
	path, warnings, err := naming.RenderPath(mustCompile(t, "{Artist Name}/{Track Title}"), ctx, naming.DefaultOptions, ".flac")
	if err != nil {
		t.Fatalf("RenderPath: %v", err)
	}
	if len(path) <= naming.MaxRecommendedRelPathBytes {
		t.Fatalf("test path is %d bytes, want it over %d", len(path), naming.MaxRecommendedRelPathBytes)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0].Message, "recommended limit") {
		t.Errorf("warnings = %v, want exactly the recommended-limit one", warnings)
	}
}

func TestRenderPath_ShortPathNeverWarns(t *testing.T) {
	ctx := naming.Context{"Artist Name": "Artist", "Track Title": "Short"}
	_, warnings, err := naming.RenderPath(mustCompile(t, "{Artist Name}/{Track Title}"), ctx, naming.DefaultOptions, ".flac")
	if err != nil {
		t.Fatalf("RenderPath: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
}

func TestColonReplacement_Valid(t *testing.T) {
	for _, mode := range naming.ColonReplacements {
		if !mode.Valid() {
			t.Errorf("%s is listed but not valid", mode)
		}
	}
	if naming.ColonReplacement("underscore").Valid() {
		t.Error("an unlisted mode is valid")
	}
}

func TestTitleForms(t *testing.T) {
	for _, tc := range []struct {
		fn       func(string) string
		in, want string
	}{
		{naming.TitleThe, "The Kokopob", "Kokopob, The"},
		{naming.TitleThe, "A Fivatad Novinu", "Fivatad Novinu, A"},
		{naming.TitleThe, "The Gupe (1979)", "Gupe, The (1979)"},
		{naming.TitleThe, "Theatre", "Theatre"},
		{naming.CleanTitle, "Guns N' Roses", "Guns N Roses"},
		{naming.CleanTitle, "Batel & Nihirekuh", "Batel and Nihirekuh"},
		{naming.CleanTitle, "XT/QK - Live: 1992 (Remastered)", "XT QK Live 1992 Remastered"},
		{naming.FirstCharacter, "The Kokopob", "K"},
		{naming.FirstCharacter, "pióurazo", "P"},
		{naming.FirstCharacter, "!!!", "_"},
		{naming.FirstCharacter, "'68 Comeback", "6"},
	} {
		if got := tc.fn(tc.in); got != tc.want {
			t.Errorf("%q -> %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Titles that open with dots, are made of separators, or are not normalised must still land in one visible folder.
func TestRenderPath_HostileTitles(t *testing.T) {
	const template = "{Artist Name}/{Album Title}/{track:00} - {Track Title}"
	for _, tc := range []struct{ name, artist, album, want string }{
		{"leading ellipsis", "Plain Band", "...And Then Some", "Plain Band/And Then Some/03 - Song.flac"},
		{"leading dot in the artist", ".band", "Album", "band/Album/03 - Song.flac"},
		{"an artist of dots keeps its level", "...", "Album", "_/Album/03 - Song.flac"},
		{"an album of a hyphen keeps its level", "Band", "-", "Band/_/03 - Song.flac"},
		{"an album of two dots keeps its level", "Band", "..", "Band/_/03 - Song.flac"},
		{"decomposed text is composed", "Band", "Café", "Band/Café/03 - Song.flac"},
		{"bidi override and zero-width space are dropped", "Band", "Al\u202ebum\u200b", "Band/Album/03 - Song.flac"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := naming.Context{"Artist Name": tc.artist, "Album Title": tc.album, "track": "3", "Track Title": "Song"}
			if got := renderPath(t, template, ctx, naming.DefaultOptions); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// The extension is counted in the 255-byte limit, and the end of a name, where two files differ, survives.
func TestRenderPath_ALongNameKeepsItsEndAndFitsWithItsExtension(t *testing.T) {
	long := strings.Repeat("Allegro ma non troppo ", 14)
	render := func(suffix string) string {
		ctx := naming.Context{"Album Title": "Album", "Track Title": long + suffix}
		return renderPath(t, "{Album Title}/{Track Title}", ctx, naming.DefaultOptions)
	}
	first, second := render("(Part 1)"), render("(Part 2)")
	if first == second {
		t.Fatal("two names that differ only at the end rendered the same")
	}
	for _, got := range []string{first, second} {
		file := got[strings.LastIndex(got, "/")+1:]
		if len(file) > 255-24+len(".flac") {
			t.Errorf("file name is %d bytes: %q", len(file), file)
		}
		if !strings.HasSuffix(got, ".flac") || !strings.Contains(got, "…") {
			t.Errorf("got %q, want an elided name that keeps its extension", got)
		}
	}
	if !strings.HasSuffix(strings.TrimSuffix(first, ".flac"), "(Part 1)") {
		t.Errorf("the tail was cut: %q", first)
	}
}

func TestRenderPath_AMultiByteNameIsCutOnARuneBoundary(t *testing.T) {
	ctx := naming.Context{"Album Title": "Album", "Track Title": strings.Repeat("あ", 120)}
	got := renderPath(t, "{Album Title}/{Track Title}", ctx, naming.DefaultOptions)
	if !utf8Valid(got) {
		t.Errorf("rendered path is not valid UTF-8: %q", got)
	}
}

func utf8Valid(s string) bool { return strings.ToValidUTF8(s, "�") == s }
