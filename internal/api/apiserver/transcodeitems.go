package apiserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/settings"
)

// The refusals a TranscodePipeline answers.
var (
	ErrTranscodeNoItem        = errors.New("apiserver: no such transcode item")
	ErrTranscodeOriginalGone  = errors.New("apiserver: the original is no longer kept")
	ErrTranscodeNotRestorable = errors.New("apiserver: only a converted file can be restored")
	ErrTranscodePlaceTaken    = errors.New("apiserver: something else sits where the original was")
	ErrTranscodeFolder        = errors.New("apiserver: the library's folder is not reachable")
	ErrTranscodeBadRetry      = errors.New("apiserver: the retry settings are not valid")
)

// TranscodeCountsView is how many files are in each state.
type TranscodeCountsView struct{ Needed, Running, Done, NotNeeded, Failed, Blocked int64 }

// TranscodeSavingsView is what conversion saved.
type TranscodeSavingsView struct{ Files, OriginalBytes, ResultBytes, EncodeSeconds int64 }

// TranscodeLibraryView is one library's line of the summary.
type TranscodeLibraryView struct {
	CategoryID, TargetID int64
	Name                 string
	Enabled              bool
	Counts               TranscodeCountsView
	Savings              TranscodeSavingsView
	Warnings             []settings.TranscodeWarning
}

// TranscodeHealthView is how one kind of conversion is doing.
type TranscodeHealthView struct {
	Kind, Mode, LastError string
	ConsecutiveFailures   int64
	BackoffUntil          time.Time
}

// TranscodeSummaryView is the transcode page's numbers.
type TranscodeSummaryView struct {
	Enabled        bool
	Libraries      []TranscodeLibraryView
	Totals         TranscodeSavingsView
	Counts         TranscodeCountsView
	Health         []TranscodeHealthView
	OriginalsBytes int64
	// OriginalsCount is how many originals the bin keeps; OriginalsNextExpiry is zero when none.
	OriginalsCount      int64
	OriginalsNextExpiry time.Time
	DiskFullRoots       int64
}

// TranscodeItemView is one judged file; the reasons, warnings and plan are the JSON the pipeline stored.
type TranscodeItemView struct {
	MediaObjectID, CategoryID                                         int64
	Path, Kind, State, Lane                                           string
	ReasonsJSON, WarningsJSON, PlanJSON                               string
	Attempts, Pass                                                    int64
	Interrupted                                                       bool
	FailureCode, LastError, Engine                                    string
	OriginalSizeBytes, ResultSizeBytes, ResultRatioPct, EncodeSeconds int64
	FinishedAt                                                        int64
	Restorable                                                        bool
	RestorableUntil                                                   time.Time
}

// TranscodeItemsQuery selects a page of items; AfterID is the cursor.
type TranscodeItemsQuery struct {
	State, Kind, Search       string
	CategoryID, AfterID, Size int64
}

// TranscodeRetryQuery is a retry: the ids or the filter, the files left out, the other settings and whether to only count.
type TranscodeRetryQuery struct {
	IDs, Excluded       []int64
	State, Kind, Search string
	CategoryID          int64
	Quality             float64
	VideoBitrateKbps    int64
	Preset, Engine      string
	Preview             bool
}

// TranscodePipeline is what the transcode page reads and changes, implemented over the pipeline in cmd/solarr/adapters.
type TranscodePipeline interface {
	Summary(ctx context.Context) (TranscodeSummaryView, error)
	Items(ctx context.Context, query TranscodeItemsQuery) ([]TranscodeItemView, error)
	Item(ctx context.Context, id int64) (TranscodeItemView, bool, error)
	Retry(ctx context.Context, query TranscodeRetryQuery) (int64, error)
	Restore(ctx context.Context, id int64) error
	ResumeHealth(ctx context.Context, kind string) error
}

// transcodePipeline answers the pipeline, or writes the 503 and answers false; the caller has checked the administrator.
func (s *Server) transcodePipeline(w http.ResponseWriter) (TranscodePipeline, bool) {
	if s.transcodeItems == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "conversion is not available")
		return nil, false
	}
	return s.transcodeItems, true
}

func transcodeCounts(counts TranscodeCountsView) gen.TranscodeCounts {
	return gen.TranscodeCounts{Needed: counts.Needed, Running: counts.Running, Done: counts.Done, NotNeeded: counts.NotNeeded, Failed: counts.Failed, Blocked: counts.Blocked}
}

func transcodeSavings(savings TranscodeSavingsView) gen.TranscodeSavings {
	out := gen.TranscodeSavings{
		Files: savings.Files, OriginalBytes: savings.OriginalBytes, ResultBytes: savings.ResultBytes, SavedBytes: savings.OriginalBytes - savings.ResultBytes,
		EncodeSeconds: savings.EncodeSeconds,
	}
	if savings.OriginalBytes > 0 {
		out.SavedPct = out.SavedBytes * 100 / savings.OriginalBytes
	}
	return out
}

