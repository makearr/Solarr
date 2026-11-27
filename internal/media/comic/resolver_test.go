package comic

import "testing"

func TestSameTitleIgnoresAccentsAndCase(t *testing.T) {
	if !sameTitle("Étoile Nord", "ETOILE NORD") || sameTitle("Étoile Nord", "Étoile Sud") {
		t.Error("accents and case must not tell titles apart, and different titles must stay apart")
	}
}

func TestPick_ChoosesTheEditionTheFileSaysThenTheLibrarysPreferredLanguage(t *testing.T) {
	found := []Candidate{
		{ID: "fr", Title: "Étoile Nord", Year: 1961, Language: "fr"},
		{ID: "en", Title: "Etoile Nord", Year: 1961, Language: "en"},
	}
	for name, test := range map[string]struct {
		file      string
		preferred []string
		want      string
		sure      bool
	}{
		"the file says french":    {"fr", []string{"en"}, "fr", true},
		"the file says nothing":   {"", []string{"en"}, "en", true},
		"neither is held":         {"", []string{"de"}, "fr", false},
		"the file outranks taste": {"en", []string{"fr"}, "en", true},
	} {
		best, confidence, ok := pick(found, Evidence{Series: "Etoile Nord", Year: 1961, Language: test.file}, test.preferred)
		if !ok || best.ID != test.want || (confidence >= 0.9) != test.sure {
			t.Errorf("%s: picked %q at %.2f, want %q (sure %v)", name, best.ID, confidence, test.want, test.sure)
		}
	}
}
