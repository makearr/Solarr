package settings

import "testing"

func TestVideoPerceptualHashIsOnUnlessTurnedOff(t *testing.T) {
	for stored, want := range map[string]bool{"": true, "true": true, "false": false} {
		policy := map[string]string{}
		if stored != "" {
			policy[KeyVideoPerceptualHash] = stored
		}
		if got := (&Snapshot{policy: policy}).VideoPerceptualHash(); got != want {
			t.Errorf("stored %q: %v, want %v", stored, got, want)
		}
	}
}