// GetTranscodeSummary answers the counts, savings, health and warnings. Administrator only.
func (s *Server) GetTranscodeSummary(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	pipeline, ok := s.transcodePipeline(w)
	if !ok {
		return
	}
	summary, err := pipeline.Summary(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	out := gen.TranscodeSummary{
		Enabled: summary.Enabled, Libraries: []gen.TranscodeLibrarySummary{}, Totals: transcodeSavings(summary.Totals), Counts: transcodeCounts(summary.Counts),
		Health: []gen.TranscodeHealth{}, OriginalsBytes: summary.OriginalsBytes,
		OriginalsCount: summary.OriginalsCount, DiskFullRoots: summary.DiskFullRoots,
	}
	if !summary.OriginalsNextExpiry.IsZero() {
		expiry := summary.OriginalsNextExpiry
		out.OriginalsNextExpiry = &expiry
	}
	for _, library := range summary.Libraries {
		one := gen.TranscodeLibrarySummary{
			CategoryId: library.CategoryID, Name: library.Name, Enabled: library.Enabled, Counts: transcodeCounts(library.Counts), Savings: transcodeSavings(library.Savings),
		}
		if library.TargetID != 0 {
			one.TargetId = &library.TargetID
		}
		warnings := []gen.TranscodeWarning{}
		for _, warning := range library.Warnings {
			item := gen.TranscodeWarning{Code: gen.TranscodeWarningCode(warning.Code)}
			if warning.Value != "" {
				value := warning.Value
				item.Value = &value
			}
			warnings = append(warnings, item)
		}
		one.Warnings = &warnings
		out.Libraries = append(out.Libraries, one)
	}
	for _, health := range summary.Health {
		one := gen.TranscodeHealth{Kind: gen.TranscodeHealthKind(health.Kind), Mode: gen.TranscodeHealthMode(health.Mode), ConsecutiveFailures: health.ConsecutiveFailures}
		if !health.BackoffUntil.IsZero() {
			until := health.BackoffUntil
			one.BackoffUntil = &until
		}
		if health.LastError != "" {
			message := health.LastError
			one.LastError = &message
		}
		out.Health = append(out.Health, one)
	}
	writeJSON(w, s.logger, http.StatusOK, out)
}

func transcodeReasons(raw string) []gen.TranscodeReason {
	reasons := []gen.TranscodeReason{}
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &reasons)
	}
	return reasons
}

func transcodeItem(view TranscodeItemView) gen.TranscodeItem {
	out := gen.TranscodeItem{
		MediaObjectId: view.MediaObjectID, CategoryId: view.CategoryID, Path: view.Path, Kind: gen.TranscodeItemKind(view.Kind), State: gen.TranscodeItemState(view.State),
		Reasons: transcodeReasons(view.ReasonsJSON), Attempts: view.Attempts, Pass: view.Pass, Interrupted: view.Interrupted, Restorable: view.Restorable,
	}
	lane := gen.TranscodeItemLane(view.Lane)
	out.Lane = &lane
	warnings := transcodeReasons(view.WarningsJSON)
	out.Warnings = &warnings
	if view.PlanJSON != "" {
		var plan map[string]any
		if json.Unmarshal([]byte(view.PlanJSON), &plan) == nil {
			out.Plan = &plan
		}
	}
	if view.FailureCode != "" {
		out.FailureCode = &view.FailureCode
	}
	if view.LastError != "" {
		out.LastError = &view.LastError
	}
	if view.Engine != "" {
		out.Engine = &view.Engine
	}
	out.OriginalSizeBytes, out.ResultSizeBytes, out.ResultRatioPct, out.EncodeSeconds = &view.OriginalSizeBytes, &view.ResultSizeBytes, &view.ResultRatioPct, &view.EncodeSeconds
	if !view.RestorableUntil.IsZero() {
		until := view.RestorableUntil
		out.RestorableUntil = &until
	}
	if view.FinishedAt != 0 {
		finished := time.Unix(view.FinishedAt, 0).UTC()
		out.FinishedAt = &finished
	}
	return out
}

// transcodeItemsLimit bounds one page of items.
const (
	transcodeItemsDefault = 100
	transcodeItemsMax     = 500
)

