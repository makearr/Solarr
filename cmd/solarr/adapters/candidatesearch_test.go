package adapters

import (
	"context"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
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
