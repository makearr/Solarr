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
	// fpcalcSeconds matches what AcoustID itself fingerprints (its own docs and
	// DroppedNeedle's recipe both use two minutes).
	fpcalcSeconds = "120"
	ffmpegBinary  = "ffmpeg"
	// tagBinary is the mutagen helper the image carries (revision D3, solarr-tag beside this file).
	tagBinary = "solarr-tag"
)

// maxEmbeddedPictureBytes bounds one extracted picture, the same ceiling the
// artwork jobs put on a downloaded image.
const maxEmbeddedPictureBytes = 20 << 20

// defaultParallelism is how many tool processes one Subprocess runs at once, across every
// concurrent call: a NAS served 7-12 cold probes a second at 8-32 and thrashed beyond (2026-09-27).
const defaultParallelism = 16

// perFileTimeout bounds one invocation. A file on a stalled network mount must not
// hold the batch open indefinitely -- section 15.2's shutdown deadline is 30s.
const perFileTimeout = 20 * time.Second

// Subprocess is the ADR-30 Tool backed by the tools the image carries.
type Subprocess struct {
	runner Runner
	// slots bound the processes running at once, shared by concurrent calls: storage pays per file.
	slots chan struct{}
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
	return &Subprocess{runner: runner, slots: make(chan struct{}, parallelism)}
}

