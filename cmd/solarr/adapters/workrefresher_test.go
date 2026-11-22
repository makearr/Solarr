package adapters_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media"
)

type refreshingDown struct{}

func (refreshingDown) RefreshWork(context.Context, int64) (bool, error) {
	return false, media.ErrSourceUnavailable
}

// A refresh whose metadata sources do not answer is the API's provider outage, not an internal error.
func TestWorkRefresher_ASourceOutageIsTheAPIsOutageAnswer(t *testing.T) {
	opened := newSuggestLibrary(t)
	ids, err := library.NewIdentity(opened).ResolveWorks(t.Context(), time.Unix(1_700_000_000, 0), []library.WorkLink{{
		Provider: "openlibrary:work", ExternalID: "w-1", Confidence: 1,
		Work: library.WorkIdentity{MediaType: "book", CanonicalTitle: "Kitu Pomaka", SortTitle: "Kitu Pomaka"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	refresher := adapters.WorkRefresher{
		Works:      library.NewWorks(opened),
		Refreshing: func(string) (media.Refreshing, bool) { return refreshingDown{}, true },
	}
	_, err = refresher.RefreshWork(t.Context(), ids[0])
	if !errors.Is(err, apiserver.ErrSeriesSourceUnavailable) {
		t.Errorf("err = %v, want apiserver.ErrSeriesSourceUnavailable", err)
	}
}