// ListTranscodeItems answers one page of judged files by id. Administrator only.
func (s *Server) ListTranscodeItems(w http.ResponseWriter, r *http.Request, params gen.ListTranscodeItemsParams) {
	if !s.requireAdministrator(w, r) {
		return
	}
	pipeline, ok := s.transcodePipeline(w)
	if !ok {
		return
	}
	query := TranscodeItemsQuery{Size: transcodeItemsDefault}
	if params.Limit != nil {
		query.Size = int64(min(max(*params.Limit, 1), transcodeItemsMax))
	}
	if params.State != nil {
		query.State = string(*params.State)
	}
	if params.Kind != nil {
		query.Kind = string(*params.Kind)
	}
	if params.CategoryId != nil {
		query.CategoryID = *params.CategoryId
	}
	if params.Q != nil {
		query.Search = *params.Q
	}
	if params.Cursor != nil && *params.Cursor != "" {
		after, err := strconv.ParseInt(*params.Cursor, 10, 64)
		if err != nil || after < 0 {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "the cursor is not one this list gave")
			return
		}
		query.AfterID = after
	}
	// One more than a page tells whether there is a next one.
	query.Size++
	views, err := pipeline.Items(r.Context(), query)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	out := gen.TranscodeItemPage{Items: []gen.TranscodeItem{}}
	if int64(len(views)) >= query.Size {
		views = views[:query.Size-1]
		cursor := strconv.FormatInt(views[len(views)-1].MediaObjectID, 10)
		out.NextCursor = &cursor
	}
	for _, view := range views {
		out.Items = append(out.Items, transcodeItem(view))
	}
	writeJSON(w, s.logger, http.StatusOK, out)
}

// GetTranscodeItem answers one judged file with its plan. Administrator only.
func (s *Server) GetTranscodeItem(w http.ResponseWriter, r *http.Request, mediaObjectID int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	pipeline, ok := s.transcodePipeline(w)
	if !ok {
		return
	}
	view, found, err := pipeline.Item(r.Context(), mediaObjectID)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if !found {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such file among the judged ones")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, transcodeItem(view))
}

// RetryTranscodeItems gives failed and blocked files another chance, or counts them. Administrator only.
func (s *Server) RetryTranscodeItems(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	pipeline, ok := s.transcodePipeline(w)
	if !ok {
		return
	}
	var body gen.TranscodeRetryRequest
	if !s.decodeBody(w, r, maxUserBody, &body, "expected a JSON object") {
		return
	}
	query := TranscodeRetryQuery{}
	if body.Ids != nil {
		query.IDs = *body.Ids
	}
	if body.ExcludedIds != nil {
		query.Excluded = *body.ExcludedIds
	}
	if body.State != nil {
		query.State = string(*body.State)
	}
	if body.Kind != nil {
		query.Kind = string(*body.Kind)
	}
	if body.Q != nil {
		query.Search = *body.Q
	}
	if body.CategoryId != nil {
		query.CategoryID = *body.CategoryId
	}
	if body.Preview != nil {
		query.Preview = *body.Preview
	}
	if override := body.Override; override != nil {
		if override.Quality != nil {
			query.Quality = float64(*override.Quality)
		}
		if override.VideoBitrateKbps != nil {
			query.VideoBitrateKbps = *override.VideoBitrateKbps
		}
		if override.Preset != nil {
			query.Preset = string(*override.Preset)
		}
		if override.Engine != nil {
			query.Engine = string(*override.Engine)
		}
	}
	count, err := pipeline.Retry(r.Context(), query)
	if errors.Is(err, ErrTranscodeBadRetry) {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "the retry settings are not valid: quality and bitrate cannot be negative, and the preset and engine must be known names")
		return
	}
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, gen.TranscodeRetryResult{Count: count, Applied: !query.Preview})
}

// RestoreTranscodeItem puts a converted file's original back. Administrator only.
func (s *Server) RestoreTranscodeItem(w http.ResponseWriter, r *http.Request, mediaObjectID int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	pipeline, ok := s.transcodePipeline(w)
	if !ok {
		return
	}
	err := pipeline.Restore(r.Context(), mediaObjectID)
	switch {
	case errors.Is(err, ErrTranscodeNoItem):
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such file among the judged ones")
		return
	case errors.Is(err, ErrTranscodeOriginalGone):
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict, "Original no longer kept", "the original was removed from the recycle bin, so it cannot come back")
		return
	case errors.Is(err, ErrTranscodeNotRestorable):
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict, "Not converted", "only a converted file has an original to restore")
		return
	case errors.Is(err, ErrTranscodePlaceTaken):
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict, "Path taken", "another file sits where the original was; move it away first")
		return
	case errors.Is(err, ErrTranscodeFolder):
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "the library's folder is not reachable or not writable")
		return
	case err != nil:
		writeError(w, s.logger, err)
		return
	}
	view, found, err := pipeline.Item(r.Context(), mediaObjectID)
	if err != nil || !found {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, transcodeItem(view))
}

// ResumeTranscodeKind ends a kind's backoff or probing. Administrator only.
func (s *Server) ResumeTranscodeKind(w http.ResponseWriter, r *http.Request, kind gen.ResumeTranscodeKindParamsKind) {
	if !s.requireAdministrator(w, r) {
		return
	}
	pipeline, ok := s.transcodePipeline(w)
	if !ok {
		return
	}
	if kind != gen.ResumeTranscodeKindParamsKindVideo && kind != gen.ResumeTranscodeKindParamsKindAudio {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "the kind is video or audio")
		return
	}
	if err := pipeline.ResumeHealth(r.Context(), string(kind)); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
