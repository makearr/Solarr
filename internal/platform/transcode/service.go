package transcode

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"sync"
	"sync/atomic"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// ErrBusy is a conversion refused because the configured number already run.
var ErrBusy = errors.New("transcode: every conversion slot is in use")

// ErrStalled is a conversion that stopped sending and was ended.
var ErrStalled = errors.New("transcode: the conversion stopped sending and was ended")

// ErrUnavailable is a conversion asked for on a machine without ffmpeg.
var ErrUnavailable = errors.New("transcode: ffmpeg is not installed")

// ErrNoSuchTrack is a subtitle asked for that the file does not have.
var ErrNoSuchTrack = errors.New("transcode: no such subtitle")

// Piper runs a command with its standard output going to w; the seam that keeps the tests off ffmpeg.
type Piper interface {
	Pipe(ctx context.Context, w io.Writer, name string, args ...string) error
}

// ExecPiper runs the real thing; Dir is where ffmpeg runs and keeps its temporary files.
type ExecPiper struct {
	Dir    string
	IONice mediatool.IONice
}

// Pipe returns nil when ctx ended the command: a viewer who closed the player is not a failure.
func (e ExecPiper) Pipe(ctx context.Context, w io.Writer, name string, args ...string) error {
	name, args = e.IONice.Wrap(name, args)
	command := exec.CommandContext(ctx, name, args...)
	if e.Dir != "" {
		command.Dir = e.Dir
		command.Env = append(os.Environ(), "TMPDIR="+e.Dir)
	}
	var stderr bytes.Buffer
	command.Stdout = w
	command.Stderr = &limitedWriter{buffer: &stderr, room: 4096}
	if err := command.Run(); err != nil && ctx.Err() == nil {
		return fmt.Errorf("transcode: %s: %w: %s", name, err, lastLine(stderr.String()))
	}
	return nil
}

// limitedWriter keeps the first room bytes of what is written and swallows the rest.
type limitedWriter struct {
	buffer *bytes.Buffer
	room   int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.room > 0 {
		take := min(l.room, len(p))
		l.buffer.Write(p[:take])
		l.room -= take
	}
	return len(p), nil
}

// Service holds what detection found and bounds the conversions running at once.
type Service struct {
	runner Runner
	piper  Piper
	fsys   fsx.FS

	// stall ends a conversion that makes no output while the viewer is reading; zero turns it off.
	stall      time.Duration
	stallClock clock.Clock

	mu     sync.RWMutex
	found  []Capability
	active int
	owned  map[string]ownedStream
}

// ownedStream is a running conversion that a newer one by the same owner may stop.
type ownedStream struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// NewService builds it; nothing is detected until Detect runs.
func NewService(runner Runner, piper Piper, fsys fsx.FS) *Service {
	return &Service{runner: runner, piper: piper, fsys: fsys, owned: map[string]ownedStream{}}
}

// WithStallWatch makes a conversion that sends nothing for after, while the viewer is not holding it up, end at once so
// the viewer's player asks again and gets a fresh one. A viewer who paused is not a stall: the writer is then busy.
func (s *Service) WithStallWatch(c clock.Clock, after time.Duration) *Service {
	s.stallClock, s.stall = c, after
	return s
}

// ClearScratch removes what a conversion that was cut off left in dir; ffmpeg deletes its own files otherwise.
func (s *Service) ClearScratch(dir string) error {
	entries, err := s.fsys.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir {
			continue
		}
		if err := s.fsys.Remove(path.Join(dir, entry.Name)); err != nil {
			return err
		}
	}
	return nil
}

// Detect tries every encoder again and keeps the outcome.
func (s *Service) Detect(ctx context.Context) []Capability {
	found := Detect(ctx, s.runner, s.fsys)
	s.mu.Lock()
	s.found = found
	s.mu.Unlock()
	return found
}

// Capabilities is what the last detection found.
func (s *Service) Capabilities() []Capability {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Capability(nil), s.found...)
}

// FrameDecodeArgs is the hwaccel input options for a one-frame decode under the hardware policy; nil decodes on the processor.
func (s *Service) FrameDecodeArgs(hardware, device string) []string {
	resolution := s.Resolve(hardware, device)
	if resolution.Accel == Software {
		return nil
	}
	return DecodeArgs(Capability{Accel: resolution.Accel, Device: resolution.Device}, true)
}

// Available reports whether ffmpeg is there at all.
func (s *Service) Available() bool { return s.runner.Lookup(ffmpeg) == nil }

// Resolution is the encoder a policy comes to, and why when it is not the one asked for.
type Resolution struct {
	Accel  Accel
	Device string
	// Note is empty when the policy was met as written.
	Note string
}

