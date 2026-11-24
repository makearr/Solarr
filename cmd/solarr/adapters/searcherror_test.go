package adapters

import (
	"errors"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/media"
)

func TestSearchError_NoIndexersIsTheModesFaultWhenTheModeStoppedSearches(t *testing.T) {
	stopped := CandidateSearch{SearchesStopped: func() bool { return true }}
	if err := stopped.searchError(media.ErrNoIndexers); !errors.Is(err, apiserver.ErrSearchesStopped) {
		t.Errorf("stopped: %v, want ErrSearchesStopped", err)
	}
	if err := (CandidateSearch{}).searchError(media.ErrNoIndexers); !errors.Is(err, apiserver.ErrNoIndexersConfigured) {
		t.Errorf("running: %v, want ErrNoIndexersConfigured", err)
	}
}
