package apiserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/config"
	"github.com/makearr/Solarr/internal/platform/transcode"
)

// PlaybackLocator finds a file the built-in player may open. Defined here and implemented in
// cmd/solarr/adapters, so this package never imports the library store.
type PlaybackLocator interface {
	// LocatePlayback answers ErrNoSuchFile for an id with no readable path.
	LocatePlayback(ctx context.Context, mediaObjectID int64) (PlaybackFile, error)
}

// PlaybackFile is a file's place and what the probe measured of it.
type PlaybackFile struct {
	MediaObjectID int64
	AbsolutePath  string
	RootFolderID  int64
	// Kind is "audio" or "video" when the library classified it, else empty and the extension decides.
	Kind                              string
	DurationMs                        int64
	Container, VideoCodec, AudioCodec string
}

var (
	audioExtensions = []string{".mp3", ".flac", ".ogg", ".oga", ".opus", ".m4a", ".aac", ".wav", ".wma", ".ape", ".wv", ".alac", ".aiff", ".aif", ".dsf", ".mka"}
	videoExtensions = []string{".mkv", ".mp4", ".m4v", ".avi", ".mov", ".webm", ".wmv", ".ts", ".m2ts", ".mpg", ".mpeg", ".flv", ".ogv"}

	// What every current browser decodes from a plain file, by container and codec.
	directAudioExtensions = []string{".mp3", ".flac", ".ogg", ".oga", ".opus", ".m4a", ".aac", ".wav"}
	directVideoExtensions = []string{".mp4", ".m4v", ".webm"}
	directVideoCodecs     = []string{"h264", "avc", "vp8", "vp9", "av1"}
	directAudioCodecs     = []string{"aac", "mp3", "opus", "vorbis", "flac", "pcm"}
)

// playbackKind is audio or video from the library's classification, else from the extension; "" when it is neither.
func playbackKind(file PlaybackFile) string {
	if file.Kind == "audio" || file.Kind == "video" {
		return file.Kind
	}
	extension := strings.ToLower(path.Ext(file.AbsolutePath))
	switch {
	case slices.Contains(audioExtensions, extension):
		return "audio"
	case slices.Contains(videoExtensions, extension):
		return "video"
	}
	return ""
}

// directPlayLikely says whether a browser plays the file as it is. Unknown codecs count as
// playable: the player falls back to a conversion the moment the browser refuses.
func directPlayLikely(file PlaybackFile, kind string) bool {
	extension := strings.ToLower(path.Ext(file.AbsolutePath))
	if kind == "audio" {
		return slices.Contains(directAudioExtensions, extension) && !codecIn(file.AudioCodec, "alac", "wma", "dts", "ac3")
	}
	if !slices.Contains(directVideoExtensions, extension) {
		return false
	}
	return (file.VideoCodec == "" || codecIn(file.VideoCodec, directVideoCodecs...)) &&
		(file.AudioCodec == "" || codecIn(file.AudioCodec, directAudioCodecs...))
}

func codecIn(codec string, names ...string) bool {
	codec = strings.ToLower(codec)
	return slices.ContainsFunc(names, func(name string) bool { return strings.Contains(codec, name) })
}

// playbackFile finds the file for the caller or writes the 404 the caller gets for a file it cannot read (ADR-27).
func (s *Server) playbackFile(w http.ResponseWriter, r *http.Request) (PlaybackFile, string, bool) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return PlaybackFile{}, "", false
	}
	mediaObjectID, err := strconv.ParseInt(chi.URLParam(r, "mediaObjectId"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "a media object id must be a number")
		return PlaybackFile{}, "", false
	}
	if s.playback == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "the player is not available")
		return PlaybackFile{}, "", false
	}
	file, err := s.playback.LocatePlayback(r.Context(), mediaObjectID)
	if errors.Is(err, ErrNoSuchFile) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such file")
		return PlaybackFile{}, "", false
	}
	if err != nil {
		writeError(w, s.logger, err)
		return PlaybackFile{}, "", false
	}
	visibleRoots, _ := s.visibleAt(access, settings.PermissionRead)
	if !slices.Contains(visibleRoots, file.RootFolderID) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such file")
		return PlaybackFile{}, "", false
	}
	kind := playbackKind(file)
	if kind == "" {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "this file is neither audio nor video")
		return PlaybackFile{}, "", false
	}
	return file, kind, true
}

