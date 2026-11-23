package music_test

import (
	"context"
	"testing"

	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/media/music"
)

type fakeServer struct {
	ids   music.ServerAlbumIDs
	found bool
	asked []string
}

func (f *fakeServer) AlbumIDsByPath(_ context.Context, _ int64, absPath string) (music.ServerAlbumIDs, bool) {
	f.asked = append(f.asked, absPath)
	return f.ids, f.found
}

func serverIdentifier(server *fakeServer) music.ServerIdentifier {
	return music.NewServerIdentifier(server, 7, func(rel string) string { return "/music/" + rel })
}

const (
	groupID   = "11111111-2222-3333-4444-555555555555"
	releaseID = "66666666-7777-8888-9999-aaaaaaaaaaaa"
)

func TestTheServersReleaseGroupNamesTheFileAndIsAskedOncePerFolder(t *testing.T) {
	server := &fakeServer{ids: music.ServerAlbumIDs{ReleaseGroup: groupID, Release: releaseID}, found: true}
	identifier := serverIdentifier(server)
	for _, name := range []string{"Band/Album/01.flac", "Band/Album/02.flac"} {
		got, err := identifier.Identify(t.Context(), model.FileRef{RelPath: name})
		if err != nil || len(got) != 1 || got[0].ExternalID.Provider != music.ProviderReleaseGroup || got[0].ExternalID.ExternalID != groupID ||
			got[0].Strategy != music.StrategyMediaServer || got[0].Confidence != 0.95 {
			t.Fatalf("%s: got %+v, %v", name, got, err)
		}
	}
	if len(server.asked) != 1 {
		t.Errorf("the server was asked %d times for one folder: %q", len(server.asked), server.asked)
	}
}

func TestTheServersReleaseNamesTheFileWhenItHoldsNoGroupAndAMalformedIdNamesNothing(t *testing.T) {
	got, _ := serverIdentifier(&fakeServer{ids: music.ServerAlbumIDs{Release: releaseID}, found: true}).Identify(t.Context(), model.FileRef{RelPath: "A/B/1.flac"})
	if len(got) != 1 || got[0].ExternalID.Provider != music.ProviderRelease || got[0].ExternalID.ExternalID != releaseID {
		t.Errorf("release only: %+v", got)
	}
	for name, server := range map[string]*fakeServer{
		"no match":  {found: false},
		"malformed": {ids: music.ServerAlbumIDs{ReleaseGroup: "not-an-id"}, found: true},
		"empty":     {found: true},
	} {
		if got, err := serverIdentifier(server).Identify(t.Context(), model.FileRef{RelPath: "A/B/1.flac"}); err != nil || got != nil {
			t.Errorf("%s: got %+v, %v", name, got, err)
		}
	}
}
