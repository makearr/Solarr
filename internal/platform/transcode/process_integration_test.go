package transcode_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/mediatool"
	"github.com/makearr/Solarr/internal/platform/transcode"
)

type withPid interface{ Pid() int }

// statFields reads /proc/<pid>/stat after the command name, whose own spaces would break a plain split.
func statFields(t *testing.T, pid int) []string {
	t.Helper()
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		t.Skipf("no /proc: %v", err)
	}
	text := string(data)
	return strings.Fields(text[strings.LastIndex(text, ")")+2:])
}

func TestExecStarter_RealFFmpegRunsAtLowestPriorityReportsAndPausesInStateT(t *testing.T) {
	requireMediaTools(t)
	starter := transcode.ExecStarter{Nice: mediatool.Nice{Level: 19}, IONice: mediatool.IONice{Class: 3}, Clock: clock.System{}}
	var got collected
	args := []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-nostats", "-progress", "pipe:1", "-y", "-re", "-f", "lavfi", "-i", "testsrc=duration=30:size=64x64:rate=10", "-f", "null", "-"}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	process, err := starter.Start(ctx, args, got.add, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pid := process.(withPid).Pid()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		got.mu.Lock()
		seen := len(got.reports)
		got.mu.Unlock()
		if seen > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Fields after the command name start at the state (3rd of the stat line), so the nice value (19th) is index 16.
	// It is read once ffmpeg reports, because nice sets it just before it replaces itself.
	if fields := statFields(t, pid); fields[16] != "19" {
		t.Errorf("nice value = %s, want 19", fields[16])
	}
	if err := process.Pause(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if state := statFields(t, pid)[0]; state != "T" {
		t.Errorf("state while paused = %s, want T", state)
	}
	if err := process.Resume(); err != nil {
		t.Fatal(err)
	}
	cancel()
	var runErr *transcode.RunError
	if err := process.Wait(); !errors.As(err, &runErr) || !runErr.Killed {
		t.Errorf("err = %v, want killed", err)
	}
	if len(got.reports) == 0 {
		t.Error("no progress was reported")
	}
}

func TestExecStarter_RealFFmpegConvertsA3SecondFileAndReportsTheEnd(t *testing.T) {
	requireMediaTools(t)
	directory := t.TempDir()
	source := filepath.Join(directory, "in.mkv")
	ffmpeg(t, "-f", "lavfi", "-i", "testsrc=duration=3:size=160x120:rate=25", "-f", "lavfi", "-i", "sine=duration=3", "-c:v", "libx264", "-c:a", "aac", source)
	args, err := transcode.ConvertArgs(transcode.ConvertSpec{Input: source, Output: filepath.Join(directory, "out.mp4"), Container: "mp4",
		Video: []transcode.VideoOut{{Index: 0, Encoder: "libx264", Preset: "fast", Rate: quality(30)}}, Audio: []transcode.AudioOut{{Index: 1, Copy: true}}})
	if err != nil {
		t.Fatal(err)
	}
	var got collected
	process, err := transcode.ExecStarter{Nice: mediatool.Nice{Level: 19}, Clock: clock.System{}}.Start(t.Context(), args, got.add, directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Wait(); err != nil {
		t.Fatal(err)
	}
	if last := got.reports[len(got.reports)-1]; !last.Done || last.OutTime < 2*time.Second {
		t.Errorf("last report = %+v, want done after about 3s", last)
	}
}
