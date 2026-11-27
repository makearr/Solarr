package comic

import "testing"

func TestSameTitleIgnoresAccentsAndCase(t *testing.T) {
	if !sameTitle("Étoile Nord", "ETOILE NORD") || sameTitle("Étoile Nord", "Étoile Sud") {
		t.Error("accents and case must not tell titles apart, and different titles must stay apart")
	}
}