// Resolve turns the hardware setting and the device override into the encoder that will run.
// A forced encoder that never worked falls back to software and says so, unless the device was
// named too: that is the operator overriding detection and is trusted.
func (s *Service) Resolve(hardware, device string) Resolution {
	switch Accel(hardware) {
	case Software:
		return Resolution{Accel: Software}
	case VAAPI, QSV, NVENC:
		forced := Accel(hardware)
		if device != "" {
			return Resolution{Accel: forced, Device: device}
		}
		for _, capability := range s.Capabilities() {
			if capability.Accel == forced && capability.Working {
				return Resolution{Accel: forced, Device: capability.Device}
			}
		}
		return Resolution{Accel: Software, Note: fmt.Sprintf("%s was chosen but no working %s encoder was found", hardware, hardware)}
	}
	best, ok := Best(s.Capabilities())
	if !ok {
		return Resolution{Accel: Software}
	}
	if device == "" {
		device = best.Device
	}
	return Resolution{Accel: best.Accel, Device: device}
}

// Stream converts spec into w. A GPU encoder that fails before sending a byte is retried one step down: whole
// path on the GPU, then GPU encoding only, then the processor.
func (s *Service) Stream(ctx context.Context, w io.Writer, spec Spec, maxSessions int64) error {
	if !s.Available() {
		return ErrUnavailable
	}
	ctx, finish := s.supersede(ctx, spec.Owner)
	defer finish()
	if !s.acquire(maxSessions) {
		return ErrBusy
	}
	defer s.release()

	counted := &countingWriter{w: w}
	var err error
	stalled := false
	for _, attempt := range attempts(spec) {
		stalled, err = s.pipeWatched(ctx, counted, attempt)
		if stalled && counted.written > 0 {
			return ErrStalled
		}
		if (err == nil && !stalled) || (!stalled && counted.written > 0) || ctx.Err() != nil {
			return err
		}
	}
	if stalled {
		return ErrStalled
	}
	return err
}

// pipeWatched runs one conversion, cancelling it when it sends nothing for the stall time while no write is pending.
func (s *Service) pipeWatched(ctx context.Context, counted *countingWriter, spec Spec) (stalled bool, err error) {
	if s.stall <= 0 || s.stallClock == nil {
		return false, s.piper.Pipe(ctx, counted, ffmpeg, Args(spec)...)
	}
	counted.watch(s.stallClock)
	attemptCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	var hung atomic.Bool
	go func() {
		ticker := s.stallClock.NewTicker(s.stall / 4)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C():
				if counted.quietFor(s.stallClock.Now()) >= s.stall {
					hung.Store(true)
					cancel()
					return
				}
			}
		}
	}()
	err = s.piper.Pipe(attemptCtx, counted, ffmpeg, Args(spec)...)
	close(done)
	return hung.Load(), err
}

// attempts is spec first, then each simpler way to do the same job.
func attempts(spec Spec) []Spec {
	if spec.Kind != Video || spec.Accel == Software {
		return []Spec{spec}
	}
	var steps []Spec
	if CanDecodeOnGPU(spec.Accel) {
		hardware := spec
		hardware.HardwareDecode = true
		steps = append(steps, hardware)
	}
	encodeOnly := spec
	encodeOnly.HardwareDecode = false
	software := spec
	software.Accel, software.Device, software.HardwareDecode = Software, "", false
	return append(steps, encodeOnly, software)
}

// supersede stops the owner's older conversion and waits for its slot to free; finish unregisters this one.
func (s *Service) supersede(ctx context.Context, owner string) (context.Context, func()) {
	if owner == "" {
		return ctx, func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	mine := ownedStream{cancel: cancel, done: make(chan struct{})}
	s.mu.Lock()
	older, replaced := s.owned[owner]
	s.owned[owner] = mine
	s.mu.Unlock()
	if replaced {
		older.cancel()
		<-older.done
	}
	return ctx, func() {
		s.mu.Lock()
		if s.owned[owner].done == mine.done {
			delete(s.owned, owner)
		}
		s.mu.Unlock()
		close(mine.done)
		cancel()
	}
}

func (s *Service) acquire(limit int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if int64(s.active) >= limit {
		return false
	}
	s.active++
	return true
}

func (s *Service) release() {
	s.mu.Lock()
	s.active--
	s.mu.Unlock()
}

// Active is how many conversions run now.
func (s *Service) Active() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.active
}

type countingWriter struct {
	w       io.Writer
	written int64

	mu      sync.Mutex
	clock   clock.Clock
	last    time.Time
	writing bool
}

// watch starts timing the quiet between writes.
func (c *countingWriter) watch(cl clock.Clock) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.clock, c.last, c.writing = cl, cl.Now(), false
}

// quietFor is how long nothing was written, zero while a write is waiting on the viewer.
func (c *countingWriter) quietFor(now time.Time) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.writing {
		return 0
	}
	return now.Sub(c.last)
}

func (c *countingWriter) Write(p []byte) (int, error) {
	c.mu.Lock()
	c.writing = true
	c.mu.Unlock()
	n, err := c.w.Write(p)
	c.mu.Lock()
	c.writing = false
	if c.clock != nil {
		c.last = c.clock.Now()
	}
	c.written += int64(n)
	c.mu.Unlock()
	return n, err
}
