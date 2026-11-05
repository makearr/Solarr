package adult

import (
	"context"
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
)

// searchMemoTTL keeps a lookup for the scan around it: a search is asked again word for word when the scene is added.
const searchMemoTTL = time.Hour

// searchMemoEntries bounds the memory a long scan holds; at the bound what has expired goes, or everything.
const searchMemoEntries = 4096

// SearchMemo remembers provider searches, the empty answers too: a scene's lookup asks each provider the same words
// several times, and at about 12 requests per file a provider's rate is what limits an adult scan (2026-11-01).
type SearchMemo struct {
	clock clock.Clock

	mu      sync.Mutex
	answers map[string]memoAnswer
}

type memoAnswer struct {
	value   any
	expires time.Time
}

// NewSearchMemo builds an empty memo.
func NewSearchMemo(cl clock.Clock) *SearchMemo {
	return &SearchMemo{clock: cl, answers: map[string]memoAnswer{}}
}

func (m *SearchMemo) get(key string) (any, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	answer, found := m.answers[key]
	if !found || !answer.expires.After(m.clock.Now()) {
		return nil, false
	}
	return answer.value, true
}

func (m *SearchMemo) put(key string, value any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clock.Now()
	if len(m.answers) >= searchMemoEntries {
		for held, answer := range m.answers {
			if !answer.expires.After(now) {
				delete(m.answers, held)
			}
		}
		if len(m.answers) >= searchMemoEntries {
			clear(m.answers)
		}
	}
	m.answers[key] = memoAnswer{value: value, expires: now.Add(searchMemoTTL)}
}

// memoized answers ask, or what it answered within the hour; a failure is not kept. A nil memo always asks.
func memoized[T any](ctx context.Context, memo *SearchMemo, provider, term string, ask func() (T, error)) (T, error) {
	if memo == nil {
		return ask()
	}
	key := provider + "\x00" + term
	if held, found := memo.get(key); found {
		if value, typed := held.(T); typed {
			return value, nil
		}
	}
	value, err := ask()
	if err == nil && ctx.Err() == nil {
		memo.put(key, value)
	}
	return value, err
}
