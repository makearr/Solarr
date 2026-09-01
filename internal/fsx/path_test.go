package fsx_test

import (
	"strings"
	"testing"

	"github.com/makearr/MediaNexus/internal/fsx"
)

func TestSanitiseComponent(t *testing.T) {
	cases := []struct {
		name      string
		component string
		want      string
	}{
		{"leaves an ordinary name alone", "Kata in Mifed", "Kata in Mifed"},
		{"replaces a separator rather than dropping it", "XT/QK", "XT_QK"},
		{"replaces a backslash too", `XT\QK`, "XT_QK"},
		{"drops control characters", "Track\x01Two\x7f", "TrackTwo"},
		{"drops a null byte", "evil\x00name", "evilname"},
		{"keeps leading dots", "...And Justice for All", "...And Justice for All"},
		{"strips trailing dots", "Album.", "Album"},
		{"strips trailing spaces", "Album   ", "Album"},
		{"strips leading spaces", "   Album", "Album"},
		{"rejects the current directory", ".", ""},
		{"rejects the parent directory", "..", ""},
		{"neutralises a traversal component", "../..", ".._"},
		{"rejects an all-dots name", "...", ""},
		{"rejects an empty name", "", ""},
		{"rejects whitespace only", "   ", ""},
		{"escapes a Windows reserved name", "CON", "CON_"},
		{"escapes a reserved name before its extension", "CON.mkv", "CON_.mkv"},
		{"escapes a reserved name case-insensitively", "nul.txt", "nul_.txt"},
		{"leaves a name that merely starts with a reserved word", "CONCERT", "CONCERT"},
		{"keeps unicode", "Sigur Rós — Ágætis byrjun", "Sigur Rós — Ágætis byrjun"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := fsx.SanitiseComponent(testCase.component); got != testCase.want {
				t.Errorf("SanitiseComponent(%q) = %q, want %q", testCase.component, got, testCase.want)
			}
		})
	}
}

func TestSanitiseComponent_CapsLengthWithoutSplittingARune(t *testing.T) {
	// Three-byte runes, so a naive byte cut would land mid-rune.
	got := fsx.SanitiseComponent(strings.Repeat("実", 200))

	if len(got) > 255 {
		t.Errorf("length = %d bytes, want at most 255", len(got))
	}
	if strings.ContainsRune(got, '�') {
		t.Error("truncation split a rune")
	}
	if want := strings.Repeat("実", 85); got != want {
		t.Errorf("got %d runes, want %d", len([]rune(got)), len([]rune(want)))
	}
}

func TestWithin(t *testing.T) {
	cases := []struct {
		root, path string
		want       bool
	}{
		{"/media/music", "/media/music", true},
		{"/media/music", "/media/music/Artist/Album", true},
		{"/media/music", "/media/music2", false},
		{"/media/music", "/media", false},
		{"/media/music", "/etc/passwd", false},
		{"/media/music/", "/media/music/Artist", true},
		{"/", "/anything", true},
	}

	for _, testCase := range cases {
		if got := fsx.Within(testCase.root, testCase.path); got != testCase.want {
			t.Errorf("Within(%q, %q) = %v, want %v", testCase.root, testCase.path, got, testCase.want)
		}
	}
}

func TestSafeJoin_RejectsARelativeRoot(t *testing.T) {
	fsys, _ := newMemUnderTest(t)
	if _, err := fsx.SafeJoin(fsys, "relative/root", "file"); err == nil {
		t.Fatal("SafeJoin with a relative root succeeded; roots come from config and must be absolute")
	}
}