// Available reports every missing tool at once rather than the first, so an operator
// installs them in one pass (the same rule as ADR-44's config validation).
func (s *Subprocess) Available(_ context.Context) error {
	var missing []error
	for _, binary := range ToolNames() {
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

// maxTagBatch bounds one helper run: its edits travel as one argument, and one stalled file
// holds the whole run.
const maxTagBatch = 200

type tagResult struct {
	Path    string   `json:"path"`
	Changed []string `json:"changed"`
	Error   string   `json:"error,omitempty"`
}

// WriteTags sets tags through the helper, maxTagBatch files per run.
func (s *Subprocess) WriteTags(ctx context.Context, edits []TagEdit) ([]TagResult, error) {
	if len(edits) == 0 {
		return nil, nil
	}
	if err := s.runner.Lookup(tagBinary); err != nil {
		return nil, err
	}
	outcomes := make([]TagResult, 0, len(edits))
	for start := 0; start < len(edits); start += maxTagBatch {
		batch := edits[start:min(start+maxTagBatch, len(edits))]
		results, err := s.writeBatch(ctx, batch)
		if err != nil {
			return nil, err
		}
		outcomes = append(outcomes, results...)
	}
	return outcomes, nil
}

func (s *Subprocess) writeBatch(ctx context.Context, edits []TagEdit) ([]TagResult, error) {
	type edit struct {
		Path  string `json:"path"`
		Fill  Tags   `json:"fill,omitempty"`
		Set   Tags   `json:"set,omitempty"`
		Cover string `json:"cover,omitempty"`
	}
	payload := make([]edit, len(edits))
	for i, one := range edits {
		payload[i] = edit(one)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(len(edits))*perFileTimeout)
	defer cancel()
	s.slots <- struct{}{}
	output, err := s.runner.Run(runCtx, tagBinary, string(encoded))
	<-s.slots
	if err != nil {
		return nil, err
	}
	var results []tagResult
	if err := json.Unmarshal(output, &results); err != nil || len(results) != len(edits) {
		return nil, fmt.Errorf("mediatool: %s answered %d results for %d files: %w", tagBinary, len(results), len(edits), err)
	}
	outcomes := make([]TagResult, len(edits))
	for i, result := range results {
		outcomes[i].Changed = result.Changed
		if result.Error != "" {
			outcomes[i].Err = fmt.Errorf("mediatool: write tags to %s: %s", result.Path, result.Error)
		}
	}
	return outcomes, nil
}

// eachInParallel runs work over count indices, at most the Subprocess's slots at a time across
// every call. Each call writes its own index, so the results need no lock.
func (s *Subprocess) eachInParallel(ctx context.Context, count int, work func(index int)) {
	var running sync.WaitGroup
	defer running.Wait()
	for index := range count {
		select {
		case s.slots <- struct{}{}:
		case <-ctx.Done():
			return
		}
		running.Go(func() {
			defer func() { <-s.slots }()
			work(index)
		})
	}
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

	// -length bounds how much audio is decoded. AcoustID matches on the first
	// two minutes, so decoding a whole 70-minute file buys nothing and costs
	// real time on a library-wide pass. Not -raw: AcoustID wants the compressed
	// form, and -raw is for callers doing their own comparison.
	output, runErr := s.runner.Run(bounded, fpcalcBinary, "-length", fpcalcSeconds, "-json", path)

	var decoded struct {
		Duration    float64 `json:"duration"`
		Fingerprint string  `json:"fingerprint"`
	}
	decodeErr := json.Unmarshal(output, &decoded)

	// fpcalc exits non-zero on a file it could only partly decode while still
	// printing a usable fingerprint. Throwing that away loses exactly the
	// damaged files fingerprinting exists to identify, so a fingerprint that
	// parsed is kept and the failure is reported as Partial instead.
	if decodeErr == nil && decoded.Fingerprint != "" {
		return Fingerprint{
			Path:        path,
			Duration:    time.Duration(decoded.Duration * float64(time.Second)),
			Chromaprint: decoded.Fingerprint,
			Partial:     runErr != nil,
		}
	}
	if runErr != nil {
		return Fingerprint{Path: path, Err: runErr}
	}
	if decodeErr != nil {
		return Fingerprint{Path: path, Err: fmt.Errorf("mediatool: fingerprint %s: %w", path, decodeErr)}
	}
	return Fingerprint{Path: path, Err: fmt.Errorf("mediatool: fingerprint %s: fpcalc returned none", path)}
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
		AvgFrameRate     string            `json:"avg_frame_rate"`
		RFrameRate       string            `json:"r_frame_rate"`
		FieldOrder       string            `json:"field_order"`
		ColorTransfer    string            `json:"color_transfer"`
		ChannelLayout    string            `json:"channel_layout"`
		Disposition      struct {
			AttachedPic int `json:"attached_pic"`
		} `json:"disposition"`
		SideDataList []struct {
			SideDataType string `json:"side_data_type"`
		} `json:"side_data_list"`
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
	var streamLanguages, subtitleLanguages []string
	for _, stream := range decoded.Streams {
		mergeTags(tags, stream.Tags)
		switch stream.CodecType {
		case "audio":
			streamLanguages = append(streamLanguages, stream.Tags["language"])
			if measured.AudioCodec == "" {
				measured.AudioCodec = stream.CodecName
				measured.AudioBitrate = parseInt(stream.BitRate)
				measured.SampleRate = int(parseInt(stream.SampleRate))
				measured.BitDepth = int(parseInt(stream.BitsPerRawSample))
				measured.Channels = stream.Channels
				measured.ChannelLayout = channelLayout(stream.ChannelLayout, stream.Channels)
			}
		case "subtitle":
			if language := normaliseLanguage(stream.Tags["language"]); language != "" {
				subtitleLanguages = append(subtitleLanguages, language)
			}
		case "video":
			// A cover picture is a video stream to ffprobe; it is not the file's picture.
			if measured.VideoCodec == "" && stream.Disposition.AttachedPic == 0 {
				measured.VideoCodec = stream.CodecName
				measured.VideoBitrate = parseInt(stream.BitRate)
				measured.Width = stream.Width
				measured.Height = stream.Height
				measured.FrameRate = frameRate(stream.AvgFrameRate, stream.RFrameRate)
				measured.ScanType = scanType(stream.FieldOrder)
				measured.HDR = hdrOf(stream.ColorTransfer, func(kind string) bool {
					for _, side := range stream.SideDataList {
						if strings.Contains(strings.ToLower(side.SideDataType), kind) {
							return true
						}
					}
					return false
				})
			}
		}
		// A container with no format-level duration still has one per stream.
		if measured.Duration == 0 {
			measured.Duration = parseSeconds(stream.Duration)
		}
	}
	mergeTags(tags, decoded.Format.Tags)
	measured.SubtitleLanguages = uniqueStrings(subtitleLanguages)
	measured.AudioLanguages = audioLanguages(streamLanguages, tags)
	measured.LyricsLanguages = lyricsLanguages(tags)

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
func ToolNames() []string { return []string{ffprobeBinary, fpcalcBinary, ffmpegBinary, tagBinary} }

// EmbeddedPicture copies the first picture stream a file carries (its cover art)
// out unchanged, as the JPEG or PNG it was stored as. ErrNoEmbeddedPicture means
// the file has none, which is ordinary rather than a failure.
func (s *Subprocess) EmbeddedPicture(ctx context.Context, path string) ([]byte, error) {
	bounded, cancel := context.WithTimeout(ctx, perFileTimeout)
	defer cancel()

	output, err := s.runner.Run(bounded, ffmpegBinary,
		"-v", "error", "-i", path, "-an", "-map", "0:v:0?", "-c:v", "copy", "-frames:v", "1",
		"-f", "image2pipe", "pipe:1")
	if err != nil {
		// The optional map leaves no output stream at all for a file with no picture.
		if strings.Contains(err.Error(), "does not contain any stream") {
			return nil, ErrNoEmbeddedPicture
		}
		return nil, fmt.Errorf("mediatool: picture %s: %w", path, err)
	}
	if len(output) == 0 {
		return nil, ErrNoEmbeddedPicture
	}
	if len(output) > maxEmbeddedPictureBytes {
		return nil, fmt.Errorf("mediatool: picture %s: %d bytes exceeds %d", path, len(output), maxEmbeddedPictureBytes)
	}
	return output, nil
}
