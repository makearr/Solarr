package specprovider

import (
	"context"
	"sync"
	"time"
)

// answerCacheLimit bounds the answers kept; the oldest goes first.
const answerCacheLimit = 256

type cachedAnswer struct {
	records []Record
	until   time.Time
}

// answerCache keeps what a definition's `cache:` asks for (search and item lifetimes) in memory: an answer is disposable.
type answerCache struct {
	mutex   sync.Mutex
	answers map[string]cachedAnswer
	order   []string
}

// cached answers from memory while the definition's lifetime for the kind holds; only a successful answer is kept.
func (p *Provider) cached(_ context.Context, kind, key string, run func() ([]Record, error)) ([]Record, error) {
	lifetime, _ := time.ParseDuration(p.def.Cache[kind])
	if lifetime <= 0 {
		return run()
	}
	now := p.clock.Now()
	p.answers.mutex.Lock()
	if held, ok := p.answers.answers[key]; ok && now.Before(held.until) {
		p.answers.mutex.Unlock()
		return held.records, nil
	}
	p.answers.mutex.Unlock()
	records, err := run()
	if err != nil {
		return nil, err
	}
	p.answers.mutex.Lock()
	defer p.answers.mutex.Unlock()
	if p.answers.answers == nil {
		p.answers.answers = map[string]cachedAnswer{}
	}
	if _, present := p.answers.answers[key]; !present {
		p.answers.order = append(p.answers.order, key)
	}
	p.answers.answers[key] = cachedAnswer{records: records, until: now.Add(lifetime)}
	for len(p.answers.order) > answerCacheLimit {
		delete(p.answers.answers, p.answers.order[0])
		p.answers.order = p.answers.order[1:]
	}
	return records, nil
}
