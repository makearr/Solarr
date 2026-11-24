package transcode

import (
	"context"
	"sync"
)

// FakeRun is what one fake conversion does: report, then end with Err once Block (when set) is closed or the context ends.
type FakeRun struct {
	Reports []Progress
	Err     error
	Block   chan struct{}
}

// FakeStarter runs scripted conversions for tests of whatever starts them; it records the arguments of each start.
type FakeStarter struct {
	mu      sync.Mutex
	started [][]string
	// Next scripts the next start; nil finishes cleanly at once.
	Next func(args []string) FakeRun
}

// Started answers the arguments of every start so far.
func (f *FakeStarter) Started() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]string(nil), f.started...)
}

// Start records the call and plays the script in the background.
func (f *FakeStarter) Start(ctx context.Context, args []string, onProgress func(Progress), _ string) (Process, error) {
	f.mu.Lock()
	f.started = append(f.started, args)
	f.mu.Unlock()
	run := FakeRun{}
	if f.Next != nil {
		run = f.Next(args)
	}
	process := &fakeProcess{done: make(chan struct{})}
	go func() {
		defer close(process.done)
		for _, report := range run.Reports {
			if onProgress != nil {
				onProgress(report)
			}
		}
		if run.Block != nil {
			select {
			case <-run.Block:
			case <-ctx.Done():
				process.err = &RunError{ExitCode: -1, Killed: true}
				return
			}
		}
		process.err = run.Err
	}()
	return process, nil
}

type fakeProcess struct {
	done   chan struct{}
	err    error
	mu     sync.Mutex
	paused bool
}

func (p *fakeProcess) Wait() error {
	<-p.done
	return p.err
}

func (p *fakeProcess) Pause() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.paused = true
	return nil
}

func (p *fakeProcess) Resume() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.paused = false
	return nil
}

func (p *fakeProcess) Paused() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.paused
}
