package music_test

import (
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/media/music"
)

type heldEnv struct {
	*searchTestEnv
	grab      music.Grab
	sender    *fakeSabnzbdSender
	indexers  []music.IndexerConn
	blocklist library.BlocklistStore
	first     librarydb.DesiredItem
	second    librarydb.DesiredItem
}

// Two albums, one client that takes one download at a time.
func newHeldEnv(t *testing.T) heldEnv {
	t.Helper()
	env := newSearchTestEnv(t)
	first := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, losslessFirstRules)
	second := env.createSearchableWork(t, "Nosi Zada", "Pasulo", 2003, losslessFirstRules)
	searcher := &fakeIndexerSearcher{responses: func(request indexer.SearchRequest) ([]indexer.RawItem, error) {
		if strings.Contains(request.Query, "Pasulo") {
			return []indexer.RawItem{rawItem("guid-pasulo", "Nosi Zada-Pasulo-2003-FLAC-GROUP")}, nil
		}
		return []indexer.RawItem{rawItem("guid-discovery", "Nosi Zada-Discovery-2001-FLAC-GROUP")}, nil
	}}
	sender := &fakeSabnzbdSender{clientRef: "nzo-1"}
	dispatch := env.dispatch([]music.DownloaderConn{{ID: 5, Protocol: "usenet", Client: sender, MaxQueued: 1}})
	ready := library.NewReadyQueue(env.library)
	blocklist := library.NewBlocklistStore(env.library)
	dispatch.Ready, dispatch.Blocklist = &ready, &blocklist
	return heldEnv{
		searchTestEnv: env, grab: music.Grab{Search: env.search(), Dispatch: dispatch}, sender: sender,
		indexers: []music.IndexerConn{{ID: 1, Impl: "newznab", Client: searcher}}, blocklist: blocklist,
		first: first, second: second,
	}
}

func (e heldEnv) grabItem(t *testing.T, itemID int64) music.GrabResult {
	t.Helper()
	result, err := e.grab.SearchAndSend(t.Context(), itemID, e.indexers, e.clock.Now())
	if err != nil {
		t.Fatalf("SearchAndSend: %v", err)
	}
	return result
}

func (e heldEnv) sendHeld(t *testing.T) int {
	t.Helper()
	sent, err := e.grab.Dispatch.SendHeld(t.Context(), e.clock.Now())
	if err != nil {
		t.Fatalf("SendHeld: %v", err)
	}
	return sent
}

// ADR-57: beyond the client's queue depth an automatic send is held, and goes once there is room.
func TestHeld_AFullClientHoldsTheReleaseUntilThereIsRoom(t *testing.T) {
	e := newHeldEnv(t)
	sent := e.grabItem(t, e.first.ID).Sent
	if sent == nil {
		t.Fatal("the first album was not sent")
	}

	if result := e.grabItem(t, e.second.ID); !result.Held || result.Sent != nil {
		t.Fatalf("second grab = held %v, sent %v; want held", result.Held, result.Sent)
	}
	if len(e.sender.requests) != 1 {
		t.Fatalf("the client got %d releases, want 1 while its queue is full", len(e.sender.requests))
	}
	if got := e.sendHeld(t); got != 0 {
		t.Errorf("SendHeld sent %d with the queue still full", got)
	}

	if err := e.acquisitions.SetState(t.Context(), sent.ID, "COMPLETED", "", 2); err != nil {
		t.Fatal(err)
	}
	e.sender.clientRef = "nzo-2"
	if got := e.sendHeld(t); got != 1 {
		t.Fatalf("SendHeld sent %d once the first finished, want 1", got)
	}
	if last := e.sender.requests[len(e.sender.requests)-1]; last.Name != "Nosi Zada-Pasulo-2003-FLAC-GROUP" {
		t.Errorf("sent %q, want the held release", last.Name)
	}
	if got := e.sendHeld(t); got != 0 {
		t.Errorf("SendHeld sent %d again; a held release goes once", got)
	}
}

// Checked again before it goes (section 10.5): blocklisted or unmonitored meanwhile, it is dropped.
func TestHeld_AReleaseBlocklistedOrUnmonitoredMeanwhileIsDropped(t *testing.T) {
	for _, change := range []string{"blocklisted", "unmonitored"} {
		t.Run(change, func(t *testing.T) {
			e := newHeldEnv(t)
			sent := e.grabItem(t, e.first.ID).Sent
			if !e.grabItem(t, e.second.ID).Held {
				t.Fatal("not held")
			}
			switch change {
			case "blocklisted":
				indexerID := int64(1)
				if _, err := e.blocklist.Create(t.Context(), library.NewBlocklistEntry{
					IndexerID: &indexerID, GUID: "guid-pasulo", Title: "Nosi Zada-Pasulo-2003-FLAC-GROUP", Reason: library.BlocklistManual,
					Source: library.BlocklistSourceUser, CreatedAt: 1,
				}); err != nil {
					t.Fatal(err)
				}
			case "unmonitored":
				if err := e.desired.SetItemMonitored(t.Context(), e.second.ID, false); err != nil {
					t.Fatal(err)
				}
			}
			if err := e.acquisitions.SetState(t.Context(), sent.ID, "COMPLETED", "", 2); err != nil {
				t.Fatal(err)
			}

			if got := e.sendHeld(t); got != 0 || len(e.sender.requests) != 1 {
				t.Errorf("sent %d (%d requests), want nothing sent", got, len(e.sender.requests))
			}
		})
	}
}
