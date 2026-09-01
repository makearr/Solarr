package jobs

import (
	"fmt"
	"sort"
	"sync"
)

// defaultMaxAttempts bounds what a misclassified retryable error can cost.
const defaultMaxAttempts = 5

// Registry maps a job type to how it behaves. Registration happens once at
// startup, so an enqueue cannot assert that a type mutates while an executor
// believes it does not.
type Registry struct {
	mu          sync.RWMutex
	definitions map[Type]Definition
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{definitions: make(map[Type]Definition)}
}

// Register adds a job type. Registering one twice is a programming error and is
// refused rather than overwritten: the second registration would silently
// change the behaviour of work already queued under the first.
func (r *Registry) Register(definition Definition) error {
	if definition.Type == "" {
		return fmt.Errorf("jobs: definition has no type")
	}
	if definition.Pool == "" {
		return fmt.Errorf("jobs: %s has no pool", definition.Type)
	}
	if definition.MaxAttempts <= 0 {
		definition.MaxAttempts = defaultMaxAttempts
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.definitions[definition.Type]; exists {
		return fmt.Errorf("jobs: %s is already registered", definition.Type)
	}
	r.definitions[definition.Type] = definition
	return nil
}

// MustRegister panics on a duplicate. For use in package initialisation, where
// there is no caller to return an error to and the failure is always a bug.
func (r *Registry) MustRegister(definition Definition) {
	if err := r.Register(definition); err != nil {
		panic(err)
	}
}

// Lookup returns a type's definition.
func (r *Registry) Lookup(jobType Type) (Definition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	definition, found := r.definitions[jobType]
	return definition, found
}

// Types lists every registered type, sorted, for the API and for diagnostics.
func (r *Registry) Types() []Type {
	r.mu.RLock()
	defer r.mu.RUnlock()

	types := make([]Type, 0, len(r.definitions))
	for jobType := range r.definitions {
		types = append(types, jobType)
	}
	sort.Slice(types, func(i, j int) bool { return types[i] < types[j] })
	return types
}

// Pools lists every pool that has at least one registered type, so the
// dispatcher polls only pools that can have work.
func (r *Registry) Pools() []Pool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	seen := make(map[Pool]bool, len(r.definitions))
	for _, definition := range r.definitions {
		seen[definition.Pool] = true
	}
	pools := make([]Pool, 0, len(seen))
	for pool := range seen {
		pools = append(pools, pool)
	}
	sort.Slice(pools, func(i, j int) bool { return pools[i] < pools[j] })
	return pools
}
