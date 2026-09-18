package jobs

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
)

// DefaultTick is how often the dispatcher looks for work.
const DefaultTick = time.Second

// PoolSize is one pool's worker count.
type PoolSize struct {
	Pool     Pool
	Capacity int
}

// ImportCapacity is fixed at one and is not configurable (ADR-3).
const ImportCapacity = 1

// workerPool runs jobs of one Pool with bounded concurrency.
type workerPool struct {
	pool     Pool
	slots    chan struct{}
	executor *Executor
	running  sync.WaitGroup
}

func newWorkerPool(pool Pool, capacity int, executor *Executor) *workerPool {
	if pool == PoolImport {
		capacity = ImportCapacity
	}
	if capacity < 1 {
		capacity = 1
	}
	return &workerPool{pool: pool, slots: make(chan struct{}, capacity), executor: executor}
}

// freeCapacity reports how many jobs this pool could take right now.
func (w *workerPool) freeCapacity() int { return cap(w.slots) - len(w.slots) }

// submit starts a job in its own goroutine and returns immediately. The
// dispatcher must never wait for a job (Invariant 23).
func (w *workerPool) submit(ctx context.Context, job Job) bool {
	select {
	case w.slots <- struct{}{}:
	default:
		return false
	}

	w.running.Add(1)
	go func() {
		defer w.running.Done()
		defer func() { <-w.slots }()
		w.executor.Execute(ctx, job)
	}()
	return true
}

// wait blocks until every running job in this pool has finished.
func (w *workerPool) wait() { w.running.Wait() }

// DispatcherConfig wires the dispatch loop.
type DispatcherConfig struct {
	// Sizes gives each pool its worker count. A pool with no entry is not
	// polled, so a deployment cannot accidentally run work it did not size.
	Sizes []PoolSize
	// Tick is how often to look for work. The loop never blocks on a job, so
	// this is a polling interval rather than a latency floor.
	Tick   time.Duration
	Logger *slog.Logger
}

// Dispatcher claims work and hands it to worker pools (Invariant 23).
type Dispatcher struct {
	queue    *Queue
	clock    clock.Clock
	logger   *slog.Logger
	pools    map[Pool]*workerPool
	order    []Pool
	tick     time.Duration
	leaseTTL time.Duration
	owner    string
}

// NewDispatcher builds a dispatcher over an executor's pools.
func NewDispatcher(queue *Queue, cl clock.Clock, executor *Executor, config DispatcherConfig) *Dispatcher {
	if config.Tick <= 0 {
		config.Tick = DefaultTick
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}

	pools := make(map[Pool]*workerPool, len(config.Sizes))
	order := make([]Pool, 0, len(config.Sizes))
	for _, size := range config.Sizes {
		if _, exists := pools[size.Pool]; exists {
			continue
		}
		pools[size.Pool] = newWorkerPool(size.Pool, size.Capacity, executor)
		order = append(order, size.Pool)
	}

	return &Dispatcher{
		queue:  queue,
		clock:  cl,
		logger: config.Logger,
		pools:  pools,
		order:  order,
		tick:   config.Tick,
		// Taken from the executor rather than configured again: the value that
		// claims a lease must be the value that renews it.
		leaseTTL: executor.LeaseTTL(),
		owner:    executor.Owner(),
	}
}

// Run drives the loop until ctx ends, then waits for in-flight jobs to finish.
func (d *Dispatcher) Run(ctx context.Context) {
	ticker := d.clock.NewTicker(d.tick)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// In-flight jobs finish or abandon cleanly; their leases are
			// released either way, so a restart reclaims immediately.
			d.drain()
			return
		case <-ticker.C():
			d.Tick(ctx)
		}
	}
}

// Tick is one pass: reclaim, then fill each pool (ADR-17).
func (d *Dispatcher) Tick(ctx context.Context) {
	d.reclaim(ctx)

	for _, pool := range d.order {
		workers := d.pools[pool]
		capacity := workers.freeCapacity()
		if capacity == 0 {
			continue
		}

		claimed, err := d.queue.Claim(ctx, pool, d.owner, capacity, d.leaseTTL)
		if err != nil {
			d.logger.Warn("could not claim jobs",
				slog.String("pool", string(pool)), slog.Any("error", err))
			continue
		}
		for _, job := range claimed {
			if !workers.submit(ctx, job) {
				// The pool filled between the capacity check and here. The job
				// keeps its lease and will be reclaimed rather than lost.
				d.logger.Debug("pool filled while dispatching",
					slog.String("pool", string(pool)), slog.Int64("job_id", job.ID))
			}
		}
	}
}

func (d *Dispatcher) reclaim(ctx context.Context) {
	reclaimed, err := d.queue.ReclaimExpired(ctx)
	if err != nil {
		d.logger.Warn("could not reclaim expired leases", slog.Any("error", err))
		return
	}
	for _, job := range reclaimed {
		d.logger.Warn("reclaimed an abandoned job",
			slog.Int64("job_id", job.ID), slog.String("type", string(job.Type)),
			slog.Int("attempts", job.Attempts))
	}
}

func (d *Dispatcher) drain() {
	for _, pool := range d.order {
		d.pools[pool].wait()
	}
}

// InFlight reports how many jobs are running in each pool, for the dashboard
// and for tests that need to wait for quiet.
func (d *Dispatcher) InFlight() map[Pool]int {
	counts := make(map[Pool]int, len(d.pools))
	for pool, workers := range d.pools {
		counts[pool] = cap(workers.slots) - workers.freeCapacity()
	}
	return counts
}
