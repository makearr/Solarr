package mediatool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Tool names, in one place: they are what the image installs and what readiness
// reports as missing (documentation/media-tools.md).
const (
	ffprobeBinary = "ffprobe"
	fpcalcBinary  = "fpcalc"
)

// defaultParallelism is how many probes run at once. ffprobe takes one file per
// invocation, so a batch is only as fast as this: measured at 40 ms per file
// sequential and 5 ms at eight, against a 500k library (documentation/media-tools.md).
const defaultParallelism = 8

// perFileTimeout bounds one invocation. A file on a stalled network mount must not
// hold the batch open indefinitely -- section 15.2's shutdown deadline is 30s.
const perFileTimeout = 20 * time.Second

// Subprocess is the ADR-30 Tool backed by the tools the image carries.
type Subprocess struct {
	runner      Runner
	parallelism int
}

// New builds it. A nil runner takes the real one; parallelism at or below zero takes
// the measured default.
func New(runner Runner, parallelism int) *Subprocess {
	if runner == nil {
		runner = ExecRunner{}
	}
	if parallelism <= 0 {
		parallelism = defaultParallelism
	}
	return &Subprocess{runner: runner, parallelism: parallelism}
}

// Available reports every missing tool at once rather than the first, so an operator
// installs them in one pass (the same rule as ADR-44's config validation).
func (s *Subprocess) Available(_ context.Context) error {
	var missing []error
	for _, binary := range []string{ffprobeBinary, fpcalcBinary} {
		if err := s.runner.Lookup(binary); err != nil {
			missing = append(missing, err)
		}
	}
	return errors.Join(missing...)
}

// ReadTags probes every path and returns one result per path, in the order asked.
// A file that fails carries its own Err: one unreadable file does not fail a batch,
// because at 500k files there is always one.
func (s *Subprocess) ReadTags(ctx context.Context, paths []string) ([]Probe, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	if err := s.runner.Lookup(ffprobeBinary); err != nil {
		return nil, err
	}

	results := make([]Probe, len(paths))
	s.eachInParallel(ctx, len(paths), func(index int) {
		results[index] = s.probeOne(ctx, paths[index])
	})
	return results, ctx.Err()
}

// Fingerprint runs fpcalc over every path, one result per path, in order.
func (s *Subprocess) Fingerprint(ctx context.Context, paths []string) ([]Fingerprint, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	if err := s.runner.Lookup(fpcalcBinary); err != nil {
		return nil, err
	}

	results := make([]Fingerprint, len(paths))
	s.eachInParallel(ctx, len(paths), func(index int) {
		results[index] = s.fingerprintOne(ctx, paths[index])
	})
	return results, ctx.Err()
}

// WriteTags is not implemented. See ErrWriteUnsupported.
func (s *Subprocess) WriteTags(_ context.Context, edits []TagEdit) error {
	if len(edits) == 0 {
		return nil
	}
	return ErrWriteUnsupported
}

// eachInParallel runs work over count indices, at most parallelism at a time. The
// results slice is preallocated and each call writes its own index, so no lock is
// needed for the results themselves.
func (s *Subprocess) eachInParallel(ctx context.Context, count int, work func(index int)) {
	limit := min(s.parallelism, count)
	indices := make(chan int)
	var running sync.WaitGroup

	for range limit {
		running.Add(1)
		go func() {
			defer running.Done()
			for index := range indices {
				work(index)
			}
		}()
	}

	for index := range count {
		select {
		case indices <- index:
		case <-ctx.Done():
			close(indices)
			running.Wait()
			return
		}
	}
	close(indices)
	running.Wait()
}

func (s *Subprocess) probeOne(ctx context.Context, path string) Probe {
	bounded, cancel := context.WithTimeout(ctx, perFileTimeout)
	defer cancel()

	output, err := s.runner.Run(bounded, ffprobeBinary,
		"-v", "error", "-print_format", "json", "-show_format", "-show_streams", path)
	if err != nil {
		return Probe{Path: path, Err: err}
	}

	probe, err := parseProbe(output)
	if err != nil {
		return Probe{Path: path, Err: fmt.Errorf("mediatool: probe %s: %w", path, err)}
	}
	// The path asked for, never the filename ffprobe echoed: a tool's idea of the
	// name it was given is not something a result should be matched on.
	probe.Path = path
	return probe
}

