package adapters

import (
	"errors"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/jobs"
)

// The zero Cache would panic on any read, so a pass proves the guard sits before every lookup.
func TestArrSync_SafeModeSendsNothingToAnArr(t *testing.T) {
	safe := ArrSync{SafeMode: true}
	if err := safe.Run(t.Context(), jobs.Job{}); err != nil {
		t.Errorf("Run = %v, want nothing done", err)
	}
	if _, err := safe.PushMonitoring(t.Context(), 1); !errors.Is(err, ErrSafeMode) {
		t.Errorf("PushMonitoring = %v, want ErrSafeMode", err)
	}
	if err := safe.Added(t.Context(), apiserver.AddedWork{Kind: "movie", Provider: "tmdb", ExternalID: "603"}); err != nil {
		t.Errorf("Added = %v, want nothing done", err)
	}
}
