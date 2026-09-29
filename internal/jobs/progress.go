package jobs

import (
	"slices"
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
)

// rateWindow is how far back a running job's rate is measured: the status bar's ETA is the rate
// over the last one to five minutes (2026-09-29).
const rateWindow = 5 * time.Minute

// rateMinimum is how much history a rate needs before an ETA is worth showing.
const rateMinimum = 20 * time.Second

// sampleEvery bounds the samples one job keeps: at most one per interval within rateWindow.
const sampleEvery = 5 * time.Second

type progressSample struct {
	at   time.Time
	done int64
}

// Progress is one running job's count of work done, for the status bar. A nil Progress ignores
// every call, so a handler reports without checking whether anyone listens.
type Progress struct {
	mu      sync.Mutex
	clock   clock.Clock
	done    int64
	total   int64
	samples []progressSample
}

// Set records done of total; a total of 0 means unknown.
func (p *Progress) Set(done, total int64) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.done, p.total = done, total
	p.sample()
}

// Add counts n more done.
func (p *Progress) Add(n int64) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.done += n
	p.sample()
}

// SetTotal records how much there is, once it is known.
func (p *Progress) SetTotal(total int64) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.total = total
}

// sample keeps one point per sampleEvery, dropping those older than rateWindow; the lock is held.
func (p *Progress) sample() {
	now := p.clock.Now()
	if n := len(p.samples); n > 0 && now.Sub(p.samples[n-1].at) < sampleEvery {
		p.samples[n-1].done = p.done
		return
	}
	p.samples = append(p.samples, progressSample{at: now, done: p.done})
	cut := 0
	for cut < len(p.samples)-1 && now.Sub(p.samples[cut].at) > rateWindow {
		cut++
	}
	p.samples = p.samples[cut:]
}

// RunningJob is one job being worked on now, as the status bar shows it.
type RunningJob struct {
	JobID      int64
	Type       Type
	CategoryID int64
	StartedAt  time.Time
	Done       int64
	// Total is 0 when the job does not know how much there is.
	Total int64
	// PerMinute is the rate over the last rateWindow, 0 until there is rateMinimum of history.
	PerMinute float64
	// Remaining is the time left at that rate, 0 when unknown.
	Remaining time.Duration
}

type runningEntry struct {
	job      Job
	started  time.Time
	progress *Progress
}

// Running is every job the executor is working on now, with its progress (2026-09-29).
type Running struct {
	mu    sync.Mutex
	clock clock.Clock
	jobs  map[int64]runningEntry
}

// NewRunning builds an empty registry.
func NewRunning(cl clock.Clock) *Running {
	return &Running{clock: cl, jobs: map[int64]runningEntry{}}
}

// Start lists job as running and answers where it reports progress; nil-safe.
func (r *Running) Start(job Job) *Progress {
	if r == nil {
		return nil
	}
	progress := &Progress{clock: r.clock}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.jobs[job.ID] = runningEntry{job: job, started: r.clock.Now(), progress: progress}
	return progress
}

// Finish takes a job off the list.
func (r *Running) Finish(jobID int64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.jobs, jobID)
}

// Snapshot answers the running jobs, longest running first.
func (r *Running) Snapshot() []RunningJob {
	r.mu.Lock()
	entries := make([]runningEntry, 0, len(r.jobs))
	for _, entry := range r.jobs {
		entries = append(entries, entry)
	}
	r.mu.Unlock()

	now := r.clock.Now()
	running := make([]RunningJob, len(entries))
	for i, entry := range entries {
		running[i] = entry.progress.view(now)
		running[i].JobID, running[i].Type, running[i].StartedAt = entry.job.ID, entry.job.Type, entry.started
		running[i].CategoryID = entry.job.CategoryID
	}
	slices.SortFunc(running, func(a, b RunningJob) int {
		if c := a.StartedAt.Compare(b.StartedAt); c != 0 {
			return c
		}
		return int(a.JobID - b.JobID)
	})
	return running
}

// view is the progress at now: the rate from the oldest sample still in the window.
func (p *Progress) view(now time.Time) RunningJob {
	p.mu.Lock()
	defer p.mu.Unlock()
	view := RunningJob{Done: p.done, Total: p.total}
	if len(p.samples) == 0 {
		return view
	}
	oldest := p.samples[len(p.samples)-1]
	for _, sample := range p.samples {
		if now.Sub(sample.at) <= rateWindow {
			oldest = sample
			break
		}
	}
	elapsed := now.Sub(oldest.at)
	if elapsed < rateMinimum || p.done <= oldest.done {
		return view
	}
	view.PerMinute = float64(p.done-oldest.done) / elapsed.Minutes()
	if p.total > p.done {
		view.Remaining = time.Duration(float64(p.total-p.done) / view.PerMinute * float64(time.Minute))
	}
	return view
}
