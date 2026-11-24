package transcode

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// stallAfter is how long a running conversion may report nothing before it is ended.
const stallAfter = 10 * time.Minute

// stderrTail is how much of ffmpeg's error output is kept for the failure message.
const stderrTail = 4096

// Progress is one report of a running conversion.
type Progress struct {
	OutTime time.Duration
	Speed   float64
	Done    bool
}

// Process is one background ffmpeg run that can be paused and ended.
type Process interface {
	// Wait returns nil after a clean exit and a *RunError otherwise.
	Wait() error
	Pause() error
	Resume() error
	Paused() bool
}

// Starter starts ffmpeg at the lowest priority; ExecStarter is the real one.
type Starter interface {
	Start(ctx context.Context, args []string, onProgress func(Progress), scratch string) (Process, error)
}

// RunError is why a conversion did not finish: the exit code, ffmpeg's last line, and whether Solarr ended it.
type RunError struct {
	ExitCode int
	LastLine string
	Stalled  bool
	Killed   bool
}

func (e *RunError) Error() string {
	switch {
	case e.Stalled:
		return "transcode: the conversion reported nothing and was ended"
	case e.Killed:
		return "transcode: the conversion was ended"
	}
	return fmt.Sprintf("transcode: ffmpeg exited %d: %s", e.ExitCode, e.LastLine)
}

// ExecStarter runs the real ffmpeg; zero Nice and IONice leave the priorities alone.
type ExecStarter struct {
	Nice   mediatool.Nice
	IONice mediatool.IONice
	Clock  clock.Clock
	// Binary is what runs; empty means ffmpeg.
	Binary string
}

// tailBuffer keeps the last room bytes written to it.
type tailBuffer struct {
	mu   sync.Mutex
	data []byte
	room int
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, p...)
	if len(b.data) > b.room {
		b.data = b.data[len(b.data)-b.room:]
	}
	return len(p), nil
}

func (b *tailBuffer) last() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return lastLine(string(bytes.TrimSpace(b.data)))
}

type execProcess struct {
	command *exec.Cmd
	timer   clock.Timer
	stderr  *tailBuffer
	paused  atomic.Bool
	stalled atomic.Bool
	killed  atomic.Bool
	done    chan struct{}
	err     error
}

// Start runs ffmpeg in its own process group so a signal reaches everything it started.
func (e ExecStarter) Start(ctx context.Context, args []string, onProgress func(Progress), scratch string) (Process, error) {
	binary := e.Binary
	if binary == "" {
		binary = "ffmpeg"
	}
	name, wrapped := e.IONice.Wrap(binary, args)
	name, wrapped = e.Nice.Wrap(name, wrapped)
	command := exec.Command(name, wrapped...)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if scratch != "" {
		command.Env = append(os.Environ(), "TMPDIR="+scratch)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("transcode: %w", err)
	}
	process := &execProcess{command: command, stderr: &tailBuffer{room: stderrTail}, done: make(chan struct{})}
	command.Stderr = process.stderr
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("transcode: start %s: %w", binary, err)
	}
	process.timer = e.Clock.NewTimer(stallAfter)
	go process.read(stdout, onProgress)
	go process.watch(ctx)
	return process, nil
}

func (p *execProcess) read(stdout io.Reader, onProgress func(Progress)) {
	var block Progress
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		if parseProgress(scanner.Text(), &block) {
			p.timer.Reset(stallAfter)
			if onProgress != nil {
				onProgress(block)
			}
		}
	}
	p.finish(p.command.Wait())
}

func (p *execProcess) finish(waitErr error) {
	p.timer.Stop()
	if waitErr != nil {
		runErr := &RunError{ExitCode: -1, LastLine: p.stderr.last(), Stalled: p.stalled.Load(), Killed: p.killed.Load()}
		var exit *exec.ExitError
		if errors.As(waitErr, &exit) {
			runErr.ExitCode = exit.ExitCode()
		}
		p.err = runErr
	}
	close(p.done)
}

// watch ends the process when the context ends or it has been silent too long while not paused.
func (p *execProcess) watch(ctx context.Context) {
	for {
		select {
		case <-p.done:
			return
		case <-ctx.Done():
			p.killed.Store(true)
			_ = p.signal(syscall.SIGKILL) // the process may have just exited
			return
		case <-p.timer.C():
			if p.paused.Load() {
				p.timer.Reset(stallAfter)
				continue
			}
			p.stalled.Store(true)
			_ = p.signal(syscall.SIGKILL) // the process may have just exited
			return
		}
	}
}

func (p *execProcess) signal(signal syscall.Signal) error {
	return syscall.Kill(-p.command.Process.Pid, signal)
}

func (p *execProcess) Wait() error {
	<-p.done
	return p.err
}

func (p *execProcess) Pause() error {
	if err := p.signal(syscall.SIGSTOP); err != nil {
		return err
	}
	p.paused.Store(true)
	return nil
}

func (p *execProcess) Resume() error {
	p.timer.Reset(stallAfter)
	p.paused.Store(false)
	return p.signal(syscall.SIGCONT)
}

func (p *execProcess) Paused() bool { return p.paused.Load() }

// parseProgress folds one line of `-progress` output into block and says when a block is complete.
func parseProgress(line string, block *Progress) bool {
	key, value, found := strings.Cut(strings.TrimSpace(line), "=")
	if !found {
		return false
	}
	switch key {
	case "out_time_us", "out_time_ms":
		// ffmpeg's out_time_ms is in microseconds too.
		if micros, err := strconv.ParseInt(value, 10, 64); err == nil && micros >= 0 {
			block.OutTime = time.Duration(micros) * time.Microsecond
		}
	case "speed":
		speed, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(value), "x"), 64)
		if err != nil {
			speed = 0
		}
		block.Speed = speed
	case "progress":
		block.Done = value == "end"
		return true
	}
	return false
}

// Pid is the process id of ffmpeg, which `nice` and `ionice` replace rather than parent.
func (p *execProcess) Pid() int { return p.command.Process.Pid }
