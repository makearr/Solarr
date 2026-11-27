package apiserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/config"
)

func transcodeLanes(limits settings.LaneLimits) gen.TranscodeLaneLimits {
	return gen.TranscodeLaneLimits{Cpu: int(limits.CPU), Gpu: int(limits.GPU), Audio: int(limits.Audio), Comic: int(limits.Comic), Ebook: int(limits.Ebook)}
}

func transcodeLimits(lanes gen.TranscodeLaneLimits) settings.LaneLimits {
	return settings.LaneLimits{CPU: int64(lanes.Cpu), GPU: int64(lanes.Gpu), Audio: int64(lanes.Audio), Comic: int64(lanes.Comic), Ebook: int64(lanes.Ebook)}
}

func (s *Server) transcodeSettings(r *http.Request) (gen.TranscodeSettings, error) {
	global := s.auth.Settings.Snapshot().TranscodeGlobal()
	out := gen.TranscodeSettings{
		Global: gen.TranscodeGlobalSettings{
			Enabled: global.Enabled, Parallel: transcodeLanes(global.Parallel), PlaybackParallel: transcodeLanes(global.PlaybackParallel),
			OriginalsRetainDays: int(global.Originals.RetainDays), OriginalsCapGb: int(global.Originals.CapGB),
		},
		Libraries: []gen.TranscodeLibrary{},
	}
	rows, err := s.auth.Settings.TranscodeLibraries(r.Context())
	if err != nil {
		return gen.TranscodeSettings{}, err
	}
	for _, row := range rows {
		order := gen.TranscodeLibraryOrder(row.Order)
		library := gen.TranscodeLibrary{
			CategoryId: row.CategoryID, Name: row.Name, MediaType: row.MediaType, Enabled: row.Enabled, Priority: int(row.Priority), Order: order, Supported: row.Supported(),
		}
		if target := row.Target; target != nil {
			id, name, kind := target.ID, target.Name, gen.TranscodeLibraryTargetKind(row.Kind())
			library.TargetId, library.TargetName, library.TargetKind = &id, &name, &kind
		}
		out.Libraries = append(out.Libraries, library)
	}
	return out, nil
}

// GetTranscodeSettings answers the transcode policy and every library's row. Administrator only.
func (s *Server) GetTranscodeSettings(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	body, err := s.transcodeSettings(r)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, body)
}

// SetTranscodeSettings saves the switch, the lane limits and the originals policy. Administrator only.
func (s *Server) SetTranscodeSettings(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	var body gen.TranscodeGlobalSettings
	if !s.decodeBody(w, r, maxUserBody, &body, "expected a JSON object") {
		return
	}
	if err := s.auth.Registry.LookupAll(
		settings.KeyTranscodeEnabled, settings.KeyTranscodeParallelCPU, settings.KeyTranscodeParallelGPU, settings.KeyTranscodeParallelAudio, settings.KeyTranscodeParallelComic, settings.KeyTranscodeParallelEbook,
		settings.KeyTranscodePlaybackParallelCPU, settings.KeyTranscodePlaybackParallelGPU, settings.KeyTranscodePlaybackParallelAudio, settings.KeyTranscodePlaybackParallelComic, settings.KeyTranscodePlaybackParallelEbook,
		settings.KeyTranscodeOriginalsRetainDays, settings.KeyTranscodeOriginalsCapGB,
	); err != nil {
		writeError(w, s.logger, err)
		return
	}
	global := settings.TranscodeGlobal{
		Enabled: body.Enabled, Parallel: transcodeLimits(body.Parallel), PlaybackParallel: transcodeLimits(body.PlaybackParallel),
		Originals: settings.OriginalsPolicy{RetainDays: int64(body.OriginalsRetainDays), CapGB: int64(body.OriginalsCapGb)},
	}
	if s.writeTranscodeProblem(w, s.auth.Settings.SetTranscodeGlobal(r.Context(), s.auth.Clock, global)) {
		return
	}
	out, err := s.transcodeSettings(r)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, out)
}

// writeTranscodeProblem answers a settings error and reports whether it did; nil is no error.
func (s *Server) writeTranscodeProblem(w http.ResponseWriter, err error) bool {
	var problems config.Problems
	switch {
	case err == nil:
		return false
	case errors.As(err, &problems):
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", problems.Report("the transcode settings", "Nothing was changed."))
	case errors.Is(err, settings.ErrNotFound):
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such library or quality target")
	default:
		writeError(w, s.logger, err)
	}
	return true
}

