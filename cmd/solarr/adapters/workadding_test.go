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

func TestLookupFailure_AnUnclassifiedErrorIsTheProviderNotAnswering(t *testing.T) {
	err := lookupFailure(errors.New("musicbrainz: dial tcp: connection refused"))
	if !errors.Is(err, apiserver.ErrSeriesSourceUnavailable) {
		t.Errorf("err = %v, want ErrSeriesSourceUnavailable", err)
	}
	if err := lookupFailure(media.ErrQueryTooShort); !errors.Is(err, apiserver.ErrQueryTooShort) {
		t.Errorf("a classified error: err = %v, want ErrQueryTooShort", err)
	}
}
