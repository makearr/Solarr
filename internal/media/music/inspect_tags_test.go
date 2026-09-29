package music_test

import (
	"context"
	"errors"
	"testing"

	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// fakeTagTool answers ReadTags from a fixed table, keyed by the exact path
// asked for -- Probe.Path is not read by BuildInspectTagReader, matching
// mediatool.Probe's own doc comment that results are matched positionally.
type fakeTagTool struct {
	byPath map[string]mediatool.Probe
}

func (f fakeTagTool) Available(context.Context) error { return nil }

func (f fakeTagTool) ReadTags(_ context.Context, paths []string) ([]mediatool.Probe, error) {
	probes := make([]mediatool.Probe, len(paths))
	for i, p := range paths {
		probes[i] = f.byPath[p]
	}
	return probes, nil
}

func (f fakeTagTool) WriteTags(context.Context, []mediatool.TagEdit) ([]mediatool.TagResult, error) {
	return nil, nil
}

func (f fakeTagTool) Fingerprint(context.Context, []string) ([]mediatool.Fingerprint, error) {
	return nil, nil
}

func TestBuildInspectTagReader_ReadsTheSameTagsTheADR24ChainReads(t *testing.T) {
	tool := fakeTagTool{byPath: map[string]mediatool.Probe{
		"/downloads/album/01.flac": {Tags: mediatool.Tags{
			"ALBUM": "Zuroveguv Wants to Dance", "ALBUMARTIST": "Cibiz M",
			"MUSICBRAINZ_ALBUMID": "release-1", "TRACKNUMBER": "1/10",
		}},
		"/downloads/album/02.flac": {Tags: mediatool.Tags{
			"album": "Zuroveguv Wants to Dance", "artist": "Cibiz M",
			"musicbrainz_albumid": "release-1", "tracknumber": "2",
		}},
	}}

	reader, err := music.BuildInspectTagReader(t.Context(), tool, "/downloads/album",
		[]string{"01.flac", "02.flac"})
	if err != nil {
		t.Fatalf("BuildInspectTagReader: %v", err)
	}

	first := reader("01.flac")
	if !first.Found || first.ReleaseID != "release-1" || first.Album != "Zuroveguv Wants to Dance" ||
		first.AlbumArtist != "Cibiz M" || first.TrackNumber != 1 {
		t.Errorf("first = %+v, want Found tags matching the fixture (track 1, not 1/10 literally)", first)
	}
	second := reader("02.flac")
	if !second.Found || second.ReleaseID != "release-1" || second.TrackNumber != 2 {
		t.Errorf("second = %+v, want the lowercase-keyed tags read the same way", second)
	}

	if got := reader("no-such-file.flac"); got.Found {
		t.Errorf("reader for an unprobed path = %+v, want Found=false", got)
	}
}

func TestBuildInspectTagReader_AFailedProbeIsNotFoundNotEmpty(t *testing.T) {
	tool := fakeTagTool{byPath: map[string]mediatool.Probe{
		"/downloads/album/corrupt.flac": {Err: errors.New("not a media file")},
	}}

	reader, err := music.BuildInspectTagReader(t.Context(), tool, "/downloads/album",
		[]string{"corrupt.flac"})
	if err != nil {
		t.Fatalf("BuildInspectTagReader: %v", err)
	}
	if got := reader("corrupt.flac"); got.Found {
		t.Errorf("a failed probe = %+v, want Found=false -- not evidence of an untagged file", got)
	}
}
