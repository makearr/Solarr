package transcoder

import (
	"encoding/json"
	"fmt"

	"github.com/makearr/Solarr/internal/platform/transcode"
)

// Judgement is a plan in the shape the item row stores it.
type Judgement struct {
	// State is needed, not_needed or blocked, the names the item table uses.
	State                               string
	Lane                                string
	ReasonsJSON, PlanJSON, WarningsJSON string
}

// Judge flattens a plan for storage; engine is the one the file would be encoded with, which decides the lane.
func Judge(plan Plan, engine transcode.Accel) (Judgement, error) {
	judgement := Judgement{State: string(plan.Verdict)}
	// The item row's lane column holds only cpu, gpu or audio; a comic's or an ebook's own lane follows from its kind.
	if plan.Verdict == Needed {
		judgement.Lane = string(plan.Lane(engine))
		if plan.Fixed != "" {
			judgement.Lane = string(LaneAudio)
		}
	}
	for _, field := range []struct {
		into  *string
		value any
		empty bool
	}{
		{&judgement.ReasonsJSON, plan.Reasons, len(plan.Reasons) == 0},
		{&judgement.WarningsJSON, plan.Warnings, len(plan.Warnings) == 0},
		{&judgement.PlanJSON, plan, plan.Verdict != Needed},
	} {
		if field.empty {
			continue
		}
		encoded, err := json.Marshal(field.value)
		if err != nil {
			return Judgement{}, fmt.Errorf("transcoder: store the plan: %w", err)
		}
		*field.into = string(encoded)
	}
	return judgement, nil
}

// ParsePlan reads a stored plan back; an empty document is no plan.
func ParsePlan(stored string) (Plan, error) {
	var plan Plan
	if stored == "" {
		return plan, nil
	}
	if err := json.Unmarshal([]byte(stored), &plan); err != nil {
		return Plan{}, fmt.Errorf("transcoder: read the stored plan: %w", err)
	}
	return plan, nil
}
