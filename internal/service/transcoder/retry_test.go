package transcoder_test

import (
	"math/rand"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/service/transcoder"
)

var epoch = time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

func TestBackoffFor_DoublesFromFiveMinutesAndCapsAtSixHours(t *testing.T) {
	want := []time.Duration{5 * time.Minute, 10 * time.Minute, 20 * time.Minute, 40 * time.Minute, 80 * time.Minute, 160 * time.Minute, 320 * time.Minute, 6 * time.Hour, 6 * time.Hour}
	for step, wait := range want {
		if got := transcoder.BackoffFor(step); got != wait {
			t.Errorf("BackoffFor(%d) = %s, want %s", step, got, wait)
		}
	}
	if transcoder.BackoffFor(500) != 6*time.Hour {
		t.Error("a large step overflowed")
	}
}

func TestAfterError_ThreeDifferentFilesFailingEnterProbingAndEarlierFilesKeepTheirAttempts(t *testing.T) {
	health := transcoder.Health{}
	items := map[int64]transcoder.ItemState{}
	for id := int64(1); id <= 3; id++ {
		items[id], health = transcoder.AfterError(items[id], health, id, epoch)
	}
	if health.Mode != transcoder.ModeProbing || health.ProbesLeft != 3 || items[1].Attempts != 1 || items[1].State != transcoder.StateNeeded {
		t.Fatalf("health = %+v, item = %+v", health, items[1])
	}
	items[4], health = transcoder.AfterError(items[4], health, 4, epoch)
	if items[4].Attempts != 0 || health.ProbesLeft != 2 {
		t.Errorf("a failure while probing burned an attempt or kept the count: %+v %+v", items[4], health)
	}
	health = transcoder.AfterSuccess()
	if health.Mode != transcoder.ModeOK || health.ConsecutiveFailures != 0 || items[1].Attempts != 1 {
		t.Errorf("success = %+v", health)
	}
}

func TestAfterError_OneFileFailingThreeTimesFailsWithoutEverProbing(t *testing.T) {
	health := transcoder.Health{}
	var item transcoder.ItemState
	for range 3 {
		item, health = transcoder.AfterError(item, health, 7, epoch)
	}
	if item.State != transcoder.StateFailed || item.FailureCode != transcoder.FailureError || item.Attempts != 3 || health.Mode != transcoder.ModeOK {
		t.Errorf("item = %+v, health = %+v", item, health)
	}
}

func TestAfterError_ThreeFailedProbesBackOffFiveTenTwentyMinutes(t *testing.T) {
	health := transcoder.Health{Mode: transcoder.ModeProbing, ProbesLeft: 3}
	now := epoch
	for round, want := range []time.Duration{5 * time.Minute, 10 * time.Minute, 20 * time.Minute} {
		for id := int64(10); id < 13; id++ {
			_, health = transcoder.AfterError(transcoder.ItemState{}, health, id+int64(round)*10, now)
		}
		if health.Mode != transcoder.ModeBackoff || !health.BackoffUntil.Equal(now.Add(want)) || health.BackoffStep != round+1 {
			t.Fatalf("round %d: health = %+v, want backoff until %s", round, health, now.Add(want))
		}
		if early := transcoder.AfterBackoff(health, now.Add(want-time.Second)); early.Mode != transcoder.ModeBackoff {
			t.Fatalf("round %d: left the backoff early", round)
		}
		now = now.Add(want)
		health = transcoder.AfterBackoff(health, now)
		if health.Mode != transcoder.ModeProbing || health.ProbesLeft != 1 {
			t.Fatalf("round %d: after the wait = %+v", round, health)
		}
		health.ProbesLeft = 3
	}
}

func TestResumeNowSkipsTheWaitForOneProbe(t *testing.T) {
	health := transcoder.ResumeNow(transcoder.Health{Mode: transcoder.ModeBackoff, BackoffUntil: epoch.Add(time.Hour), BackoffStep: 2})
	if health.Mode != transcoder.ModeProbing || health.ProbesLeft != 1 || !health.BackoffUntil.IsZero() || health.BackoffStep != 2 {
		t.Errorf("health = %+v", health)
	}
}

func TestAfterInterruption_KeepsAttemptsAndMarksTheItemToRunFirst(t *testing.T) {
	got := transcoder.AfterInterruption(transcoder.ItemState{State: "running", Attempts: 2})
	if !got.Interrupted || got.State != transcoder.StateNeeded || got.Attempts != 2 {
		t.Errorf("item = %+v", got)
	}
}

func TestAfterRefused_LaddersOrEndsOrCountsAnAttempt(t *testing.T) {
	size, other := sizeFailure(), []transcoder.Reason{{Code: transcoder.ReasonDuration}}
	if got := transcoder.AfterRefused(transcoder.ItemState{Attempts: 1}, size, transcoder.PassSettings{Pass: 1}, true); got.Pass != 1 || got.Attempts != 1 || got.State != transcoder.StateNeeded {
		t.Errorf("laddered = %+v", got)
	}
	if got := transcoder.AfterRefused(transcoder.ItemState{Pass: 2}, size, transcoder.PassSettings{}, false); got.State != transcoder.StateFailed || got.FailureCode != "result_above_range" {
		t.Errorf("exhausted = %+v", got)
	}
	item := transcoder.ItemState{}
	for range 3 {
		item = transcoder.AfterRefused(item, other, transcoder.PassSettings{}, false)
	}
	if item.State != transcoder.StateFailed || item.FailureCode != "duration_mismatch" || item.Attempts != 3 {
		t.Errorf("attempts = %+v", item)
	}
}

func TestRetryRules_AnySequenceOfOutcomesKeepsAttemptsAtThreeAndBackoffAtSixHours(t *testing.T) {
	random := rand.New(rand.NewSource(7))
	for trial := range 50 {
		health, items := transcoder.Health{}, make([]transcoder.ItemState, 5)
		now := epoch
		for range 200 {
			id := random.Intn(len(items))
			if items[id].State == transcoder.StateFailed {
				continue
			}
			switch random.Intn(5) {
			case 0, 1:
				items[id], health = transcoder.AfterError(items[id], health, int64(id+1), now)
			case 2:
				health = transcoder.AfterSuccess()
			case 3:
				items[id] = transcoder.AfterInterruption(items[id])
			default:
				now = now.Add(time.Duration(random.Intn(8)) * time.Hour)
				health = transcoder.AfterBackoff(health, now)
			}
			if items[id].Attempts > 3 || health.BackoffUntil.Sub(now) > 6*time.Hour || health.ProbesLeft < 0 {
				t.Fatalf("trial %d: item %+v health %+v", trial, items[id], health)
			}
		}
	}
}
