package music_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// fakeTagTool answers ReadTags from a fixed table, keyed by the exact path
// asked for -- Probe.Path is not read by BuildInspectTagGrouper, matching
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

// groupFolder runs the import grouper over one folder of tag sets named 00.flac, 01.flac, ...
func groupFolder(t *testing.T, tags ...mediatool.Tags) (inspect.TagGroups, bool) {
	t.Helper()
	probes := map[string]mediatool.Probe{}
	relPaths := make([]string, len(tags))
	for i, one := range tags {
		relPaths[i] = fmt.Sprintf("%02d.flac", i)
		probes["/in/"+relPaths[i]] = mediatool.Probe{Tags: one}
	}
	grouper, err := music.BuildInspectTagGrouper(t.Context(), fakeTagTool{byPath: probes}, "/in", relPaths)
	if err != nil {
		t.Fatalf("BuildInspectTagGrouper: %v", err)
	}
	return grouper(relPaths)
}

func repeatTags(count int, tags func(i int) mediatool.Tags) []mediatool.Tags {
	out := make([]mediatool.Tags, count)
	for i := range out {
		out[i] = tags(i)
	}
	return out
}

func TestBuildInspectTagGrouper_GroupsByTheRuleScanUses(t *testing.T) {
	album := func(name string) mediatool.Tags { return mediatool.Tags{"ALBUM": name, "ALBUMARTIST": "Ixu Band"} }
	release := mediatool.Tags{"album": "Gamma", "albumartist": "Ixu Band", "musicbrainz_albumid": "rel-1"}
	tagAgreement := inspect.Signal{Kind: inspect.SignalTagAgreement, Detail: "ixu band / alpha"}
	boundary := inspect.Signal{Kind: inspect.SignalDirectoryBoundary}
	for name, test := range map[string]struct {
		tags []mediatool.Tags
		want inspect.TagGroups
	}{
		"two albums in one flat folder are two groups": {
			[]mediatool.Tags{album("Alpha"), album("Beta"), album("Alpha")},
			inspect.TagGroups{Groups: [][]int{{0, 2}, {1}}, Signals: []inspect.Signal{tagAgreement, {Kind: inspect.SignalTagAgreement, Detail: "ixu band / beta"}}},
		},
		"files without a release id join the release of the same album": {
			[]mediatool.Tags{release, release, {"album": "gamma ", "albumartist": "Ixu Band"}},
			inspect.TagGroups{Groups: [][]int{{0, 1, 2}}, Signals: []inspect.Signal{{Kind: inspect.SignalEmbeddedID, Detail: "rel-1"}}},
		},
		"a disc marker in the album tag does not split a set": {
			[]mediatool.Tags{album("Alpha (Disc 1)"), album("Alpha (Disc 2)"), album("Alpha [CD 2]")},
			inspect.TagGroups{Groups: [][]int{{0, 1, 2}}, Signals: []inspect.Signal{tagAgreement}},
		},
		"an untagged file beside one album joins it": {
			[]mediatool.Tags{album("Alpha"), {}, album("Alpha")},
			inspect.TagGroups{Groups: [][]int{{0, 2, 1}}, Signals: []inspect.Signal{tagAgreement}},
		},
		"a file naming only an artist is untagged": {
			[]mediatool.Tags{album("Alpha"), {"artist": "Ixu Band"}},
			inspect.TagGroups{Groups: [][]int{{0, 1}}, Signals: []inspect.Signal{tagAgreement}},
		},
		"untagged files beside several albums are an ambiguous group of their own": {
			[]mediatool.Tags{album("Alpha"), album("Beta"), {}},
			inspect.TagGroups{Groups: [][]int{{0}, {1}, {2}}, Ambiguous: true,
				Signals: []inspect.Signal{tagAgreement, {Kind: inspect.SignalTagAgreement, Detail: "ixu band / beta"}, boundary}},
		},
		"a compilation without album artists is one album": {
			repeatTags(4, func(i int) mediatool.Tags {
				return mediatool.Tags{"album": "Invented Hits", "artist": fmt.Sprintf("Artist %d", i), "track": fmt.Sprint(i + 1)}
			}),
			inspect.TagGroups{Groups: [][]int{{0, 1, 2, 3}}, Signals: []inspect.Signal{{Kind: inspect.SignalTagAgreement, Detail: "artist 0 / invented hits"}}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := groupFolder(t, test.tags...)
			if !ok || !reflect.DeepEqual(got, test.want) {
				t.Errorf("groups = %+v, %v; want %+v", got, ok, test.want)
			}
		})
	}
}

func TestBuildInspectTagGrouper_NoAlbumTagsSayNothing(t *testing.T) {
	tool := fakeTagTool{byPath: map[string]mediatool.Probe{
		"/in/01.flac":      {Tags: mediatool.Tags{"artist": "Ixu Band", "tracknumber": "1"}},
		"/in/corrupt.flac": {Err: errors.New("not a media file"), Tags: mediatool.Tags{"album": "Alpha"}},
	}}
	grouper, err := music.BuildInspectTagGrouper(t.Context(), tool, "/in", []string{"01.flac", "corrupt.flac"})
	if err != nil {
		t.Fatalf("BuildInspectTagGrouper: %v", err)
	}
	if got, ok := grouper([]string{"01.flac", "corrupt.flac", "unprobed.flac"}); ok {
		t.Errorf("groups = %+v, want none: an artist alone, a failed probe and an unread file name no album", got)
	}
}

func TestImporterInspectTags_ReadsOnlyAudioAndNothingWhenThereIsNone(t *testing.T) {
	tool := fakeTagTool{byPath: map[string]mediatool.Probe{
		"/in/01.flac":   {Tags: mediatool.Tags{"ALBUM": "Wuvo", "ALBUMARTIST": "Cibiz M"}},
		"/in/movie.mkv": {Tags: mediatool.Tags{"ALBUM": "Other", "ALBUMARTIST": "Cibiz M"}},
	}}
	importer := music.NewImporter(music.ImporterConfig{Tool: tool})

	grouper, err := importer.InspectTags(t.Context(), "/in", []string{"01.flac", "cover.jpg", "movie.mkv"})
	if err != nil {
		t.Fatalf("InspectTags: %v", err)
	}
	want := inspect.TagGroups{Groups: [][]int{{0, 1, 2}}, Signals: []inspect.Signal{{Kind: inspect.SignalTagAgreement, Detail: "cibiz m / wuvo"}}}
	if got, ok := grouper([]string{"01.flac", "movie.mkv", "02.flac"}); !ok || !reflect.DeepEqual(got, want) {
		t.Errorf("groups = %+v, %v; want the audio file grouped and the video file unread", got, ok)
	}

	none, err := importer.InspectTags(t.Context(), "/in", []string{"movie.mkv"})
	if err != nil || none != nil {
		t.Errorf("no audio: grouper %v, error %v; want neither, so the walk probes nothing", none != nil, err)
	}
}