// SetTranscodeLibrary saves one library's row; a library without an audio or video target is refused. Administrator only.
func (s *Server) SetTranscodeLibrary(w http.ResponseWriter, r *http.Request, categoryID int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	var body gen.TranscodeLibraryRow
	if !s.decodeBody(w, r, maxUserBody, &body, "expected a JSON object") {
		return
	}
	libraries, err := s.auth.Settings.TranscodeLibraries(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	var current *settings.TranscodeLibrary
	for index := range libraries {
		if libraries[index].CategoryID == categoryID {
			current = &libraries[index]
		}
	}
	if current == nil {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such library")
		return
	}
	if !current.Supported() {
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict, "Not supported",
			"transcoding needs a library with an audio, video or comics quality target")
		return
	}
	row := settings.CategoryTranscode{CategoryID: categoryID, Enabled: body.Enabled, Priority: int64(body.Priority), Order: settings.TranscodeOrder(body.Order)}
	if s.writeTranscodeProblem(w, s.auth.Settings.SetCategoryTranscode(r.Context(), row)) {
		return
	}
	all, err := s.transcodeSettings(r)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	for _, library := range all.Libraries {
		if library.CategoryId == categoryID {
			writeJSON(w, s.logger, http.StatusOK, library)
			return
		}
	}
	writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such library")
}

// transcodeSetupState answers a target's setup as the API shape; the typed document goes through JSON, whose names both sides share.
func (s *Server) transcodeSetupState(r *http.Request, targetID int64) (gen.TranscodeSetupState, error) {
	stored, err := s.auth.Settings.TranscodeSetup(r.Context(), targetID)
	if err != nil {
		return gen.TranscodeSetupState{}, err
	}
	target, _ := s.auth.Settings.Snapshot().QualityTarget(targetID)
	raw, err := json.Marshal(stored.Setup)
	if err != nil {
		return gen.TranscodeSetupState{}, err
	}
	out := gen.TranscodeSetupState{
		TargetId: targetID, IsDefault: !stored.Stored, Revision: stored.Revision, Warnings: []gen.TranscodeWarning{}, Availability: s.transcodeAvailability(target.VideoCodec), Filters: map[string]bool{},
	}
	if err := json.Unmarshal(raw, &out.Setup); err != nil {
		return gen.TranscodeSetupState{}, err
	}
	if s.transcoder != nil {
		out.Filters = s.transcoder.Filters()
	}
	for _, warning := range settings.TranscodeWarnings(stored.Setup, target) {
		one := gen.TranscodeWarning{Code: gen.TranscodeWarningCode(warning.Code)}
		if warning.Value != "" {
			value := warning.Value
			one.Value = &value
		}
		out.Warnings = append(out.Warnings, one)
	}
	return out, nil
}

// GetTranscodeSetup answers a target's setup, the generated default until one is saved. Administrator only.
func (s *Server) GetTranscodeSetup(w http.ResponseWriter, r *http.Request, targetID int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	s.writeTranscodeSetup(w, r, targetID)
}

func (s *Server) writeTranscodeSetup(w http.ResponseWriter, r *http.Request, targetID int64) {
	out, err := s.transcodeSetupState(r, targetID)
	if s.writeTranscodeProblem(w, err) {
		return
	}
	writeJSON(w, s.logger, http.StatusOK, out)
}

// SetTranscodeSetup saves a target's setup over its default: a missing field keeps the default, an unknown one is refused. Administrator only.
func (s *Server) SetTranscodeSetup(w http.ResponseWriter, r *http.Request, targetID int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	target, found := s.auth.Settings.Snapshot().QualityTarget(targetID)
	if !found {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such quality target")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxUserBody))
	if err != nil {
		writeProblem(w, s.logger, http.StatusRequestEntityTooLarge, TypePayloadTooLarge, "Request too large", "the request body is larger than this endpoint accepts")
		return
	}
	setup, err := settings.ParseTranscodeSetup(raw, target)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "expected a transcode setup document: "+err.Error())
		return
	}
	if s.writeTranscodeProblem(w, s.auth.Settings.SetTranscodeSetup(r.Context(), s.auth.Clock, targetID, setup)) {
		return
	}
	s.writeTranscodeSetup(w, r, targetID)
}

// ResetTranscodeSetup writes a target's generated default over its setup. Administrator only.
func (s *Server) ResetTranscodeSetup(w http.ResponseWriter, r *http.Request, targetID int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.writeTranscodeProblem(w, s.auth.Settings.ResetTranscodeSetup(r.Context(), s.auth.Clock, targetID)) {
		return
	}
	s.writeTranscodeSetup(w, r, targetID)
}
