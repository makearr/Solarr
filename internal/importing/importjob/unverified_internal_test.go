package importjob

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

// D6: only a run started from the Import screen adds what nothing identified; a finished download
// was grabbed for a known album, and a preview places nothing at all.
func TestUnverifiedWanted_OnlyForAManualNonPreviewRun(t *testing.T) {
	for name, test := range map[string]struct {
		payload Payload
		want    bool
	}{
		"manual":            {Payload{Origin: library.ImportOriginManual}, true},
		"manual, opted out": {Payload{Origin: library.ImportOriginManual, NoUnverified: true}, false},
		"preview":           {Payload{Origin: library.ImportOriginManual, Preview: true}, false},
		"download":          {Payload{Origin: library.ImportOriginAcquisition}, false},
	} {
		if got := unverifiedWanted(test.payload); got != test.want {
			t.Errorf("%s: unverifiedWanted = %v, want %v", name, got, test.want)
		}
	}
}
