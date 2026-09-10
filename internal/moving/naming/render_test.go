package naming_test

import (
	"testing"

	"github.com/makearr/MediaNexus/internal/moving/naming"
)

// TestRenderPath_ShippedMusicDefault is section 11.8's own worked example: it
// must reproduce the existing library byte for byte, so adopting it proposes
// zero renames.
func TestRenderPath_ShippedMusicDefault(t *testing.T) {
	folder := mustCompile(t, "{Artist Name}/{Album Title} ({Release Year})")
	file := mustCompile(t, "{Artist Name} - {track:00} - {Track Title}")
	ctx := naming.Context{
		"Artist Name": "Nick Cave and The Bad Seeds", "Album Title": "Tender Prey",
		"Release Year": "1988", "track": "5", "Track Title": "The Furab Keke",
	}

	path, warnings, err := naming.RenderPath(folder, file, ctx, naming.DefaultSeparator, ".flac")
	if err != nil {
		t.Fatalf("RenderPath: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
	want := "Nick Cave and The Bad Seeds/Tender Prey (1988)/Nick Cave and The Bad Seeds - 05 - The Furab Keke.flac"
	if path != want {
		t.Errorf("RenderPath = %q, want %q", path, want)
	}
}

func TestRenderPath_ArtistWithASlashDoesNotAddAPathLevel(t *testing.T) {
	folder := mustCompile(t, "{Artist Name}/{Album Title}")
	file := mustCompile(t, "{track:00} - {Track Title}")
	ctx := naming.Context{
		"Artist Name": "XT/QK", "Album Title": "Kata In Mifed",
		"track": "1", "Track Title": "Metup Nagug",
	}

	path, _, err := naming.RenderPath(folder, file, ctx, naming.DefaultSeparator, ".flac")
	if err != nil {
		t.Fatalf("RenderPath: %v", err)
	}
	want := "XT+QK/Kata In Mifed/01 - Metup Nagug.flac"
	if path != want {
		t.Errorf("RenderPath = %q, want %q -- the artist's own slash must not become a folder boundary", path, want)
	}
}

func TestRenderPath_ExtensionSurvivesTruncation(t *testing.T) {
	file := mustCompile(t, "{Track Title:.10}")
	ctx := naming.Context{"Track Title": "A Very Long Track Title That Would Otherwise Run On"}

	path, _, err := naming.RenderPath(nil, file, ctx, naming.DefaultSeparator, ".flac")
	if err != nil {
		t.Fatalf("RenderPath: %v", err)
	}
	if path != "A Very Lon.flac" {
		t.Errorf("RenderPath = %q, want the extension preserved after truncation", path)
	}
}

func TestRenderPath_EmptyFileRenderIsAnError(t *testing.T) {
	file := mustCompile(t, "{Track Title}")
	if _, _, err := naming.RenderPath(nil, file, naming.Context{}, naming.DefaultSeparator, ".flac"); err == nil {
		t.Fatal("RenderPath: expected an error when the file template renders to nothing, got nil")
	}
}

func TestRenderPath_ReservedNameIsSanitised(t *testing.T) {
	folder := mustCompile(t, "{Artist Name}")
	file := mustCompile(t, "{Track Title}")
	ctx := naming.Context{"Artist Name": "CON", "Track Title": "Track"}

	path, _, err := naming.RenderPath(folder, file, ctx, naming.DefaultSeparator, ".flac")
	if err != nil {
		t.Fatalf("RenderPath: %v", err)
	}
	if path != "CON_/Track.flac" {
		t.Errorf("RenderPath = %q, want the Windows-reserved folder name sanitised", path)
	}
}
