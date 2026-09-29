package music_test

import (
	"strings"
	"testing"
	"time"

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
	third     librarydb.DesiredItem
}

// Three albums, one client that takes one download at a time and holds a user's grabs unless
// alwaysSendUser.
func newHeldEnv(t *testing.T, alwaysSendUser bool) heldEnv {
	t.Helper()
	env := newSearchTestEnv(t)
	first := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, losslessFirstRules)
	second := env.createSearchableWork(t, "Nosi Zada", "Pasulo", 2003, losslessFirstRules)
	third := env.createSearchableWork(t, "Nosi Zada", "Kemaro", 2005, losslessFirstRules)
	searcher := &fakeIndexerSearcher{responses: func(request indexer.SearchRequest) ([]indexer.RawItem, error) {
		for title, release := range map[string]string{
			"Pasulo": "Nosi Zada-Pasulo-2003-FLAC-GROUP", "Kemaro": "Nosi Zada-Kemaro-2005-FLAC-GROUP",
		} {
			if strings.Contains(request.Query, title) {
				return []indexer.RawItem{rawItem("guid-"+strings.ToLower(title), release)}, nil
			}
		}
		return []indexer.RawItem{rawItem("guid-discovery", "Nosi Zada-Discovery-2001-FLAC-GROUP")}, nil
	}}
	sender := &fakeSabnzbdSender{clientRef: "nzo-1"}
	dispatch := env.dispatch([]music.DownloaderConn{{
		ID: 5, Protocol: "usenet", Client: sender, MaxQueued: 1, AlwaysSendUser: alwaysSendUser,
	}})
	ready := library.NewReadyQueue(env.library)
	blocklist := library.NewBlocklistStore(env.library)
	dispatch.Ready, dispatch.Blocklist = &ready, &blocklist
	return heldEnv{
		searchTestEnv: env, grab: music.Grab{Search: env.search(), Dispatch: dispatch}, sender: sender,
		indexers: []music.IndexerConn{{ID: 1, Impl: "newznab", Client: searcher}}, blocklist: blocklist,
		first: first, second: second, third: third,
	}
}

func (e heldEnv) grabItem(t *testing.T, itemID int64) music.GrabResult {
	t.Helper()
	return e.grabAs(t, itemID, music.GrabAutomatic)
}

func (e heldEnv) grabAs(t *testing.T, itemID int64, origin music.GrabOrigin) music.GrabResult {
	t.Helper()
	result, err := e.grab.SearchAndSend(t.Context(), itemID, e.indexers, origin, e.clock.Now())
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
	e := newHeldEnv(t, true)
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
			e := newHeldEnv(t, true)
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

// 2026-09-29: HELD is for automatic searches; a user's own grab goes to a full client when the
// client says "always send my grabs".
func TestHeld_AUsersGrabGoesToAFullClient(t *testing.T) {
	e := newHeldEnv(t, true)
	if e.grabItem(t, e.first.ID).Sent == nil {
		t.Fatal("the first album was not sent")
	}
	e.sender.clientRef = "nzo-2"
	if result := e.grabAs(t, e.second.ID, music.GrabUser); result.Held || result.Sent == nil {
		t.Fatalf("user grab = held %v, sent %v; want sent", result.Held, result.Sent)
	}
	if len(e.sender.requests) != 2 {
		t.Errorf("the client got %d releases, want 2", len(e.sender.requests))
	}
}

// With the box off a user's grab is held too, and goes before an automatic one held earlier.
func TestHeld_AHeldUserGrabGoesBeforeAnAutomaticOne(t *testing.T) {
	e := newHeldEnv(t, false)
	sent := e.grabItem(t, e.first.ID).Sent
	if sent == nil {
		t.Fatal("the first album was not sent")
	}
	if !e.grabItem(t, e.second.ID).Held {
		t.Fatal("the automatic grab was not held")
	}
	e.clock.Advance(time.Minute)
	if !e.grabAs(t, e.third.ID, music.GrabUser).Held {
		t.Fatal("the user's grab was not held with the box off")
	}

	held, err := library.NewReadyQueue(e.library).Next(t.Context(), e.clock.Now().Unix(), 10)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, entry := range held {
		order = append(order, entry.Origin)
	}
	if strings.Join(order, ",") != "user,automatic" {
		t.Errorf("held order = %v, want the user's first", order)
	}

	if err := e.acquisitions.SetState(t.Context(), sent.ID, "COMPLETED", "", 2); err != nil {
		t.Fatal(err)
	}
	e.sender.clientRef = "nzo-2"
	if got := e.sendHeld(t); got != 1 {
		t.Fatalf("SendHeld sent %d, want 1", got)
	}
	if last := e.sender.requests[len(e.sender.requests)-1]; last.Name != "Nosi Zada-Kemaro-2005-FLAC-GROUP" {
		t.Errorf("sent %q first, want the user's", last.Name)
	}
}
