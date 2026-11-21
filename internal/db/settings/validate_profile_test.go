package settings

import "testing"

func TestValidateQualityProfile_RefusesAnUnknownUpgradeKey(t *testing.T) {
	rules := `{"quality_profile":"p","require":{},"compare":["score"]}`
	if problems := validateQualityProfile("p", rules, `{"min_delta":2}`); len(problems) != 0 {
		t.Fatalf("a known upgrade key: %+v, want none", problems)
	}
	problems := validateQualityProfile("p", rules, `{"min_delt":2}`)
	if len(problems) != 1 || problems[0].Path != "upgrade" || problems[0].Value != "min_delt" {
		t.Errorf("a mistyped upgrade key: %+v, want one problem on upgrade naming min_delt", problems)
	}
}
