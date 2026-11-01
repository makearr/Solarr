package apiserver_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
)

type fakePlayback struct {
	file apiserver.PlaybackFile
	err  error
}

func (f *fakePlayback) LocatePlayback(context.Context, int64) (apiserver.PlaybackFile, error) {
	return f.file, f.err
}

func writeFixtureMedia(t *testing.T, fixture worksFixture, path string, content string) {
	t.Helper()
	if err := fixture.fsys.MkdirAll("/media/music", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	file, err := fixture.fsys.Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := file.Write([]byte(content)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestPlayback_InfoAndRangedStream(t *testing.T) {
	fixture := newWorksFixture(t)
	category, root := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	user := fixture.createUser(t, "listener", settings.RoleReadonly)
	if err := fixture.cache.SetCategoryAccess(t.Context(), user.ID, category.ID, settings.PermissionRead); err != nil {
		t.Fatalf("SetCategoryAccess: %v", err)
	}
	cookies := fixture.signIn(t, "listener")
	writeFixtureMedia(t, fixture, "/media/music/a.flac", "0123456789")
	fixture.playback.file = apiserver.PlaybackFile{
		MediaObjectID: 5, AbsolutePath: "/media/music/a.flac", RootFolderID: root.ID, DurationMs: 61000, AudioCodec: "flac",
	}

	info := fixture.do(authedGet(t, cookies, "/api/v1/files/5/playback"))
	if info.Code != http.StatusOK {
		t.Fatalf("playback status %d (%s)", info.Code, info.Body)
	}
	for _, want := range []string{`"kind":"audio"`, `"direct_play_likely":true`, `"duration_ms":61000`, `"transcode_available":false`} {
		if !strings.Contains(info.Body.String(), want) {
			t.Errorf("playback body %s lacks %s", info.Body, want)
		}
	}

	request := authedGet(t, cookies, "/api/v1/files/5/stream")
	request.Header.Set("Range", "bytes=2-5")
	stream := fixture.do(request)
	if stream.Code != http.StatusPartialContent || stream.Body.String() != "2345" {
		t.Fatalf("stream = %d %q, want 206 and the range", stream.Code, stream.Body)
	}
}

func TestPlayback_AFileOutsideTheCallersLibrariesDoesNotExist(t *testing.T) {
	fixture := newWorksFixture(t)
	_, root := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	fixture.createUser(t, "stranger", settings.RoleReadonly)
	cookies := fixture.signIn(t, "stranger")
	writeFixtureMedia(t, fixture, "/media/music/a.flac", "0123456789")
	fixture.playback.file = apiserver.PlaybackFile{MediaObjectID: 5, AbsolutePath: "/media/music/a.flac", RootFolderID: root.ID}

	for _, path := range []string{"playback", "stream", "transcode"} {
		if got := fixture.do(authedGet(t, cookies, "/api/v1/files/5/"+path)).Code; got != http.StatusNotFound {
			t.Errorf("%s of a file the caller cannot read = %d, want 404", path, got)
		}
	}
}

func TestPlayback_ConvertingIsRefusedWithoutFFmpegWiring(t *testing.T) {
	fixture := newWorksFixture(t)
	category, root := fixture.createCategoryWithRoot(t, "Video", "/media/video")
	user := fixture.createUser(t, "viewer", settings.RoleReadonly)
	if err := fixture.cache.SetCategoryAccess(t.Context(), user.ID, category.ID, settings.PermissionRead); err != nil {
		t.Fatalf("SetCategoryAccess: %v", err)
	}
	cookies := fixture.signIn(t, "viewer")
	fixture.playback.file = apiserver.PlaybackFile{MediaObjectID: 6, AbsolutePath: "/media/video/a.mkv", RootFolderID: root.ID}

	if got := fixture.do(authedGet(t, cookies, "/api/v1/files/6/transcode")).Code; got != http.StatusServiceUnavailable {
		t.Fatalf("transcode without a service = %d, want 503", got)
	}
}

func TestDirectPlayLikely(t *testing.T) {
	for _, test := range []struct {
		name string
		file apiserver.PlaybackFile
		kind string
		want bool
	}{
		{"flac", apiserver.PlaybackFile{AbsolutePath: "/a.flac", AudioCodec: "flac"}, "audio", true},
		{"alac in m4a", apiserver.PlaybackFile{AbsolutePath: "/a.m4a", AudioCodec: "alac"}, "audio", false},
		{"wma", apiserver.PlaybackFile{AbsolutePath: "/a.wma"}, "audio", false},
		{"h264 mp4", apiserver.PlaybackFile{AbsolutePath: "/a.mp4", VideoCodec: "h264", AudioCodec: "aac"}, "video", true},
		{"hevc mp4", apiserver.PlaybackFile{AbsolutePath: "/a.mp4", VideoCodec: "hevc", AudioCodec: "aac"}, "video", false},
		{"ac3 in mp4", apiserver.PlaybackFile{AbsolutePath: "/a.mp4", VideoCodec: "h264", AudioCodec: "ac3"}, "video", false},
		{"mkv", apiserver.PlaybackFile{AbsolutePath: "/a.mkv", VideoCodec: "h264", AudioCodec: "aac"}, "video", false},
		{"unprobed mp4", apiserver.PlaybackFile{AbsolutePath: "/a.mp4"}, "video", true},
	} {
		if got := apiserver.DirectPlayLikelyForTest(test.file, test.kind); got != test.want {
			t.Errorf("%s: DirectPlayLikely = %v, want %v", test.name, got, test.want)
		}
	}
}
