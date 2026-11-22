package music

import (
	"sync"

	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// probeMemoCap bounds the probes kept for a session that never reaches its batch; past it the memo starts over.
const probeMemoCap = 20000

// probeMemo hands a probe read once to the next reader of the same file and forgets it, so a file is never read twice
// for one import.
type probeMemo struct {
	mu     sync.Mutex
	byPath map[string]mediatool.Probe
}

func (m *probeMemo) put(absolute string, probe mediatool.Probe) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.byPath == nil || len(m.byPath) >= probeMemoCap {
		m.byPath = map[string]mediatool.Probe{}
	}
	m.byPath[absolute] = probe
}

func (m *probeMemo) take(absolute string) (mediatool.Probe, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	probe, found := m.byPath[absolute]
	delete(m.byPath, absolute)
	return probe, found
}
