package plan_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/plan"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/identify/priority"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/migrations"
)

type env struct {
	store    *store.Store
	clock    *clock.Fake
	perHour  int
	outcome  plan.Outcome
	searched []int64
}

func newEnv(t *testing.T) *env {
	t.Helper()
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := clock.NewFakeAtEpoch()
	fake.Set(time.Date(2026, time.September, 29, 12, 0, 0, 0, time.UTC))
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
	return &env{store: opened, clock: fake, perHour: 10}
}

// album adds a wanted album; monitored false makes it one nobody asked to search.
func (e *env) album(t *testing.T, title string, monitored bool) int64 {
	t.Helper()
	workIDs, err := library.NewIdentity(e.store).ResolveWorks(t.Context(), e.clock.Now(), []library.WorkLink{{
		Provider: "musicbrainz:release-group", ExternalID: "rg-" + title, Confidence: 1,
		Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: title},
	}})
	if err != nil {
		t.Fatal(err)
	}
	item, err := library.NewDesired(e.store).EnsureAlbumItem(t.Context(), library.AlbumRequest{
		WorkID: workIDs[0], CategoryID: 1, ProfileID: 1, Monitored: monitored,
	})
	if err != nil {
		t.Fatal(err)
	}
	return item.ID
}

func (e *env) planPass(t *testing.T) {
	t.Helper()
	job := plan.NewJob(plan.JobConfig{
		Hunt: library.NewHunt(e.store), Clock: e.clock, Logger: slog.New(slog.DiscardHandler),
		Facts: func(context.Context) (plan.Facts, error) {
			return plan.Facts{
				Policy:       plan.Policy{Weights: priority.DefaultWeights(), NewReleaseDays: 7},
				CategoryRank: func(int64) int64 { return 1 },
			}, nil
		},
		Searchable: func(item library.PlanInput) bool { return item.SatisfactionKind == "SET" },
	})
	if err := job.Run(t.Context(), jobs.Job{Type: plan.JobType}); err != nil {
		t.Fatalf("plan Run: %v", err)
	}
}

func (e *env) huntJob() *plan.HuntJob {
	return plan.NewHuntJob(plan.HuntConfig{
		Hunt: library.NewHunt(e.store), Clock: e.clock, Logger: slog.New(slog.DiscardHandler),
		PerHour: func(context.Context) (int, error) { return e.perHour, nil },
		Search: func(ctx context.Context, itemID int64) (plan.Outcome, error) {
			e.searched = append(e.searched, itemID)
			if e.outcome == plan.OutcomeSearched {
				return e.outcome, library.NewDesired(e.store).RecordSearched(ctx, itemID, e.clock.Now().Unix())
			}
			return e.outcome, nil
		},
	})
}

func run(t *testing.T, hunt *plan.HuntJob) {
	t.Helper()
	if err := hunt.Run(t.Context(), jobs.Job{Type: plan.HuntJobType}); err != nil {
		t.Fatalf("hunt Run: %v", err)
	}
}

func TestHuntSearchesOnlyMonitoredMissingAlbums(t *testing.T) {
	e := newEnv(t)
	wanted := e.album(t, "Zanuvode", true)
	e.album(t, "Unwanted", false)
	e.planPass(t)

	run(t, e.huntJob())
	if !slices.Equal(e.searched, []int64{wanted}) {
		t.Fatalf("searched %v, want only %d", e.searched, wanted)
	}
}

// Nothing is searched until a plan pass has queued it: the hunt reads hunt_queue, never desired_item.
func TestHuntSearchesNothingBeforeThePlanner(t *testing.T) {
	e := newEnv(t)
	e.album(t, "Zanuvode", true)
	run(t, e.huntJob())
	if len(e.searched) != 0 {
		t.Fatalf("searched %v before any plan pass", e.searched)
	}
}

func TestHuntSpendsNoMoreThanTheHourlyBudget(t *testing.T) {
	e := newEnv(t)
	e.perHour = 2
	for _, title := range []string{"A", "B", "C"} {
		e.album(t, title, true)
	}
	e.planPass(t)
	hunt := e.huntJob()

	run(t, hunt)
	e.clock.Advance(30 * time.Minute)
	run(t, hunt)
	if len(e.searched) != 2 {
		t.Fatalf("searched %v within one hour, want 2", e.searched)
	}
	e.clock.Advance(31 * time.Minute)
	run(t, hunt)
	if len(e.searched) != 3 {
		t.Fatalf("searched %v once the hour passed, want all 3", e.searched)
	}
}

