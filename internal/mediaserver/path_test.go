package mediaserver_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/mediaserver"
)

func TestMapPath(t *testing.T) {
	pairs := []mediaserver.PathPair{
		{Solarr: "/library", Server: "/data"},
		{Solarr: "/library/music", Server: "/srv/music/"},
	}
	for _, test := range []struct {
		name, in, want string
		mapped         bool
		reverse        bool
	}{
		{"longest prefix wins", "/library/music/a/b.flac", "/srv/music/a/b.flac", true, false},
		{"shorter prefix", "/library/tv/s.mkv", "/data/tv/s.mkv", true, false},
		{"no pair matches", "/elsewhere/x", "/elsewhere/x", false, false},
		{"not a path boundary", "/library2/x", "/library2/x", false, false},
		{"back again", "/srv/music/a/b.flac", "/library/music/a/b.flac", true, true},
	} {
		got, mapped := mediaserver.MapPath(pairs, test.in)
		if test.reverse {
			got, mapped = mediaserver.UnmapPath(pairs, test.in)
		}
		if got != test.want || mapped != test.mapped {
			t.Errorf("%s: got %q,%v want %q,%v", test.name, got, mapped, test.want, test.mapped)
		}
	}
	if got, mapped := mediaserver.MapPath(nil, "/a"); got != "/a" || mapped {
		t.Errorf("no pairs: got %q,%v", got, mapped)
	}
}

func TestKinds_JellyfinCapabilities(t *testing.T) {
	kind, found := mediaserver.KindOf("jellyfin")
	if !found || !kind.CanPush || !kind.PushByPath || !kind.CanAnnounce || !kind.Takes("tv") || kind.Takes("book") {
		t.Errorf("jellyfin = %+v, found %v", kind, found)
	}
	if kind, _ := mediaserver.KindOf("calibre"); kind.CanPush || kind.CanPlayback || !kind.CanMetadata {
		t.Errorf("calibre = %+v, want metadata only", kind)
	}
}
