package mediatool_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// fakeRunner answers by tool and argument, and counts what it was asked to do, so a
// test can assert the batching rather than just the parsing.
type fakeRunner struct {
	missing map[string]bool
	// respond returns the output for one invocation, keyed on the last argument,
	// which is the path in every call this package makes.
	respond func(name, path string) ([]byte, error)
	// record, when set, receives each invocation's full argument list.
	record func(args []string)

	mu       sync.Mutex
	calls    int
	inFlight int32
	peak     int32
}

func (f *fakeRunner) Lookup(name string) error {
	if f.missing[name] {
		return fmt.Errorf("%w: %s", mediatool.ErrToolMissing, name)
	}
	return nil
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	running := atomic.AddInt32(&f.inFlight, 1)
	for {
		peak := atomic.LoadInt32(&f.peak)
		if running <= peak || atomic.CompareAndSwapInt32(&f.peak, peak, running) {
			break
		}
	}
	// Long enough that concurrent calls actually overlap, short enough not to
	// slow the suite.
	timer := time.NewTimer(2 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
	atomic.AddInt32(&f.inFlight, -1)

	f.mu.Lock()
	f.calls++
	if f.record != nil {
		f.record(args)
	}
	f.mu.Unlock()
	return f.respond(name, args[len(args)-1])
}

const flacProbe = `{
  "streams": [{"codec_name":"flac","codec_type":"audio","bit_rate":"705600",
               "sample_rate":"44100","channels":2,"duration":"215.000000"}],
  "format": {"format_name":"flac","duration":"215.000000","bit_rate":"705600",
             "tags":{"MUSICBRAINZ_TRACKID":"track-mbid","ARTIST":"Nick Cave",
                     "ALBUM":"Tender Prey"}}
}`

func probeReplying(output string) func(string, string) ([]byte, error) {
	return func(_, _ string) ([]byte, error) { return []byte(output), nil }
}

func TestReadTags_LowercasesKeysSoTheContainerDoesNotLeak(t *testing.T) {
	runner := &fakeRunner{respond: probeReplying(flacProbe)}
	probes, err := mediatool.New(runner, 1).ReadTags(t.Context(), []string{"/music/a.flac"})
	if err != nil {
		t.Fatalf("ReadTags: %v", err)
	}
	if len(probes) != 1 {
		t.Fatalf("got %d probes, want 1", len(probes))
	}

	// Vorbis spells it ARTIST and ID3 spells it artist. A caller reads one name.
	if got := probes[0].Tags["artist"]; got != "Nick Cave" {
		t.Errorf("tags[artist] = %q, want Nick Cave", got)
	}
	if got := probes[0].Tags.First("musicbrainz_trackid"); got != "track-mbid" {
		t.Errorf("First(musicbrainz_trackid) = %q, want track-mbid", got)
	}
	if _, uppercase := probes[0].Tags["ARTIST"]; uppercase {
		t.Error("the uppercase key survived, so callers would have to try both")
	}
}

func TestReadTags_ReportsWhatWasMeasuredNotWhatWasClaimed(t *testing.T) {
	runner := &fakeRunner{respond: probeReplying(flacProbe)}
	probes, err := mediatool.New(runner, 1).ReadTags(t.Context(), []string{"/music/a.flac"})
	if err != nil {
		t.Fatalf("ReadTags: %v", err)
	}

	// ADR-39: these are the facts that override a release name's claims.
	want := mediatool.Measured{
		FormatName: "flac", Duration: 215 * time.Second,
		OverallBitrate: 705600, AudioBitrate: 705600,
		AudioCodec: "flac", SampleRate: 44100, Channels: 2,
	}
	if probes[0].Measured != want {
		t.Errorf("Measured = %+v, want %+v", probes[0].Measured, want)
	}
}

// TestReadTags_ReportsBitDepthWhenFfprobeHasOne (todo 4.2): ffprobe's own
// bits_per_raw_sample is the one field that gives a real, measured bit depth,
// distinct from a release name's claim -- meaningful for a lossless stream,
// absent for a lossy one that decodes to float internally.
func TestReadTags_ReportsBitDepthWhenFfprobeHasOne(t *testing.T) {
	runner := &fakeRunner{respond: probeReplying(`{
	  "streams": [{"codec_name":"flac","codec_type":"audio","bit_rate":"705600",
	               "sample_rate":"44100","channels":2,"bits_per_raw_sample":"24"}],
	  "format": {"format_name":"flac","bit_rate":"705600"}
	}`)}
	probes, err := mediatool.New(runner, 1).ReadTags(t.Context(), []string{"/music/a.flac"})
	if err != nil {
		t.Fatalf("ReadTags: %v", err)
	}
	if probes[0].Measured.BitDepth != 24 {
		t.Errorf("BitDepth = %d, want 24", probes[0].Measured.BitDepth)
	}
}

// Vorbis comments land at format level and are the authoritative copy, so a
// same-named stream tag must not shadow them.
func TestReadTags_PrefersAFormatTagOverAStreamTagOfTheSameName(t *testing.T) {
	runner := &fakeRunner{respond: probeReplying(`{
	  "streams": [{"codec_type":"audio","codec_name":"flac",
	               "tags":{"ARTIST":"from the stream","ENCODER":"only on the stream"}}],
	  "format": {"format_name":"flac","tags":{"ARTIST":"from the format"}}
	}`)}

	probes, err := mediatool.New(runner, 1).ReadTags(t.Context(), []string{"/m/a.flac"})
	if err != nil {
		t.Fatalf("ReadTags: %v", err)
	}
	if got := probes[0].Tags["artist"]; got != "from the format" {
		t.Errorf("tags[artist] = %q, want the format-level value", got)
	}
	// A stream-only tag is still kept: preferring one is not discarding the other.
	if got := probes[0].Tags["encoder"]; got != "only on the stream" {
		t.Errorf("tags[encoder] = %q, want the stream-only value kept", got)
	}
}

func TestReadTags_ReturnsOneResultPerPathInTheOrderAsked(t *testing.T) {
	// Each file answers with its own artist, so a mismatch shows as the wrong
	// value rather than as an equal-looking result.
	runner := &fakeRunner{respond: func(_, path string) ([]byte, error) {
		return []byte(fmt.Sprintf(`{"format":{"format_name":"flac","tags":{"ARTIST":%q}}}`, path)), nil
	}}
	paths := []string{"/m/c.flac", "/m/a.flac", "/m/b.flac"}

	probes, err := mediatool.New(runner, 4).ReadTags(t.Context(), paths)
	if err != nil {
		t.Fatalf("ReadTags: %v", err)
	}
	for index, probe := range probes {
		if probe.Path != paths[index] {
			t.Errorf("probes[%d].Path = %q, want %q", index, probe.Path, paths[index])
		}
		if got := probe.Tags["artist"]; got != paths[index] {
			t.Errorf("probes[%d] carries %q, want the result for %q", index, got, paths[index])
		}
	}
}

// At 500k files there is always an unreadable one. It must not take the batch with it.
func TestReadTags_KeepsTheBatchWhenOneFileFails(t *testing.T) {
	broken := errors.New("moov atom not found")
	runner := &fakeRunner{respond: func(_, path string) ([]byte, error) {
		if strings.HasSuffix(path, "bad.m4a") {
			return nil, broken
		}
		return []byte(flacProbe), nil
	}}

	probes, err := mediatool.New(runner, 2).ReadTags(t.Context(),
		[]string{"/m/good.flac", "/m/bad.m4a", "/m/also-good.flac"})
	if err != nil {
		t.Fatalf("ReadTags returned a batch error for one bad file: %v", err)
	}
	if probes[0].Err != nil || probes[2].Err != nil {
		t.Errorf("a good file carried an error: %v, %v", probes[0].Err, probes[2].Err)
	}
	if !errors.Is(probes[1].Err, broken) {
		t.Errorf("probes[1].Err = %v, want the underlying %v", probes[1].Err, broken)
	}
	if probes[1].Path != "/m/bad.m4a" {
		t.Errorf("the failed result lost its path: %q", probes[1].Path)
	}
}

// ADR-28 rules out a synchronous subprocess per file on a caller's hot path. ffprobe
// takes one file per invocation, so the batch's concurrency is the whole mechanism.
func TestReadTags_RunsTheBatchConcurrentlyUpToTheLimit(t *testing.T) {
	runner := &fakeRunner{respond: probeReplying(flacProbe)}
	paths := make([]string, 40)
	for index := range paths {
		paths[index] = fmt.Sprintf("/m/%d.flac", index)
	}

	if _, err := mediatool.New(runner, 4).ReadTags(t.Context(), paths); err != nil {
		t.Fatalf("ReadTags: %v", err)
	}
	if runner.calls != len(paths) {
		t.Errorf("ran %d invocations for %d files", runner.calls, len(paths))
	}
	peak := atomic.LoadInt32(&runner.peak)
	if peak > 4 {
		t.Errorf("peak concurrency %d, want at most the configured 4", peak)
	}
	if peak < 2 {
		t.Errorf("peak concurrency %d: the batch ran serially", peak)
	}
}

// Directories are scanned side by side; the limit is on the storage's load, so it holds across calls.
func TestReadTags_ConcurrentBatchesShareTheLimit(t *testing.T) {
	runner := &fakeRunner{respond: probeReplying(flacProbe)}
	tool := mediatool.New(runner, 4)
	var batches sync.WaitGroup
	for batch := range 4 {
		paths := make([]string, 10)
		for index := range paths {
			paths[index] = fmt.Sprintf("/m/%d/%d.flac", batch, index)
		}
		batches.Go(func() {
			if _, err := tool.ReadTags(t.Context(), paths); err != nil {
				t.Errorf("ReadTags: %v", err)
			}
		})
	}
	batches.Wait()
	if peak := atomic.LoadInt32(&runner.peak); peak > 4 {
		t.Errorf("peak concurrency %d across batches, want at most the configured 4", peak)
	}
}

func TestReadTags_WithNoPathsRunsNothing(t *testing.T) {
	runner := &fakeRunner{respond: probeReplying(flacProbe)}
	probes, err := mediatool.New(runner, 4).ReadTags(t.Context(), nil)
	if err != nil || probes != nil {
		t.Fatalf("ReadTags(nil) = %v, %v; want nil, nil", probes, err)
	}
	if runner.calls != 0 {
		t.Errorf("ran %d invocations for no files", runner.calls)
	}
}

func TestReadTags_RefusesTheBatchWhenTheToolIsMissing(t *testing.T) {
	runner := &fakeRunner{missing: map[string]bool{"ffprobe": true}, respond: probeReplying(flacProbe)}
	_, err := mediatool.New(runner, 1).ReadTags(t.Context(), []string{"/m/a.flac"})
	if !errors.Is(err, mediatool.ErrToolMissing) {
		t.Fatalf("ReadTags = %v, want ErrToolMissing", err)
	}
	// A missing tool is reported before anything runs, not once per file.
	if runner.calls != 0 {
		t.Errorf("ran %d invocations with the tool missing", runner.calls)
	}
}

// ADR-44's rule, applied to tools: report every problem at once, not the first.
func TestAvailable_NamesEveryMissingTool(t *testing.T) {
	runner := &fakeRunner{
		missing: map[string]bool{"ffprobe": true, "fpcalc": true, "ffmpeg": true, "solarr-tag": true},
		respond: probeReplying(flacProbe),
	}
	err := mediatool.New(runner, 1).Available(t.Context())
	if err == nil {
		t.Fatal("Available = nil with every tool missing")
	}
	for _, tool := range mediatool.ToolNames() {
		if !strings.Contains(err.Error(), tool) {
			t.Errorf("Available did not name %s: %v", tool, err)
		}
	}
}

func TestAvailable_PassesWhenBothToolsAreThere(t *testing.T) {
	runner := &fakeRunner{respond: probeReplying(flacProbe)}
	if err := mediatool.New(runner, 1).Available(t.Context()); err != nil {
		t.Errorf("Available = %v, want nil", err)
	}
}

func TestFingerprint_ReadsWhatFpcalcReported(t *testing.T) {
	runner := &fakeRunner{respond: func(name, _ string) ([]byte, error) {
		if name != "fpcalc" {
			t.Errorf("fingerprinting ran %q", name)
		}
		return []byte(`{"duration":215.23,"fingerprint":"AQADtE..."}`), nil
	}}

	prints, err := mediatool.New(runner, 1).Fingerprint(t.Context(), []string{"/m/a.flac"})
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	if prints[0].Chromaprint != "AQADtE..." || prints[0].Path != "/m/a.flac" {
		t.Errorf("got %+v, want the fingerprint and the path asked for", prints[0])
	}
	if prints[0].Duration != 215230*time.Millisecond {
		t.Errorf("Duration = %s, want 215.23s", prints[0].Duration)
	}
}

// fpcalc exits 0 having printed nothing for some inputs, which would otherwise be
// stored as a valid empty fingerprint that matches every other empty one.
func TestFingerprint_TreatsAnEmptyResultAsAFailure(t *testing.T) {
	runner := &fakeRunner{respond: func(string, string) ([]byte, error) {
		return []byte(`{"duration":215.23,"fingerprint":""}`), nil
	}}
	prints, err := mediatool.New(runner, 1).Fingerprint(t.Context(), []string{"/m/a.flac"})
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	if prints[0].Err == nil {
		t.Error("an empty fingerprint was accepted")
	}
}

// The helper gets every edit as one JSON argument and answers per file: one file it cannot
// tag fails alone, not the batch (revision D3).
func TestWriteTags_ReportsEachFileOnItsOwn(t *testing.T) {
	var sent []string
	runner := &fakeRunner{
		record: func(args []string) { sent = args },
		respond: func(name, _ string) ([]byte, error) {
			if name != "solarr-tag" {
				return nil, fmt.Errorf("unexpected tool %s", name)
			}
			return []byte(`[{"path":"/m/a.m4a","changed":["musicbrainz_releasegroupid","cover"]},` +
				`{"path":"/m/b.txt","changed":[],"error":"ValueError: not audio"}]`), nil
		},
	}
	results, err := mediatool.New(runner, 1).WriteTags(t.Context(), []mediatool.TagEdit{
		{Path: "/m/a.m4a", Fill: mediatool.Tags{"musicbrainz_releasegroupid": "rg-1"}, Cover: "/c/work-1.jpg"},
		{Path: "/m/b.txt", Set: mediatool.Tags{"musicbrainz_albumid": "rel-1"}},
	})
	if err != nil {
		t.Fatalf("WriteTags: %v", err)
	}
	want := `[{"path":"/m/a.m4a","fill":{"musicbrainz_releasegroupid":"rg-1"},"cover":"/c/work-1.jpg"},` +
		`{"path":"/m/b.txt","set":{"musicbrainz_albumid":"rel-1"}}]`
	if len(sent) != 1 || sent[0] != want {
		t.Errorf("sent %q, want %q", sent, want)
	}
	if len(results) != 2 || results[0].Err != nil ||
		fmt.Sprint(results[0].Changed) != "[musicbrainz_releasegroupid cover]" ||
		results[1].Err == nil || results[1].Err.Error() != "mediatool: write tags to /m/b.txt: ValueError: not audio" {
		t.Errorf("results = %+v, want the first changed and only the second failed", results)
	}
}

// Without the helper nothing is written and the reason is the missing tool, not a silent no-op.
func TestWriteTags_MissingHelperFailsTheBatch(t *testing.T) {
	tool := mediatool.New(&fakeRunner{missing: map[string]bool{"solarr-tag": true}}, 1)
	_, err := tool.WriteTags(t.Context(), []mediatool.TagEdit{{Path: "/m/a.flac", Set: mediatool.Tags{"artist": "x"}}})
	if !errors.Is(err, mediatool.ErrToolMissing) {
		t.Errorf("WriteTags = %v, want ErrToolMissing", err)
	}
	if results, err := tool.WriteTags(t.Context(), nil); err != nil || results != nil {
		t.Errorf("WriteTags(nil) = %v, %v; want nothing", results, err)
	}
}

// A helper answering for fewer files than it was given is a broken run, not a partial success.
func TestWriteTags_AShortAnswerFailsTheBatch(t *testing.T) {
	runner := &fakeRunner{respond: func(string, string) ([]byte, error) { return []byte(`[]`), nil }}
	if _, err := mediatool.New(runner, 1).WriteTags(t.Context(), []mediatool.TagEdit{{Path: "/m/a.flac"}}); err == nil {
		t.Error("an answer for 0 of 1 files was accepted")
	}
}

// The same tag really does arrive spelled three ways, verified against ffprobe
// (documentation/media-tools.md). One lookup has to find all of them.
func TestTagsFirst_IgnoresHowTheNameIsPunctuated(t *testing.T) {
	for _, spelling := range []string{
		"musicbrainz_releasegroupid",   // Vorbis, as Picard writes it
		"musicbrainz release group id", // Vorbis, as kid3 writes its canonical name
		"musicbrainz release group id", // ID3 and MP4, lowercased from mixed case
	} {
		tags := mediatool.Tags{spelling: "rg-uuid"}
		if got := tags.First("MUSICBRAINZ_RELEASEGROUPID"); got != "rg-uuid" {
			t.Errorf("First on %q = %q, want rg-uuid", spelling, got)
		}
	}
	// Squeezing must not make different tags equal.
	tags := mediatool.Tags{"musicbrainz_albumid": "al", "musicbrainz_albumartistid": "aa"}
	if got := tags.First("MusicBrainz Album Id"); got != "al" {
		t.Errorf("First(album id) = %q, want al", got)
	}
}

func TestTagsFirst_TakesTheFirstNameThatIsPresentAndNotEmpty(t *testing.T) {
	tags := mediatool.Tags{"albumartist": "", "artist": "Nick Cave"}
	if got := tags.First("AlbumArtist", "Artist"); got != "Nick Cave" {
		t.Errorf("First = %q, want Nick Cave: an empty tag is not a value", got)
	}
	if got := tags.First("composer"); got != "" {
		t.Errorf("First(absent) = %q, want empty", got)
	}
}

func TestEmbeddedPicture_ReturnsThePictureBytes(t *testing.T) {
	jpeg := []byte{0xff, 0xd8, 0xff, 0xe0, 'J', 'F', 'I', 'F'}
	runner := &fakeRunner{respond: func(name, _ string) ([]byte, error) {
		if name != "ffmpeg" {
			return nil, fmt.Errorf("unexpected tool %s", name)
		}
		return jpeg, nil
	}}

	got, err := mediatool.New(runner, 1).EmbeddedPicture(t.Context(), "/music/a.m4a")
	if err != nil {
		t.Fatalf("EmbeddedPicture: %v", err)
	}
	if string(got) != string(jpeg) {
		t.Errorf("EmbeddedPicture = %x, want %x", got, jpeg)
	}
}

// ffmpeg's real wording for a file with no picture, captured against a
// generated AAC file with the same arguments this package passes.
func TestEmbeddedPicture_AFileWithNoPictureIsErrNoEmbeddedPicture(t *testing.T) {
	runner := &fakeRunner{respond: func(string, string) ([]byte, error) {
		return nil, errors.New("mediatool: ffmpeg: exit status 234: [out#0/image2pipe @ 0x59fd02ebfb80] " +
			"Output file does not contain any stream\nError opening output file pipe:1.")
	}}

	_, err := mediatool.New(runner, 1).EmbeddedPicture(t.Context(), "/music/a.m4a")
	if !errors.Is(err, mediatool.ErrNoEmbeddedPicture) {
		t.Errorf("EmbeddedPicture error = %v, want ErrNoEmbeddedPicture", err)
	}
}

func TestEmbeddedPicture_AnyOtherFailureIsAnError(t *testing.T) {
	runner := &fakeRunner{respond: func(string, string) ([]byte, error) {
		return nil, errors.New("mediatool: ffmpeg: exit status 1: /music/a.m4a: Permission denied")
	}}

	_, err := mediatool.New(runner, 1).EmbeddedPicture(t.Context(), "/music/a.m4a")
	if err == nil || errors.Is(err, mediatool.ErrNoEmbeddedPicture) {
		t.Errorf("EmbeddedPicture error = %v, want a real failure, not ErrNoEmbeddedPicture", err)
	}
}

// AcoustID matches on the first two minutes, so decoding a whole file buys
// nothing. -raw is deliberately not used: AcoustID wants the compressed form.
func TestFingerprint_BoundsHowMuchAudioIsDecoded(t *testing.T) {
	var arguments []string
	runner := &fakeRunner{
		respond: func(string, string) ([]byte, error) {
			return []byte(`{"duration":215.23,"fingerprint":"AQADtE..."}`), nil
		},
		record: func(args []string) { arguments = args },
	}
	if _, err := mediatool.New(runner, 1).Fingerprint(t.Context(), []string{"/m/a.flac"}); err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	if !slices.Contains(arguments, "-length") || !slices.Contains(arguments, "120") {
		t.Errorf("fpcalc args = %v, want -length 120", arguments)
	}
	if slices.Contains(arguments, "-raw") {
		t.Errorf("fpcalc args = %v, want the compressed fingerprint, not -raw", arguments)
	}
}

// fpcalc exits non-zero on a file it could only partly decode while still
// printing a usable fingerprint. Discarding that loses exactly the damaged
// files fingerprinting exists to identify.
func TestFingerprint_KeepsAFingerprintFromAFailedRun(t *testing.T) {
	runner := &fakeRunner{respond: func(string, string) ([]byte, error) {
		return []byte(`{"duration":215.23,"fingerprint":"AQADtE..."}`), errors.New("exit status 3")
	}}
	prints, err := mediatool.New(runner, 1).Fingerprint(t.Context(), []string{"/m/a.flac"})
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	if prints[0].Err != nil {
		t.Errorf("Err = %v, want the usable fingerprint kept", prints[0].Err)
	}
	if prints[0].Chromaprint != "AQADtE..." {
		t.Errorf("Chromaprint = %q, want it kept", prints[0].Chromaprint)
	}
	if !prints[0].Partial {
		t.Error("Partial = false, want the failure reported alongside the fingerprint")
	}
}

// A failed run that printed nothing usable is still a failure.
func TestFingerprint_AFailedRunWithNoFingerprintIsAnError(t *testing.T) {
	runner := &fakeRunner{respond: func(string, string) ([]byte, error) {
		return nil, errors.New("exit status 2")
	}}
	prints, err := mediatool.New(runner, 1).Fingerprint(t.Context(), []string{"/m/a.flac"})
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	if prints[0].Err == nil {
		t.Error("Err = nil, want the failure reported when nothing was produced")
	}
}
