package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
)

const (
	personDetailsJobType  jobs.Type = "people.details"
	peopleConvergeJobType jobs.Type = "people.converge"
	// peopleConvergeKey is the periodic action that merges the people two sources made of one human.
	peopleConvergeKey = "people_converge"
	// peopleConvergeScheduleCron runs daily at 04:10, after the night's refreshes brought new ids and births.
	peopleConvergeScheduleCron = "10 4 * * *"
)

var (
	personDetailsDefinition  = jobs.Definition{Name: "Reading a person", Type: personDetailsJobType, Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 2}
	peopleConvergeDefinition = jobs.Definition{Name: "Merging duplicate people", Type: peopleConvergeJobType, Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 1}
)

type personDetailsPayload struct {
	PersonID int64 `json:"person_id"`
}

// warmPerson reads a person's details ahead of the background sweeps: the page is open, so someone is waiting for it.
func (p *process) warmPerson(ctx context.Context, personID int64) {
	payload, err := json.Marshal(personDetailsPayload{PersonID: personID})
	if err == nil {
		_, err = p.queue.Enqueue(ctx, jobs.Request{
			Type: personDetailsJobType, ActionKey: fmt.Sprintf("%s:%d", personDetailsJobType, personID),
			Priority: jobs.PriorityInteractive, Payload: payload,
		})
	}
	if err == nil {
		err = p.refreshArtistNow(ctx, personID)
	}
	if err != nil {
		p.logger.Warn("enqueue person details", slog.Int64("person_id", personID), slog.Any("error", err))
	}
}

// personDetailsJob reads one person from every source that holds an id for them (a music artist is read by its own
// refresh job); a reader answers nil for a person it holds no id of.
type personDetailsJob struct {
	people  library.People
	readers []namedPersonReader
	clock   clock.Clock
	logger  *slog.Logger
}

type namedPersonReader struct {
	name string
	read func(ctx context.Context, personID int64) error
}

func (j personDetailsJob) Run(ctx context.Context, job jobs.Job) error {
	var payload personDetailsPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("people: decode details payload: %w", err)
	}
	for _, reader := range j.readers {
		if err := reader.read(ctx, payload.PersonID); err != nil {
			j.logger.Warn("person details not read", slog.String("source", reader.name), slog.Int64("person_id", payload.PersonID), slog.Any("error", err))
		}
	}
	// Recorded either way, so an unknown person is not asked again at every opening.
	return j.people.RecordRefresh(ctx, payload.PersonID, j.clock.Now().Unix())
}

type peopleConvergeJob struct {
	people library.People
	logger *slog.Logger
}

func (j peopleConvergeJob) Run(ctx context.Context, _ jobs.Job) error {
	merged, err := j.people.ConvergePeople(ctx)
	if err != nil {
		return fmt.Errorf("people: converge: %w", err)
	}
	j.logger.Info("duplicate people merged", slog.Int("merged", merged))
	return nil
}

// registerPeopleJobs wires the on-open details read and the daily merge of duplicate people.
func (p *process) registerPeopleJobs(registry *jobs.Registry, executor *jobs.Executor) error {
	for _, definition := range []jobs.Definition{personDetailsDefinition, peopleConvergeDefinition} {
		if err := registry.Register(definition); err != nil {
			return err
		}
	}
	people := library.NewPeople(p.library)
	executor.Handle(personDetailsJobType, personDetailsJob{
		people: people, readers: p.personReaders(people), clock: clock.NewSystem(), logger: p.logger,
	})
	executor.Handle(peopleConvergeJobType, peopleConvergeJob{people: people, logger: p.logger})
	return nil
}

// peopleConvergeTask is the registered "Merge duplicate people" action.
func (p *process) peopleConvergeTask() jobs.Task {
	return jobs.Task{
		Key: peopleConvergeKey, Name: "Merge duplicate people", DefaultCadence: peopleConvergeScheduleCron,
		Tier: string(settings.TierAdvanced), Category: jobs.CategoryMetadata, Startup: jobs.StartupOn,
		Enqueue: func(ctx context.Context) (jobs.EnqueueResult, error) {
			return p.queue.Enqueue(ctx, jobs.Request{
				Type: peopleConvergeJobType, ActionKey: string(peopleConvergeJobType), Priority: jobs.PriorityLow, Payload: []byte("{}"),
			})
		},
	}
}
