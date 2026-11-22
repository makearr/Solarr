package recovery_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/recovery"
	"github.com/makearr/Solarr/internal/db/store"
)

func (f fixture) acquisition(t *testing.T, ref string) library.Acquisition {
	t.Helper()
	created, err := library.NewAcquisitions(f.library).Create(t.Context(), library.NewAcquisition{
		DownloaderID: 4, DownloadClientRef: ref, ClientCategory: "solarr-tv", Protocol: "usenet", State: "DOWNLOADING",
		CreatedAt: 10, UpdatedAt: 10, CandidateTitle: "Show.S01E01.1080p-GRP",
	})
	if err != nil {
		t.Fatalf("create acquisition: %v", err)
	}
	return created
}

func (f fixture) restore(t *testing.T, clients ...recovery.DownloadClient) recovery.Report {
	t.Helper()
	report, err := recovery.Reconcile(t.Context(), f.clock, f.library, f.jobs, recovery.Known{},
		[]store.RecoveryReport{{Database: "library", Recovered: true}}, clients)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	return report
}

func holding(held ...recovery.HeldDownload) recovery.DownloadClient {
	return recovery.DownloadClient{
		ID: 4, Name: "Usenet", Categories: []string{"solarr-tv"},
		Held: func(context.Context, []string) ([]recovery.HeldDownload, error) { return held, nil },
	}
}

// A download its client still holds is left to the poll; one the client lost is closed without a failure or a blocklist row (TRACK-18).
func TestReconcile_ClosesWhatTheClientLostAndLeavesWhatItHolds(t *testing.T) {
	f := newFixture(t)
	kept, lost := f.acquisition(t, "nzo-kept"), f.acquisition(t, "nzo-lost")
	report := f.restore(t, holding(recovery.HeldDownload{Ref: "nzo-kept", Category: "solarr-tv"}))
	acquisitions := library.NewAcquisitions(f.library)
	gotKept, _ := acquisitions.Get(t.Context(), kept.ID)
	gotLost, _ := acquisitions.Get(t.Context(), lost.ID)
	if gotKept.State != "DOWNLOADING" || gotLost.State != "CANCELLED" || gotLost.LastError != recovery.LostInRestore || report.DownloadsClosed != 1 {
		t.Errorf("kept %s, lost %s (%q), closed %d; want DOWNLOADING, CANCELLED with the restore reason, 1", gotKept.State, gotLost.State, gotLost.LastError, report.DownloadsClosed)
	}
}

// What a client holds that no acquisition references is reported with its path, never guessed at (TRACK-18).
func TestReconcile_ReportsWhatNothingReferences(t *testing.T) {
	f := newFixture(t)
	f.acquisition(t, "nzo-known")
	report := f.restore(t, holding(
		recovery.HeldDownload{Ref: "nzo-known", Name: "Known", Category: "solarr-tv"},
		recovery.HeldDownload{Ref: "nzo-new", Name: "Show.S01E02.1080p-GRP", Category: "solarr-tv", Path: "/dl/Show.S01E02", Finished: true},
		recovery.HeldDownload{Ref: "nzo-running", Name: "Show.S01E03.1080p-GRP", Category: "solarr-tv"},
	))
	want := []recovery.UntrackedDownload{
		{ClientID: 4, Client: "Usenet", Name: "Show.S01E02.1080p-GRP", Path: "/dl/Show.S01E02", Finished: true},
		{ClientID: 4, Client: "Usenet", Name: "Show.S01E03.1080p-GRP"},
	}
	if !reflect.DeepEqual(report.Untracked, want) {
		t.Errorf("untracked = %+v, want %+v", report.Untracked, want)
	}
}

// A client that cannot be asked leaves its downloads alone and is named as a gap (TRACK-18).
func TestReconcile_ANamedGapForAClientThatCannotBeAsked(t *testing.T) {
	f := newFixture(t)
	acquisition := f.acquisition(t, "nzo-1")
	down := recovery.DownloadClient{ID: 4, Name: "Usenet", Held: func(context.Context, []string) ([]recovery.HeldDownload, error) {
		return nil, errors.New("connection refused")
	}}
	report := f.restore(t, down)
	got, _ := library.NewAcquisitions(f.library).Get(t.Context(), acquisition.ID)
	named := false
	for _, gap := range report.Gaps {
		named = named || gap.What == "re-derive in-flight downloads from the Usenet download client"
	}
	if got.State != "DOWNLOADING" || !named {
		t.Errorf("state %s, gaps %+v; want the download untouched and the client named", got.State, report.Gaps)
	}
}

// A client that could not be asked is named by id, and asking it again later closes what it lost (TRACK-18).
func TestReconcileDownloads_AClientAskedAgainIsReconciledAfterItWasUnreachable(t *testing.T) {
	f := newFixture(t)
	acquisition := f.acquisition(t, "nzo-1")
	reachable := false
	client := recovery.DownloadClient{ID: 4, Name: "Usenet", Categories: []string{"solarr-tv"},
		Held: func(context.Context, []string) ([]recovery.HeldDownload, error) {
			if !reachable {
				return nil, errors.New("connection refused")
			}
			return nil, nil
		}}
	report := f.restore(t, client)
	if want := []int64{4}; !reflect.DeepEqual(report.Unreached, want) {
		t.Fatalf("unreached = %v, want %v", report.Unreached, want)
	}

	reachable = true
	again, err := recovery.ReconcileDownloads(t.Context(), f.clock, f.library, []recovery.DownloadClient{client})
	if err != nil {
		t.Fatalf("ReconcileDownloads: %v", err)
	}
	got, _ := library.NewAcquisitions(f.library).Get(t.Context(), acquisition.ID)
	if got.State != "CANCELLED" || got.LastError != recovery.LostInRestore || again.DownloadsClosed != 1 || len(again.Unreached) != 0 {
		t.Errorf("state %s (%q), closed %d, unreached %v; want CANCELLED with the restore reason, 1, none", got.State, got.LastError, again.DownloadsClosed, again.Unreached)
	}
}
