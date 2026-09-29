package poll_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/acquiring/poll"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/migrations"
)

// fakeClient answers a canned list of downloads and records removals.
type fakeClient struct {
	download.Client
	downloads []download.Download
	status    download.QueueStatus
	removed   []string
}

func (f *fakeClient) Downloads(context.Context, string) ([]download.Download, error) {
	return f.downloads, nil
}

func (f *fakeClient) QueueStatus(context.Context) (download.QueueStatus, error) {
	return f.status, nil
}

func (f *fakeClient) Remove(_ context.Context, clientRef string, deleteFiles bool) error {
	if deleteFiles {
		clientRef += "+files"
	}
	f.removed = append(f.removed, clientRef)
	return nil
}

type env struct {
	store        *store.Store
	acquisitions library.Acquisitions
	sessions     library.ImportSessions
	blocklist    library.BlocklistStore
	fsys         *fsx.Mem
	clock        *clock.Fake
	client       *fakeClient
	removeDone   bool
	imported     []string
	searched     []int64
	history      []string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := clock.NewFakeAtEpoch()
	opened, err := store.Open(t.Context(), fake, store.Config{
		Name: "library", Path: filepath.Join(directory, "library.db"), Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	owner, err := migrations.Library()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(t.Context(), opened, backups, owner); err != nil {
		t.Fatal(err)
	}
	fsys := fsx.NewMem(fake)
	if err := fsys.MkdirAll("/downloads/complete/Heka-Watermark", 0o755); err != nil {
		t.Fatal(err)
	}
	return &env{
		store: opened, acquisitions: library.NewAcquisitions(opened), sessions: library.NewImportSessions(opened),
		blocklist: library.NewBlocklistStore(opened), fsys: fsys, clock: fake, client: &fakeClient{}, removeDone: true,
	}
}

func (e *env) job() *poll.Job {
	return poll.NewJob(poll.Config{
		Acquisitions: e.acquisitions, Sessions: e.sessions, Blocklist: e.blocklist, FS: e.fsys,
		Clients: func(context.Context) (map[int64]poll.Client, error) {
			return map[int64]poll.Client{1: {Name: "Home SABnzbd", Client: e.client, RemoveCompleted: e.removeDone,
				LocalPath: func(reported string) string { return reported }}}, nil
		},
		DownloadFolders: func(context.Context) ([]string, error) { return []string{"/downloads"}, nil },
		Import: func(_ context.Context, _ library.Acquisition, localPath string) error {
			e.imported = append(e.imported, localPath)
			return nil
		},
		Search: func(_ context.Context, desiredItemID int64) error {
			e.searched = append(e.searched, desiredItemID)
			return nil
		},
		History: func(_ context.Context, kind download.HistoryKind, acquisition library.Acquisition, detail string) {
			e.history = append(e.history, fmt.Sprintf("%s %s %s", kind, acquisition.DownloadClientRef, detail))
		},
		Clock: e.clock, Logger: slog.New(slog.DiscardHandler),
	})
}

func (e *env) run(t *testing.T) {
	t.Helper()
	if err := e.job().Run(t.Context(), jobs.Job{Type: poll.JobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// send records one acquisition as dispatch does, for a wanted album.
func (e *env) send(t *testing.T, ref string) library.Acquisition {
	t.Helper()
	identity := library.NewIdentity(e.store)
	workIDs, err := identity.ResolveWorks(t.Context(), e.clock.Now(), []library.WorkLink{{
		Provider: "musicbrainz:release-group", ExternalID: "rg-" + ref, Confidence: 1,
		Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: "Watermark " + ref},
	}})
	if err != nil {
		t.Fatal(err)
	}
	item, err := library.NewDesired(e.store).EnsureAlbumItem(t.Context(), library.AlbumRequest{
		WorkID: workIDs[0], CategoryID: 1, ProfileID: 1, Monitored: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	indexerID := int64(3)
	acquisition, err := e.acquisitions.Create(t.Context(), library.NewAcquisition{
		DesiredItemID: &item.ID, IndexerID: &indexerID, CandidateGUID: "guid-" + ref, CandidateTitle: "Heka - Watermark",
		DownloaderID: 1, DownloadClientRef: ref, Protocol: "usenet", State: poll.StateQueued,
		CreatedAt: 1, UpdatedAt: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return acquisition
}

func (e *env) state(t *testing.T, id int64) library.Acquisition {
	t.Helper()
	acquisition, err := e.acquisitions.Get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return acquisition
}

func TestPoll_FollowsADownloadToImporting(t *testing.T) {
	e := newEnv(t)
	sent := e.send(t, "nzo-1")

	e.client.downloads = []download.Download{{ClientRef: "nzo-1", Name: "Heka-Watermark", State: download.StateDownloading}}
	e.run(t)
	if got := e.state(t, sent.ID); got.State != poll.StateDownloading || got.ClientFingerprint != "client:Heka-Watermark" {
		t.Fatalf("after downloading: %s %q, want DOWNLOADING with the client's name recorded", got.State, got.ClientFingerprint)
	}

	e.client.downloads = []download.Download{{ClientRef: "nzo-1", Name: "Heka-Watermark", State: download.StateCompleted,
		Path: "/downloads/complete/Heka-Watermark"}}
	e.run(t)
	if got := e.state(t, sent.ID); got.State != poll.StateImporting || got.DownloadDir != "/downloads/complete/Heka-Watermark" {
		t.Errorf("after completion: %s %q, want IMPORTING from its folder", got.State, got.DownloadDir)
	}
	if !slices.Equal(e.imported, []string{"/downloads/complete/Heka-Watermark"}) {
		t.Errorf("imported %v, want exactly the reported folder", e.imported)
	}
}

// A path outside every download folder is reported and never imported (ADR-14).
func TestPoll_RefusesAPathOutsideTheDownloadFolders(t *testing.T) {
	e := newEnv(t)
	sent := e.send(t, "nzo-1")
	if err := e.fsys.MkdirAll("/elsewhere/Heka-Watermark", 0o755); err != nil {
		t.Fatal(err)
	}
	e.client.downloads = []download.Download{{ClientRef: "nzo-1", Name: "x", State: download.StateCompleted,
		Path: "/elsewhere/Heka-Watermark"}}
	e.run(t)
	if got := e.state(t, sent.ID); got.State != poll.StateCompleted || got.LastError == "" || len(e.imported) != 0 {
		t.Errorf("= %s %q, imported %v; want COMPLETED with the reason and nothing imported", got.State, got.LastError, e.imported)
	}
}

// A failed download is blocklisted for 30 days, removed with its files, and the album searched again.
func TestPoll_AFailedDownloadIsBlocklistedAndSearchedAgain(t *testing.T) {
	e := newEnv(t)
	sent := e.send(t, "nzo-1")
	e.client.downloads = []download.Download{{ClientRef: "nzo-1", Name: "x", State: download.StateFailed, Error: "Repair failed"}}
	e.run(t)

	if got := e.state(t, sent.ID); got.State != poll.StateFailed || got.LastError != "Repair failed" {
		t.Errorf("= %s %q, want FAILED with the client's reason", got.State, got.LastError)
	}
	indexerID := int64(3)
	blocked, err := e.blocklist.IsBlocked(t.Context(), &indexerID, "guid-nzo-1", "", e.clock.Now().Unix())
	if err != nil || !blocked {
		t.Errorf("blocklisted = %v, %v; want the release blocked", blocked, err)
	}
	later := e.clock.Now().Add(31 * 24 * time.Hour).Unix()
	if still, _ := e.blocklist.IsBlocked(t.Context(), &indexerID, "guid-nzo-1", "", later); still {
		t.Error("still blocked after 31 days, want the automatic entry to expire")
	}
	if !slices.Equal(e.client.removed, []string{"nzo-1+files"}) || !slices.Equal(e.searched, []int64{*sent.DesiredItemID}) {
		t.Errorf("removed %v, searched %v; want removed with files and the album searched", e.client.removed, e.searched)
	}
	if want := []string{"download.failed nzo-1 Repair failed"}; !slices.Equal(e.history, want) {
		t.Errorf("history = %q, want %q", e.history, want)
	}
}

// A person removing a download is respected: failed, never sent again. Losing most at once is a
// client that lost its queue: nothing is concluded.
func TestPoll_VanishedAndMassVanished(t *testing.T) {
	e := newEnv(t)
	one := e.send(t, "nzo-1")
	e.run(t)
	if got := e.state(t, one.ID); got.State != poll.StateFailed || len(e.searched) != 0 {
		t.Errorf("one vanished: %s, searched %v; want FAILED and no new search", got.State, e.searched)
	}
	if want := []string{"download.failed nzo-1 removed from the download client"}; !slices.Equal(e.history, want) {
		t.Errorf("history = %q, want %q", e.history, want)
	}

	f := newEnv(t)
	var sent []library.Acquisition
	for _, ref := range []string{"a", "b", "c", "d"} {
		sent = append(sent, f.send(t, ref))
	}
	f.client.downloads = []download.Download{{ClientRef: "a", Name: "a", State: download.StateDownloading}}
	f.run(t)
	for _, acquisition := range sent[1:] {
		if got := f.state(t, acquisition.ID); got.State != poll.StateQueued {
			t.Errorf("mass vanish: %s is %s, want untouched", acquisition.DownloadClientRef, got.State)
		}
	}
}

// A reference that later names another download is a different download.
func TestPoll_AReusedReferenceIsNotAdopted(t *testing.T) {
	e := newEnv(t)
	sent := e.send(t, "nzo-1")
	e.client.downloads = []download.Download{{ClientRef: "nzo-1", Name: "Heka-Watermark", State: download.StateDownloading}}
	e.run(t)
	e.client.downloads = []download.Download{{ClientRef: "nzo-1", Name: "Something Else", State: download.StateCompleted,
		Path: "/downloads/complete/Heka-Watermark"}}
	e.run(t)
	if got := e.state(t, sent.ID); got.State != poll.StateFailed || len(e.imported) != 0 {
		t.Errorf("= %s, imported %v; want FAILED and nothing imported", got.State, e.imported)
	}
}

// SABnzbd names a job only once its NZB is fetched; the fingerprint waits for that name.
func TestPoll_TheFingerprintWaitsForTheClientsName(t *testing.T) {
	e := newEnv(t)
	sent := e.send(t, "nzo-1")
	e.client.downloads = []download.Download{{ClientRef: "nzo-1", State: download.StateQueued}}
	e.run(t)
	e.client.downloads = []download.Download{{ClientRef: "nzo-1", Name: "Heka-Watermark", State: download.StateDownloading}}
	e.run(t)
	if got := e.state(t, sent.ID); got.State != poll.StateDownloading || got.ClientFingerprint != "client:Heka-Watermark" {
		t.Errorf("= %s %q, want DOWNLOADING fingerprinted by the real name", got.State, got.ClientFingerprint)
	}
}

// importing starts an import session for the acquisition and finishes it with units decided as given.
func (e *env) finishImport(t *testing.T, acquisition library.Acquisition, placed bool, decision string) {
	t.Helper()
	sessionID, err := e.sessions.Create(t.Context(), library.NewSession{
		SourcePath: "/downloads/complete/Heka-Watermark", Origin: library.ImportOriginAcquisition,
		AcquisitionID: &acquisition.ID, Threshold: 0.9, StartedAt: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	units, err := e.sessions.AddUnits(t.Context(), sessionID, []library.NewUnit{{RelPath: ".", Shape: library.UnitSet, FileCount: 1, EvidenceJSON: "{}"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.sessions.SetUnitDecisions(t.Context(), sessionID, []library.UnitDecision{{UnitID: units[0], Decision: decision}}); err != nil {
		t.Fatal(err)
	}
	if placed {
		work, err := library.NewDesired(e.store).Get(t.Context(), *acquisition.DesiredItemID)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.sessions.RecordPlacement(t.Context(), units[0], library.Placement{WorkID: work.WorkID}); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.sessions.Finish(t.Context(), sessionID, library.ImportDone, 3, ""); err != nil {
		t.Fatal(err)
	}
	if err := e.acquisitions.SetState(t.Context(), acquisition.ID, poll.StateImporting, "", 3); err != nil {
		t.Fatal(err)
	}
}

func TestPoll_AFinishedImportIsCleanedUp(t *testing.T) {
	for _, removeDone := range []bool{true, false} {
		e := newEnv(t)
		e.removeDone = removeDone
		sent := e.send(t, "nzo-1")
		e.finishImport(t, sent, true, library.UnitCommit)
		e.run(t)
		want, wantRemoved := poll.StateCleaned, []string{"nzo-1+files"}
		if !removeDone {
			want, wantRemoved = poll.StateImported, nil
		}
		if got := e.state(t, sent.ID); got.State != want || !slices.Equal(e.client.removed, wantRemoved) {
			t.Errorf("remove completed %v: %s, removed %v; want %s, %v", removeDone, got.State, e.client.removed, want, wantRemoved)
		}
		if wantHistory := []string{"download.imported nzo-1 "}; !slices.Equal(e.history, wantHistory) {
			t.Errorf("history = %q, want %q", e.history, wantHistory)
		}
	}
}

func TestPoll_AnImportWaitingForAPersonWaits(t *testing.T) {
	e := newEnv(t)
	sent := e.send(t, "nzo-1")
	e.finishImport(t, sent, false, library.UnitQueue)
	e.run(t)
	if got := e.state(t, sent.ID); got.State != poll.StateImporting || got.LastError == "" || len(e.client.removed) != 0 {
		t.Errorf("= %s %q, removed %v; want IMPORTING, waiting, nothing removed", got.State, got.LastError, e.client.removed)
	}
	if len(e.history) != 0 {
		t.Errorf("history = %q, want nothing until the import finishes", e.history)
	}
}

// Committed but never placed (no root had room) is not imported: it fails and is searched again.
func TestPoll_AnImportThatPlacedNothingFails(t *testing.T) {
	e := newEnv(t)
	sent := e.send(t, "nzo-1")
	e.finishImport(t, sent, false, library.UnitCommit)
	e.run(t)
	if got := e.state(t, sent.ID); got.State != poll.StateFailed || len(e.searched) != 1 {
		t.Errorf("= %s, searched %v; want FAILED and searched again", got.State, e.searched)
	}
}
