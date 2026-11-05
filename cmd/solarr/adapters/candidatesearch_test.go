package adapters

import (
	"context"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media/music"
)

// A torrent ranked best with no torrent client cannot be sent: it is marked and listed after
// every sendable release, each group keeping its best-first order (2026-09-30).
func TestSendableFirst_ListsWhatNoClientTakesLast(t *testing.T) {
	search := CandidateSearch{Dispatch: music.Dispatch{
		Downloaders: func(context.Context) ([]music.DownloaderConn, error) {
			return []music.DownloaderConn{{ID: 1, Protocol: "usenet"}}, nil
		},
	}}
	views := []apiserver.CandidateDecision{
		{ID: 1, Protocol: "torrent"}, {ID: 2, Protocol: "usenet"}, {ID: 3, Protocol: "torrent"}, {ID: 4, Protocol: "usenet"},
	}

	got := search.sendableFirst(t.Context(), views)
	want := []apiserver.CandidateDecision{
		{ID: 2, Protocol: "usenet"}, {ID: 4, Protocol: "usenet"},
		{ID: 1, Protocol: "torrent", NoClient: true}, {ID: 3, Protocol: "torrent", NoClient: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sendableFirst = %+v, want %+v", got, want)
	}
}

// A release both a season and an episode query return is listed once, the best-ranked decision kept (2026-11-04).
func TestDistinctReleases_KeepsTheFirstDecisionOfEachRelease(t *testing.T) {
	detail := func(decisionID, indexerID int64, guid string) library.DecisionDetail {
		return library.DecisionDetail{
			Decision:         library.Decision{ID: decisionID},
			ReleaseCandidate: library.ReleaseCandidate{IndexerID: indexerID, GUID: guid},
		}
	}
	got := distinctReleases([]library.DecisionDetail{detail(1, 1, "a"), detail(2, 1, "a"), detail(3, 2, "a"), detail(4, 1, "b")})
	var ids []int64
	for _, kept := range got {
		ids = append(ids, kept.Decision.ID)
	}
	if want := []int64{1, 3, 4}; !reflect.DeepEqual(ids, want) {
		t.Errorf("kept decisions = %v, want %v", ids, want)
	}
}
