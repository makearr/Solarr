package transcoder

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/transcode"
)

// AnalyzeJobType judges the files of one library against its target and setup.
const AnalyzeJobType = jobs.Type("transcode.analyze")

// AnalyzeDefinition declares the analysis: it reads the library's stored facts and writes only the item rows.
var AnalyzeDefinition = jobs.Definition{Name: "Judging files for conversion", Type: AnalyzeJobType, Pool: jobs.PoolFiles, MaxAttempts: 2}

// analyzePage is how many files one query and one write cover.
const analyzePage = 200

// AnalyzePayload names the library to judge.
type AnalyzePayload struct {
	CategoryID int64 `json:"category_id"`
}

// EnqueueAnalyze submits the analysis of one library.
func EnqueueAnalyze(ctx context.Context, queue Queue, categoryID int64) (jobs.EnqueueResult, error) {
	payload, err := json.Marshal(AnalyzePayload{CategoryID: categoryID})
	if err != nil {
		return jobs.EnqueueResult{}, fmt.Errorf("transcoder: analyse payload: %w", err)
	}
	return queue.Enqueue(ctx, jobs.Request{
		Type: AnalyzeJobType, ActionKey: "transcode.analyze:" + strconv.FormatInt(categoryID, 10), Priority: jobs.PriorityLow, Payload: payload, CategoryID: categoryID,
	})
}

// AnalyzeHandler adapts the analysis to the job queue.
type AnalyzeHandler struct{ Service *Service }

// Run reads the payload and judges the library.
func (h AnalyzeHandler) Run(ctx context.Context, job jobs.Job) error {
	var payload AnalyzePayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("transcoder: analyse payload: %w", err)
	}
	return h.Service.Analyze(ctx, payload.CategoryID, job.Progress)
}

// Analyze judges every audio and video file of the library that has no judgement for the current target and setup.
// A file the probe has not read yet is left for the next run.
func (s *Service) Analyze(ctx context.Context, categoryID int64, progress *jobs.Progress) error {
	if !s.Settings.Global().Enabled {
		return nil
	}
	libraries, err := s.Settings.Libraries(ctx)
	if err != nil {
		return err
	}
	var one *settings.TranscodeLibrary
	for _, candidate := range libraries {
		if candidate.CategoryID == categoryID && candidate.Enabled && candidate.Supported() {
			one = &candidate
		}
	}
	if one == nil {
		return nil
	}
	stored, err := s.Settings.Setup(ctx, one.Target.ID)
	if err != nil {
		return err
	}
	var rootIDs []int64
	for _, root := range s.Settings.RootFolders(categoryID) {
		rootIDs = append(rootIDs, root.ID)
	}
	wanted := WantedLanguages(*one.Target, s.Settings.Languages(categoryID))
	for afterID := int64(0); ; {
		page, err := s.Items.ObjectsToAnalyze(ctx, rootIDs, one.Target.ID, stored.Revision, afterID, analyzePage)
		if err != nil || len(page) == 0 {
			return err
		}
		afterID = page[len(page)-1].MediaObjectID
		ids := make([]int64, len(page))
		for i, object := range page {
			ids[i] = object.MediaObjectID
		}
		facts, err := s.Items.Facts(ctx, ids)
		if err != nil {
			return err
		}
		var judged []library.JudgedTranscodeItem
		for _, object := range page {
			known := facts[object.MediaObjectID]
			if !known.Measured {
				continue
			}
			file := FileFromMeasured(MeasuredOf(known), known.Container, known.SizeBytes, nil)
			plan := Analyze(file, *one.Target, stored.Setup, wanted)
			engine := s.laneEngine(plan, *one.Target, stored.Setup)
			judgement, err := Judge(plan, engine)
			if err != nil {
				return err
			}
			judged = append(judged, library.JudgedTranscodeItem{
				MediaObjectID: object.MediaObjectID, CategoryID: categoryID, TargetID: one.Target.ID, SetupRevision: stored.Revision, Kind: object.Kind,
				Lane: judgement.Lane, State: library.TranscodeState(judgement.State), ReasonsJSON: judgement.ReasonsJSON, PlanJSON: judgement.PlanJSON, WarningsJSON: judgement.WarningsJSON,
			})
		}
		if err := s.Items.Judge(ctx, judged); err != nil {
			return err
		}
		progress.Add(int64(len(page)))
		progress.Beat()
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}

// laneEngine is the engine a plan would encode its video with, which decides the lane; audio work and a missing encoder read as software.
func (s *Service) laneEngine(plan Plan, target settings.QualityTarget, setup settings.TranscodeSetup) transcode.Accel {
	codec, known := transcode.CodecNamed(target.VideoCodec)
	if !plan.EncodesVideo || !known {
		return transcode.Software
	}
	engines, err := ResolveEngines(s.Hardware, EngineQuery{Setup: setup, Codec: codec, TenBit: plan.tenBitVideo()})
	if err != nil {
		return transcode.Software
	}
	return engines[0]
}
