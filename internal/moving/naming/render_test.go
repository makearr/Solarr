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
		"Artist Name": "Nick Cave and The Bad Seeds", "Album Title": "Tender Prey",
		"Release Year": "1988", "track": "5", "Track Title": "The Furab Keke",
	}
	got := renderPath(t, "{Artist Name}/{Album Title} ({Release Year})/{Artist Name} - {track:00} - {Track Title}",
		ctx, naming.DefaultOptions)
	want := "Nick Cave and The Bad Seeds/Tender Prey (1988)/Nick Cave and The Bad Seeds - 05 - The Furab Keke.flac"
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
	ctx := naming.Context{"Artist Name": "Artist", "Track Title": strings.Repeat("A very long track title indeed ", 10)}
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
		{naming.TitleThe, "The Gupe (1979)", "Wall, The (1979)"},
		{naming.TitleThe, "Theatre", "Theatre"},
		{naming.CleanTitle, "Guns N' Roses", "Guns N Roses"},
		{naming.CleanTitle, "Batel & Nihirekuh", "Batel and Nihirekuh"},
		{naming.CleanTitle, "XT/QK - Live: 1992 (Remastered)", "XT QK Live 1992 Remastered"},
		{naming.FirstCharacter, "The Kokopob", "B"},
		{naming.FirstCharacter, "pióurazo", "B"},
		{naming.FirstCharacter, "!!!", "_"},
		{naming.FirstCharacter, "'68 Comeback", "6"},
	} {
		if got := tc.fn(tc.in); got != tc.want {
			t.Errorf("%q -> %q, want %q", tc.in, got, tc.want)
		}
	}
}