// A searched album waits out its backoff, and the planner keeps that backoff when it replans.
func TestHuntBacksOffASearchedAlbumAcrossPlanPasses(t *testing.T) {
	e := newEnv(t)
	item := e.album(t, "Zanuvode", true)
	e.planPass(t)
	hunt := e.huntJob()
	run(t, hunt)

	e.clock.Advance(2 * time.Hour)
	e.planPass(t)
	run(t, hunt)
	if len(e.searched) != 1 {
		t.Fatalf("searched %v two hours after a search, want one search", e.searched)
	}
	e.clock.Advance(4*time.Hour + time.Minute)
	e.planPass(t)
	run(t, hunt)
	if !slices.Equal(e.searched, []int64{item, item}) {
		t.Fatalf("searched %v after the six-hour backoff, want a second search", e.searched)
	}
}

func TestHuntDoesNotChargeTheBudgetForADownloadOnItsWay(t *testing.T) {
	e := newEnv(t)
	e.perHour = 1
	e.album(t, "A", true)
	e.album(t, "B", true)
	e.planPass(t)
	e.outcome = plan.OutcomeInFlight

	run(t, e.huntJob())
	if len(e.searched) != 2 {
		t.Fatalf("looked at %v, want both: an album with a download on its way costs nothing", e.searched)
	}
}

func TestHuntDropsAnAlbumNoLongerWanted(t *testing.T) {
	e := newEnv(t)
	e.album(t, "Zanuvode", true)
	e.planPass(t)
	e.outcome = plan.OutcomeNotWanted
	hunt := e.huntJob()
	run(t, hunt)

	e.clock.Advance(30 * 24 * time.Hour)
	run(t, hunt)
	if len(e.searched) != 1 {
		t.Fatalf("looked at %v, want the unwanted album dropped until the planner says otherwise", e.searched)
	}
}

func TestHuntDoesNotChargeTheBudgetWhileTheLibraryHasNoRoom(t *testing.T) {
	e := newEnv(t)
	e.perHour = 1
	e.album(t, "A", true)
	e.album(t, "B", true)
	e.planPass(t)
	e.outcome = plan.OutcomeNoRoom
	hunt := e.huntJob()

	run(t, hunt)
	if len(e.searched) != 2 {
		t.Fatalf("looked at %v, want both: a library with no room costs no budget", e.searched)
	}
	e.clock.Advance(30 * time.Minute)
	run(t, hunt)
	if len(e.searched) != 2 {
		t.Fatalf("looked at %v within the hour, want them left alone until it passes", e.searched)
	}
}

// upgradable makes a monitored album complete-but-below-cutoff, as the cutoff job does.
func (e *env) upgradable(t *testing.T, title string) int64 {
	t.Helper()
	id := e.album(t, title, true)
	desired := library.NewDesired(e.store)
	if err := desired.SetAlbumState(t.Context(), id, "UPGRADABLE", 1); err != nil {
		t.Fatal(err)
	}
	return id
}

// Section 9.4: upgrades are promised a fifth of the hour's searches, and what a class leaves unused goes to the other.
func TestHuntKeepsAFifthOfTheBudgetForUpgradesAndSpillsTheRest(t *testing.T) {
	e := newEnv(t)
	e.perHour = 5
	for _, title := range []string{"A", "B", "C", "D", "E", "F"} {
		e.album(t, title, true)
	}
	upgrade := e.upgradable(t, "Up")
	e.planPass(t)
	run(t, e.huntJob())
	if len(e.searched) != 5 || !slices.Contains(e.searched, upgrade) {
		t.Fatalf("searched %v, want 5 searches including the upgrade %d", e.searched, upgrade)
	}

	spill := newEnv(t)
	spill.perHour = 5
	for _, title := range []string{"A", "B", "C"} {
		spill.upgradable(t, title)
	}
	spill.planPass(t)
	run(t, spill.huntJob())
	if len(spill.searched) != 3 {
		t.Fatalf("with nothing missing, searched %v, want all 3 upgrades", spill.searched)
	}
}
