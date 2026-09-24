package jobs

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// Task is one periodic action, registered once in Go (ADR-59). Every
// existing "enqueued once at startup, waits for a restart" function
// (scan, enrichment, subscription expand, artwork, category counts, probe,
// capacity) becomes one of these.
type Task struct {
	// Key is the stable identifier -- what ScheduleSource and ScheduleStates
	// key their own rows on, and what the API and screen address a task by.
	Key string
	// Name is shown on the scheduler screen. Plain text today; an i18n key
	// once the interface has more than one language (every other UI-facing
	// string in this codebase carries the same deferred note).
	Name string
	// DefaultCadence is a five-field cron expression, the fallback a
	// ScheduleSource resolves to when nothing overrides it.
	DefaultCadence string
	// Tier is ADR-26's normal/advanced/expert vocabulary. A plain string,
	// not settings.Tier -- this package never imports internal/db/settings
	// (architecture §2), so the caller's own tier constant is passed as text.
	Tier string
	// Enqueue submits the actual work and reports what happened, so a
	// TaskRunner can record real outcomes (enqueued / already active /
	// failed) rather than assuming success.
	Enqueue func(context.Context) (EnqueueResult, error)
	// RunFull is an optional second trigger for a task whose ordinary cadence
	// does not do what a manual invocation sometimes needs -- library scan's
	// own full rescan (private/questions.md), which rereads every directory
	// regardless of mtime rather than the incremental sweep Enqueue always
	// runs. Nil for every task with no such distinction; RunNowFull refuses
	// with ErrNoFullMode when it is nil. Deliberately not a generic options
	// payload every task would have to grow a case for -- scan is still the
	// only task that needs one.
	RunFull func(context.Context) (EnqueueResult, error)
	// EnqueueManual, when set, is what "Run now" fires instead of Enqueue, for a task that
	// behaves differently when a person asks (a library's "Only After Manual Refresh").
	EnqueueManual func(context.Context) (EnqueueResult, error)
	// Category groups the task on the scheduler screen.
	Category TaskCategory
	// Startup says whether "run at startup" is offered, and its default.
	Startup Startup
}

// TaskCategory groups tasks on the scheduler screen.
type TaskCategory string

// The scheduler screen's groups, in the order it shows them.
const (
	CategoryLibrary     TaskCategory = "library"
	CategoryMetadata    TaskCategory = "metadata"
	CategoryDownloads   TaskCategory = "downloads"
	CategoryStatistics  TaskCategory = "statistics"
	CategoryMaintenance TaskCategory = "maintenance"
)

// Startup is whether a task may fire once when the process starts.
type Startup string

const (
	// StartupNotOffered is a task for which running at startup makes no sense, such as a backup.
	StartupNotOffered Startup = ""
	// StartupOn runs at startup unless the user switches it off.
	StartupOn Startup = "on"
	// StartupOff is offered but off unless the user switches it on.
	StartupOff Startup = "off"
)

// TaskRegistry holds every registered periodic action. One instance for the
// whole process, built once at startup -- the same MustRegister-at-init
// shape Registry (job types) already uses.
type TaskRegistry struct {
	mu    sync.RWMutex
	tasks map[string]Task
	order []string
}

// NewTaskRegistry returns an empty registry.
func NewTaskRegistry() *TaskRegistry {
	return &TaskRegistry{tasks: make(map[string]Task)}
}

// Register adds a task, refusing a duplicate key or an unparseable cadence.
func (r *TaskRegistry) Register(task Task) error {
	if task.Key == "" {
		return fmt.Errorf("jobs: task has no key")
	}
	if task.Enqueue == nil {
		return fmt.Errorf("jobs: task %s has no Enqueue function", task.Key)
	}
	switch task.Category {
	case CategoryLibrary, CategoryMetadata, CategoryDownloads, CategoryStatistics, CategoryMaintenance:
	default:
		return fmt.Errorf("jobs: task %s has no valid category: %q", task.Key, task.Category)
	}
	if _, err := cronParser.Parse(task.DefaultCadence); err != nil {
		return fmt.Errorf("jobs: task %s default cadence %q: %w", task.Key, task.DefaultCadence, err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tasks[task.Key]; exists {
		return fmt.Errorf("jobs: task %s is already registered", task.Key)
	}
	r.tasks[task.Key] = task
	r.order = append(r.order, task.Key)
	return nil
}

// MustRegister panics on a duplicate or invalid task. For package
// initialisation, where there is no caller to return an error to and the
// failure is always a bug.
func (r *TaskRegistry) MustRegister(task Task) {
	if err := r.Register(task); err != nil {
		panic(err)
	}
}

// Lookup returns one task by key.
func (r *TaskRegistry) Lookup(key string) (Task, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	task, found := r.tasks[key]
	return task, found
}

// All lists every registered task, sorted by key, for the API and the
// scheduler screen.
func (r *TaskRegistry) All() []Task {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tasks := make([]Task, 0, len(r.tasks))
	for _, task := range r.tasks {
		tasks = append(tasks, task)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].Key < tasks[j].Key })
	return tasks
}

// InRegistrationOrder lists every task in the order it was registered, which is the order a
// startup run enqueues them in.
func (r *TaskRegistry) InRegistrationOrder() []Task {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tasks := make([]Task, 0, len(r.order))
	for _, key := range r.order {
		tasks = append(tasks, r.tasks[key])
	}
	return tasks
}
