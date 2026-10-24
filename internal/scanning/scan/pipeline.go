package scan

import (
	"context"
	"errors"
)

// observeConcurrency is how many directories are identified at once. Identification waits on
// metadata lookups of 0.4-1.1s each (BrainzMash, measured 2026-09-27), not the disk or the
// database; eight in flight stays under its 10 requests a second, which the limiter enforces.
const observeConcurrency = 8

// observeWindow bounds the directories finished but not yet advanced past, so one slow directory
// no longer idles every other slot while the cursor waits for it (head-of-line blocking).
const observeWindow = 8 * observeConcurrency

// pipeline observes up to observeConcurrency batches at once and advances the cursor strictly in
// walk order, so a restart resumes after the last batch that, with every one before it, committed.
type pipeline struct {
	observe  func(ctx context.Context, batch Batch) error
	advance  func(ctx context.Context, directory string) error
	window   []pending
	running  int
	finished chan struct{}
	failed   error
}

type pending struct {
	directory string
	partial   bool
	done      chan error
}

// submit starts one batch once a slot and window room are free, advancing past every finished
// batch at the head of the window on the way.
func (p *pipeline) submit(ctx context.Context, batch Batch) error {
	if p.finished == nil {
		p.finished = make(chan struct{}, observeWindow)
	}
	for {
		if err := p.advanceFinished(ctx); err != nil {
			return err
		}
		if p.running < observeConcurrency && len(p.window) < observeWindow {
			break
		}
		<-p.finished
		p.running--
	}
	slot := pending{directory: batch.Directory, partial: batch.Partial, done: make(chan error, 1)}
	p.window = append(p.window, slot)
	p.running++
	go func() {
		slot.done <- p.observe(ctx, batch)
		p.finished <- struct{}{}
	}()
	return nil
}

// advanceFinished advances past the head of the window for as long as its batches are done.
func (p *pipeline) advanceFinished(ctx context.Context) error {
	if p.failed != nil {
		return p.failed
	}
	for len(p.window) > 0 {
		select {
		case err := <-p.window[0].done:
			if err := p.settleOldest(ctx, err); err != nil {
				return err
			}
		default:
			return nil
		}
	}
	return nil
}

// finishOldest waits for the oldest batch and advances the cursor past it.
func (p *pipeline) finishOldest(ctx context.Context) error {
	return p.settleOldest(ctx, <-p.window[0].done)
}

// settleOldest removes the oldest batch, whose outcome is observed, and advances past it.
func (p *pipeline) settleOldest(ctx context.Context, observed error) error {
	oldest := p.window[0]
	p.window = p.window[1:]
	if observed != nil {
		p.failed = observed
		return observed
	}
	if oldest.partial {
		return nil
	}
	if err := p.advance(ctx, oldest.directory); err != nil {
		p.failed = err
		return err
	}
	return nil
}

// wait finishes every batch still in flight; after a failure none of the later ones advances the
// cursor, but every one is still waited for, so nothing writes after the scan has returned.
func (p *pipeline) wait(ctx context.Context) error {
	var errs []error
	for len(p.window) > 0 {
		if p.failed != nil {
			oldest := p.window[0]
			p.window = p.window[1:]
			if err := <-oldest.done; err != nil {
				errs = append(errs, err)
			}
			continue
		}
		if err := p.finishOldest(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if p.failed != nil && len(errs) == 0 {
		return p.failed
	}
	return errors.Join(errs...)
}
