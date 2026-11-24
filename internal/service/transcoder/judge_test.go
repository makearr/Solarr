package transcoder_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/platform/transcode"
	"github.com/makearr/Solarr/internal/service/transcoder"
)

func TestJudge_ANeededPlanStoresItsLaneReasonsAndPlanAndReadsBack(t *testing.T) {
	plan := encodePlan()
	plan.Reasons = []transcoder.Reason{{Code: transcoder.ReasonContainer, Stream: -1, Want: "mp4", Have: "matroska"}}
	got, err := transcoder.Judge(plan, transcode.QSV)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "needed" || got.Lane != "gpu" || got.ReasonsJSON != `[{"code":"container","stream":-1,"want":"mp4","have":"matroska"}]` || got.WarningsJSON != "" {
		t.Errorf("judgement = %+v", got)
	}
	back, err := transcoder.ParsePlan(got.PlanJSON)
	if err != nil || back.Verdict != transcoder.Needed || len(back.Video) != 1 || back.Video[0].BitrateKbps != 2000 {
		t.Errorf("plan read back = %+v, %v", back, err)
	}
	if soft, _ := transcoder.Judge(plan, transcode.Software); soft.Lane != "cpu" {
		t.Errorf("software lane = %s", soft.Lane)
	}
	audioOnly := transcoder.Plan{Verdict: transcoder.Needed, EncodesAudio: true}
	if got, _ := transcoder.Judge(audioOnly, transcode.QSV); got.Lane != "audio" {
		t.Errorf("audio lane = %s", got.Lane)
	}
}

func TestJudge_NotNeededAndBlockedStoreTheirReasonsButNoLaneOrPlan(t *testing.T) {
	blocked := transcoder.Plan{Verdict: transcoder.Blocked, Reasons: []transcoder.Reason{{Code: transcoder.ReasonNoStreams, Stream: -1}}}
	got, _ := transcoder.Judge(blocked, transcode.Software)
	if got.State != "blocked" || got.Lane != "" || got.PlanJSON != "" || got.ReasonsJSON != `[{"code":"no_streams","stream":-1}]` {
		t.Errorf("blocked = %+v", got)
	}
	skipped := transcoder.Plan{Verdict: transcoder.NotNeeded, Warnings: []transcoder.Reason{{Code: transcoder.ReasonRangeUnknown, Stream: -1}}}
	if got, _ := transcoder.Judge(skipped, transcode.Software); got.State != "not_needed" || got.ReasonsJSON != "" || got.WarningsJSON == "" {
		t.Errorf("not needed = %+v", got)
	}
	if plan, err := transcoder.ParsePlan(""); err != nil || plan.Verdict != "" {
		t.Errorf("empty plan = %+v, %v", plan, err)
	}
	if _, err := transcoder.ParsePlan("{"); err == nil {
		t.Error("garbage accepted")
	}
}
