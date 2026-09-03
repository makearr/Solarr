package score

import (
	"encoding/json"
	"testing"
	"time"
)

func TestParse_DefaultsBitrateWeightsToOne(t *testing.T) {
	profile, err := Parse([]byte(`{"quality_profile": "no-weights"}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if profile.BitrateWeights.Over != 1 || profile.BitrateWeights.Under != 1 {
		t.Errorf("BitrateWeights = %+v, want {1 1}", profile.BitrateWeights)
	}
}

func TestParse_KeepsExplicitBitrateWeights(t *testing.T) {
	profile, err := Parse([]byte(`{"bitrate_weights": {"over": 2.5, "under": 0.5}}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if profile.BitrateWeights.Over != 2.5 || profile.BitrateWeights.Under != 0.5 {
		t.Errorf("BitrateWeights = %+v, want {2.5 0.5}", profile.BitrateWeights)
	}
}

func TestParse_RejectsInvalidJSON(t *testing.T) {
	if _, err := Parse([]byte(`{not json`)); err == nil {
		t.Fatal("Parse: expected error for invalid JSON, got nil")
	}
}

func TestParseUpgrade(t *testing.T) {
	policy, err := ParseUpgrade([]byte(`{"min_delta": 15, "min_file_age": "24h", "max_per_window": 3, "window": "168h"}`))
	if err != nil {
		t.Fatalf("ParseUpgrade: %v", err)
	}
	want := UpgradePolicy{MinDelta: 15, MinFileAge: Duration(24 * time.Hour), MaxPerWindow: 3, Window: Duration(168 * time.Hour)}
	if policy != want {
		t.Errorf("ParseUpgrade = %+v, want %+v", policy, want)
	}
}

func TestParseUpgrade_EmptyInput(t *testing.T) {
	policy, err := ParseUpgrade(nil)
	if err != nil {
		t.Fatalf("ParseUpgrade(nil): %v", err)
	}
	if policy != (UpgradePolicy{}) {
		t.Errorf("ParseUpgrade(nil) = %+v, want zero value", policy)
	}
}

func TestParseUpgrade_InvalidDuration(t *testing.T) {
	if _, err := ParseUpgrade([]byte(`{"window": "not-a-duration"}`)); err == nil {
		t.Fatal("ParseUpgrade: expected error for invalid duration, got nil")
	}
}

func TestInClause_PlaceholderForm(t *testing.T) {
	var clause InClause
	if err := json.Unmarshal([]byte(`"$preferred"`), &clause); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !clause.Placeholder || len(clause.Values) != 0 {
		t.Errorf("clause = %+v, want Placeholder=true, no Values", clause)
	}
}

func TestInClause_ListForm(t *testing.T) {
	var clause InClause
	if err := json.Unmarshal([]byte(`["en", "de"]`), &clause); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if clause.Placeholder {
		t.Error("clause.Placeholder = true, want false for a literal list")
	}
	if len(clause.Values) != 2 || clause.Values[0] != "en" || clause.Values[1] != "de" {
		t.Errorf("clause.Values = %v, want [en de]", clause.Values)
	}
}

func TestInClause_SingleLiteralString(t *testing.T) {
	var clause InClause
	if err := json.Unmarshal([]byte(`"en"`), &clause); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if clause.Placeholder {
		t.Error("clause.Placeholder = true, want false for a plain string that isn't $preferred")
	}
	if len(clause.Values) != 1 || clause.Values[0] != "en" {
		t.Errorf("clause.Values = %v, want [en]", clause.Values)
	}
}
