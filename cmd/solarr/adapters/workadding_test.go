package adapters

import (
	"errors"
	"fmt"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/media"
)

func TestNotWired_AnIDNoSourceKnowsIsNotFoundNotAnInternalError(t *testing.T) {
	err := notWired(fmt.Errorf("music: %w", media.ErrUnknownCandidate))
	if !errors.Is(err, apiserver.ErrCandidateNotFound) {
		t.Errorf("err = %v, want ErrCandidateNotFound", err)
	}
}
