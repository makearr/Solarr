package transcode_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/transcode"
)

type collected struct {
	mu      sync.Mutex
	reports []transcode.Progress
}

func (c *collected) add(p transcode.Progress) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reports = append(c.reports, p)
}

func shell(script string) []string { return []string{"-c", script} }

func start(ctx context.Context, t *testing.T, clk clock.Clock, script string, got *collected) transcode.Process {
	t.Helper()
	process, err := transcode.ExecStarter{Binary: "sh", Clock: clk}.Start(ctx, shell(script), got.add, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return process
}

func TestExecStarter_ReportsOneProgressPerBlockAndEndsClean(t *testing.T) {
	var got collected
	process := start(t.Context(), t, clock.NewFakeAtEpoch(), `printf 'out_time_us=1500000\nspeed=2.5x\nprogress=continue\nout_time_ms=3000000\nspeed=N/A\nprogress=end\n'`, &got)
	if err := process.Wait(); err != nil {
		t.Fatal(err)
	}
	want := []transcode.Progress{{OutTime: 1500 * time.Millisecond, Speed: 2.5}, {OutTime: 3 * time.Second, Done: true}}
	if !reflect.DeepEqual(got.reports, want) {
		t.Errorf("reports = %+v, want %+v", got.reports, want)
	}
}

func TestExecStarter_AFailureCarriesTheExitCodeAndFfmpegsLastLine(t *testing.T) {
	var got collected
	err := start(t.Context(), t, clock.NewFakeAtEpoch(), `echo first >&2; echo "Invalid data found" >&2; exit 3`, &got).Wait()
	var runErr *transcode.RunError
	if !errors.As(err, &runErr) || runErr.ExitCode != 3 || runErr.LastLine != "Invalid data found" || runErr.Killed || runErr.Stalled {
		t.Fatalf("err = %#v, want exit 3 and the last line", err)
	}
}

func TestExecStarter_ACancelledContextKillsTheWholeGroup(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	var got collected
	// The background sleep keeps stdout open: Wait returns only if the group, not just the shell, is killed.
	process := start(ctx, t, clock.NewFakeAtEpoch(), `sleep 30 & echo progress=continue; wait`, &got)
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		got.mu.Lock()
		reported := len(got.reports)
		got.mu.Unlock()
		if reported > 0 {
			break
		}
	}
	cancel()
	var runErr *transcode.RunError
	if err := process.Wait(); !errors.As(err, &runErr) || !runErr.Killed {
		t.Fatalf("err = %#v, want killed", err)
	}
}

func TestExecStarter_TenSilentMinutesEndItButAPauseDoesNotCount(t *testing.T) {
	clk := clock.NewFakeAtEpoch()
	var got collected
	process := start(t.Context(), t, clk, `sleep 30`, &got)
	clk.BlockUntilWaiters(1)
	if err := process.Pause(); err != nil || !process.Paused() {
		t.Fatalf("Pause = %v, paused %v", err, process.Paused())
	}
	clk.Advance(10 * time.Minute)
	clk.BlockUntilWaiters(1)
	if !process.Paused() {
		t.Fatal("paused flag lost")
	}
	if err := process.Resume(); err != nil || process.Paused() {
		t.Fatalf("Resume = %v, paused %v", err, process.Paused())
	}
	clk.Advance(10 * time.Minute)
	var runErr *transcode.RunError
	if err := process.Wait(); !errors.As(err, &runErr) || !runErr.Stalled {
		t.Fatalf("err = %#v, want stalled", err)
	}
}

func TestFakeStarter_ScriptsReportsAndFailures(t *testing.T) {
	var got collected
	fake := &transcode.FakeStarter{Next: func([]string) transcode.FakeRun {
		return transcode.FakeRun{Reports: []transcode.Progress{{OutTime: time.Second}}, Err: &transcode.RunError{ExitCode: 1, LastLine: "bad"}}
	}}
	process, err := fake.Start(t.Context(), []string{"-i", "a"}, got.add, "")
	if err != nil {
		t.Fatal(err)
	}
	var runErr *transcode.RunError
	if err := process.Wait(); !errors.As(err, &runErr) || runErr.ExitCode != 1 || len(got.reports) != 1 || !reflect.DeepEqual(fake.Started(), [][]string{{"-i", "a"}}) {
		t.Fatalf("err = %v, reports = %v, started = %v", err, got.reports, fake.Started())
	}
}