func (s *Server) transcodeAvailable() bool {
	return s.transcoder != nil && s.auth.Settings.Snapshot().StreamingSettings().TranscodeEnabled && s.transcoder.Available()
}

// The three generated entry points; the id is read from the path by playbackFile.
func (s *Server) GetFilePlayback(w http.ResponseWriter, r *http.Request, _ int64) {
	s.routeGetFilePlayback(w, r)
}
func (s *Server) StreamFile(w http.ResponseWriter, r *http.Request, _ int64) { s.routeStreamFile(w, r) }
func (s *Server) TranscodeFile(w http.ResponseWriter, r *http.Request, _ int64, _ gen.TranscodeFileParams) {
	s.routeTranscodeFile(w, r)
}

func (s *Server) routeGetFilePlayback(w http.ResponseWriter, r *http.Request) {
	file, kind, ok := s.playbackFile(w, r)
	if !ok {
		return
	}
	view := gen.FilePlayback{
		MediaObjectId: file.MediaObjectID, Kind: gen.FilePlaybackKind(kind),
		DirectPlayLikely: directPlayLikely(file, kind), TranscodeAvailable: s.transcodeAvailable(),
	}
	if file.DurationMs > 0 {
		view.DurationMs = &file.DurationMs
	}
	view.Container = nonEmpty(file.Container)
	view.VideoCodec = nonEmpty(file.VideoCodec)
	view.AudioCodec = nonEmpty(file.AudioCodec)
	writeJSON(w, s.logger, http.StatusOK, view)
}

func nonEmpty(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// routeStreamFile serves the bytes with Range support, which is all a browser needs to seek.
func (s *Server) routeStreamFile(w http.ResponseWriter, r *http.Request) {
	file, _, ok := s.playbackFile(w, r)
	if !ok {
		return
	}
	opened, err := s.fsys.Open(file.AbsolutePath)
	if err != nil {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "the file is not on disk")
		return
	}
	defer func() { _ = opened.Close() }()
	info, err := s.fsys.Stat(file.AbsolutePath)
	if err != nil {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "the file is not on disk")
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, filepath.Base(file.AbsolutePath), info.ModTime, opened)
}

// routeTranscodeFile streams ffmpeg's output as it is made; the viewer leaving cancels the request context and with it ffmpeg.
func (s *Server) routeTranscodeFile(w http.ResponseWriter, r *http.Request) {
	file, kind, ok := s.playbackFile(w, r)
	if !ok {
		return
	}
	if !s.transcodeAvailable() {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable",
			"transcoding is off or ffmpeg is not installed")
		return
	}
	start, err := parseStart(r.URL.Query().Get("start"))
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "start must be a number of seconds")
		return
	}

	policy := s.auth.Settings.Snapshot().StreamingSettings()
	resolution := s.transcoder.Resolve(policy.Hardware, policy.Device)
	spec := transcode.Spec{
		Kind: transcode.Kind(kind), Path: file.AbsolutePath, StartSecond: start,
		Accel: resolution.Accel, Device: resolution.Device, MaxHeight: policy.MaxHeight, VideoKbps: policy.VideoKbps,
	}

	w.Header().Set("Content-Type", spec.ContentType())
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	flushing := &flushWriter{w: w, flusher: http.NewResponseController(w)}
	// Headers go out with the first byte, so a refusal before that is still a proper status.
	err = s.transcoder.Stream(r.Context(), flushing, spec, policy.MaxSessions)
	switch {
	case err == nil:
	case errors.Is(err, transcode.ErrBusy) && !flushing.started:
		writeProblem(w, s.logger, http.StatusTooManyRequests, TypeUnavailable, "Busy", "every conversion slot is in use")
	case errors.Is(err, transcode.ErrUnavailable) && !flushing.started:
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "ffmpeg is not installed")
	case !flushing.started:
		s.logger.Warn("transcode failed", "file", file.AbsolutePath, "error", err)
		writeProblem(w, s.logger, http.StatusBadGateway, TypeUnavailable, "Conversion failed", "ffmpeg could not convert this file")
	default:
		s.logger.Warn("transcode ended early", "file", file.AbsolutePath, "error", err)
	}
}

