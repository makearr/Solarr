package dispatch

import (
	"context"
	"testing"
)

func TestChooseTakesTheBoundClientThenTheLowestPriority(t *testing.T) {
	clients := []DownloaderConn{
		{ID: 1, Protocol: "usenet", Priority: 30},
		{ID: 2, Protocol: "usenet", Priority: 10},
		{ID: 3, Protocol: "torrent", Priority: 1},
	}
	bound := int64(0)
	d := Dispatch{BoundClient: func(_ context.Context, indexerID int64) int64 {
		if indexerID == 7 {
			return bound
		}
		return 0
	}}
	for name, test := range map[string]struct {
		indexer, bound int64
		protocol       string
		want           int64
	}{
		"lowest number first":        {1, 0, "usenet", 2},
		"bound client wins":          {7, 1, "usenet", 1},
		"bound to another protocol":  {7, 3, "usenet", 2},
		"only the protocol's client": {1, 0, "torrent", 3},
	} {
		bound = test.bound
		got, found, err := d.choose(t.Context(), clients, test.indexer, test.protocol)
		if err != nil || !found || got.ID != test.want {
			t.Errorf("%s: got %d found=%v err=%v, want %d", name, got.ID, found, err, test.want)
		}
	}
	if _, found, _ := d.choose(t.Context(), clients, 1, "ddl"); found {
		t.Error("a protocol no client takes found one")
	}
}
