package scan

import (
	"context"
	"errors"
)

// observeConcurrency is how many directories are identified at once. Identification waits on
// metadata lookups, not the disk or the database: one after another the main library took
// 21 hours at 165 files a minute (2026-09-25), well inside every provider's rate limit.
const observeConcurrency = 4

// pipeline observes up to observeConcurrency batches at once and advances the cursor strictly in
// walk order, so a restart resumes after the last batch that, with every one before it, committed.
type pipeline struct {
	observe func(ctx context.Context, batch Batch) error
	advance func(ctx context.Context, directory string) error
	window  []pending
	failed  error
}

type pending struct {
	directory string
	done      chan error
}

// submit starts one batch, first finishing the oldest when the window is full.
func (p *pipeline) submit(ctx context.Context, batch Batch) error {
	if p.failed != nil {
		return p.failed
	}
	for len(p.window) >= observeConcurrency {
		if err := p.finishOldest(ctx); err != nil {
			return err
		}
	}
	slot := pending{directory: batch.Directory, done: make(chan error, 1)}
	p.window = append(p.window, slot)
	go func() { slot.done <- p.observe(ctx, batch) }()
	return nil
}

// finishOldest waits for the oldest batch and advances the cursor past it.
func (p *pipeline) finishOldest(ctx context.Context) error {
	oldest := p.window[0]
	p.window = p.window[1:]
	if err := <-oldest.done; err != nil {
		p.failed = err
		return err
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
