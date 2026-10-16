package adapters

import (
	"errors"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
)

func TestCheckMusicBrainzID_AMistypedIDIsNotFoundNotAnInternalError(t *testing.T) {
	if err := checkMusicBrainzID("not-an-mbid"); !errors.Is(err, apiserver.ErrCandidateNotFound) {
		t.Errorf("mistyped id: err = %v, want ErrCandidateNotFound", err)
	}
	if err := checkMusicBrainzID("5b11f4ce-a62d-471e-81fc-a69a8278c7da"); err != nil {
		t.Errorf("well-formed id: err = %v, want nil", err)
	}
}