func (s *Subprocess) fingerprintOne(ctx context.Context, path string) Fingerprint {
	bounded, cancel := context.WithTimeout(ctx, perFileTimeout)
	defer cancel()

	output, err := s.runner.Run(bounded, fpcalcBinary, "-json", path)
	if err != nil {
		return Fingerprint{Path: path, Err: err}
	}

	var decoded struct {
		Duration    float64 `json:"duration"`
		Fingerprint string  `json:"fingerprint"`
	}
	if err := json.Unmarshal(output, &decoded); err != nil {
		return Fingerprint{Path: path, Err: fmt.Errorf("mediatool: fingerprint %s: %w", path, err)}
	}
	if decoded.Fingerprint == "" {
		return Fingerprint{Path: path, Err: fmt.Errorf("mediatool: fingerprint %s: fpcalc returned none", path)}
	}
	return Fingerprint{
		Path:        path,
		Duration:    time.Duration(decoded.Duration * float64(time.Second)),
		Chromaprint: decoded.Fingerprint,
	}
}

// ffprobeOutput is only the fields that are used. ffprobe reports far more, and
// decoding the rest would make its every release a schema change here.
type ffprobeOutput struct {
	Format struct {
		FormatName string            `json:"format_name"`
		Duration   string            `json:"duration"`
		BitRate    string            `json:"bit_rate"`
		Tags       map[string]string `json:"tags"`
	} `json:"format"`
	Streams []struct {
		CodecName        string            `json:"codec_name"`
		CodecType        string            `json:"codec_type"`
		BitRate          string            `json:"bit_rate"`
		Duration         string            `json:"duration"`
		SampleRate       string            `json:"sample_rate"`
		BitsPerRawSample string            `json:"bits_per_raw_sample"`
		Channels         int               `json:"channels"`
		Width            int               `json:"width"`
		Height           int               `json:"height"`
		Tags             map[string]string `json:"tags"`
	} `json:"streams"`
}

func parseProbe(output []byte) (Probe, error) {
	var decoded ffprobeOutput
	if err := json.Unmarshal(output, &decoded); err != nil {
		return Probe{}, err
	}

	measured := Measured{
		FormatName:     decoded.Format.FormatName,
		Duration:       parseSeconds(decoded.Format.Duration),
		OverallBitrate: parseInt(decoded.Format.BitRate),
	}
	// Stream tags first, so a format-level tag of the same name wins: Vorbis
	// comments land at format level and are the authoritative copy.
	tags := Tags{}
	for _, stream := range decoded.Streams {
		mergeTags(tags, stream.Tags)
		switch stream.CodecType {
		case "audio":
			if measured.AudioCodec == "" {
				measured.AudioCodec = stream.CodecName
				measured.AudioBitrate = parseInt(stream.BitRate)
				measured.SampleRate = int(parseInt(stream.SampleRate))
				measured.BitDepth = int(parseInt(stream.BitsPerRawSample))
				measured.Channels = stream.Channels
			}
		case "video":
			if measured.VideoCodec == "" {
				measured.VideoCodec = stream.CodecName
				measured.VideoBitrate = parseInt(stream.BitRate)
				measured.Width = stream.Width
				measured.Height = stream.Height
			}
		}
		// A container with no format-level duration still has one per stream.
		if measured.Duration == 0 {
			measured.Duration = parseSeconds(stream.Duration)
		}
	}
	mergeTags(tags, decoded.Format.Tags)

	return Probe{Tags: tags, Measured: measured}, nil
}

// mergeTags lowercases every key, because the same tag is ARTIST in Vorbis comments
// and artist in ID3 and a caller must not have to know which it is reading.
func mergeTags(into Tags, from map[string]string) {
	for key, value := range from {
		into[strings.ToLower(key)] = value
	}
}

// ToolNames is what the image must carry and what readiness names when it does not.
func ToolNames() []string { return []string{ffprobeBinary, fpcalcBinary} }