func parseStart(raw string) (float64, error) {
	if raw == "" {
		return 0, nil
	}
	start, err := strconv.ParseFloat(raw, 64)
	if err != nil || start < 0 || start > 48*3600 {
		return 0, errors.New("bad start")
	}
	return start, nil
}

// flushWriter pushes every chunk out at once: a transcode is a live stream, not a body that is ready.
type flushWriter struct {
	w       http.ResponseWriter
	flusher *http.ResponseController
	started bool
}

func (f *flushWriter) Write(p []byte) (int, error) {
	f.started = true
	n, err := f.w.Write(p)
	_ = f.flusher.Flush()
	return n, err
}

// streamingReport is the policy from the cache, the detection from the service and the encoder that would run.
func (s *Server) streamingReport() gen.StreamingReport {
	policy := s.auth.Settings.Snapshot().StreamingSettings()
	report := gen.StreamingReport{
		Settings: gen.StreamingSettings{
			TranscodeEnabled: policy.TranscodeEnabled, Hardware: gen.StreamingSettingsHardware(policy.Hardware),
			MaxHeight: policy.MaxHeight, VideoKbps: policy.VideoKbps, MaxSessions: policy.MaxSessions,
			Device: nonEmpty(policy.Device),
		},
		Detected: []gen.StreamingEncoder{},
	}
	if s.transcoder == nil {
		report.Effective.Accel = gen.StreamingReportEffectiveAccelSoftware
		return report
	}
	report.FfmpegAvailable = s.transcoder.Available()
	report.ActiveSessions = s.transcoder.Active()
	for _, capability := range s.transcoder.Capabilities() {
		report.Detected = append(report.Detected, gen.StreamingEncoder{
			Accel: gen.StreamingEncoderAccel(capability.Accel), Device: nonEmpty(capability.Device),
			Name: nonEmpty(capability.Name), Working: capability.Working, Detail: nonEmpty(capability.Detail),
		})
	}
	resolution := s.transcoder.Resolve(policy.Hardware, policy.Device)
	report.Effective.Accel = gen.StreamingReportEffectiveAccel(resolution.Accel)
	report.Effective.Device = nonEmpty(resolution.Device)
	report.Effective.Note = nonEmpty(resolution.Note)
	return report
}

// GetStreaming answers the policy, what detection found and what would run.
func (s *Server) GetStreaming(w http.ResponseWriter, r *http.Request) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, s.streamingReport())
}

// UpdateStreaming replaces the policy. Administrator only.
func (s *Server) UpdateStreaming(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	var body gen.UpdateStreamingJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "the streaming settings could not be read")
		return
	}
	if err := s.auth.Registry.LookupAll(settings.KeyStreamingTranscodeEnabled, settings.KeyStreamingHardware,
		settings.KeyStreamingDevice, settings.KeyStreamingMaxHeight, settings.KeyStreamingVideoKbps,
		settings.KeyStreamingMaxSessions); err != nil {
		writeError(w, s.logger, err)
		return
	}
	err := s.auth.Settings.SetStreamingSettings(r.Context(), s.auth.Clock, settings.StreamingSettings{
		TranscodeEnabled: body.TranscodeEnabled, Hardware: string(body.Hardware),
		Device: strings.TrimSpace(derefOr(body.Device, "")), MaxHeight: body.MaxHeight,
		VideoKbps: body.VideoKbps, MaxSessions: body.MaxSessions,
	})
	var problems config.Problems
	if errors.As(err, &problems) {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"The streaming settings have problems", problems.Report("the streaming settings", "Nothing was changed."))
		return
	}
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, s.streamingReport())
}

// DetectStreamingHardware tries every encoder again. Administrator only.
func (s *Server) DetectStreamingHardware(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.transcoder != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		s.transcoder.Detect(ctx)
	}
	writeJSON(w, s.logger, http.StatusOK, s.streamingReport())
}
