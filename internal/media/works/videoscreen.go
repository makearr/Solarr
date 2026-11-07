package works

import (
	"context"

	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/importing/rules"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// LargestFile is the unit's biggest file, which is the work for a one-file type (a film, a scene); the rest are extras and samples.
func LargestFile(media []inspect.File) *inspect.File {
	var best *inspect.File
	for index := range media {
		if best == nil || media[index].SizeBytes > best.SizeBytes {
			best = &media[index]
		}
	}
	return best
}

// ScreenVideo holds a one-file video's file against the checks made before an import: still being unpacked, one part of
// several, a sample by its runtime, no audio track. A nil probe skips the last two, and a file the probe could not read
// is let through, the tool being optional. expectedMinutes 0 means defaultMinutes. nil is a pass.
func ScreenVideo(ctx context.Context, probe func(ctx context.Context, absolutePath string) (mediatool.Measured, bool),
	sourceAbs string, siblings []string, expectedMinutes, defaultMinutes int,
) *model.Rejection {
	for _, rejection := range []*model.Rejection{rules.Unpacking(sourceAbs, false), rules.MultiPart(sourceAbs, siblings)} {
		if rejection != nil {
			return rejection
		}
	}
	if probe == nil {
		return nil
	}
	measured, ok := probe(ctx, sourceAbs)
	if expectedMinutes == 0 {
		expectedMinutes = defaultMinutes
	}
	if verdict := rules.Sample(rules.SampleFacts{Path: sourceAbs, Runtime: measured.Duration, Probed: ok, ExpectedMinutes: expectedMinutes}); verdict == rules.VerdictSample {
		return rules.RejectSample(verdict, false)
	}
	audioStreams := 0
	if measured.AudioCodec != "" {
		audioStreams = 1
	}
	return rules.HasAudio(ok && measured.VideoCodec != "", audioStreams)
}
