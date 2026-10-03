package tv

import (
	"context"
	"log/slog"
	"strconv"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/metadata/tmdb"
)

// BridgeJobType is the registered type of the sweep that recognises one human across libraries.
const BridgeJobType = jobs.Type("people.bridge")

// BridgeActionKey deduplicates the sweep: one at a time (Invariant 16).
const BridgeActionKey = "people.bridge"

// BridgeDefinition declares the sweep: background metadata work, retried on transient failure.
var BridgeDefinition = jobs.Definition{
	Name: "Matching people across libraries",
	Type: BridgeJobType, Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 3,
}

// bridgePerRun bounds one sweep to what TMDB's shared bucket can take beside the other metadata work.
const bridgePerRun = 60

// BridgeSource is the TMDB client as the bridge needs it.
type BridgeSource interface {
	Configured() bool
	Person(ctx context.Context, id int64) (tmdb.Person, error)
}

// BridgeConfig is what a BridgeJob needs; Logger is optional.
type BridgeConfig struct {
	People library.People
	Source BridgeSource
	Logger *slog.Logger
}

// BridgeJob learns the Wikidata item of TV people who share a name with a music artist; the item is what merges them
// into one person (ADR-8), and a name alone never does.
type BridgeJob struct {
	config BridgeConfig
	logger *slog.Logger
}

// NewBridgeJob builds the handler.
func NewBridgeJob(config BridgeConfig) *BridgeJob {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &BridgeJob{config: config, logger: logger}
}

// Run asks TMDB about the next candidates; one failing never stops the others.
func (j *BridgeJob) Run(ctx context.Context, _ jobs.Job) error {
	if j.config.Source == nil || !j.config.Source.Configured() {
		return nil
	}
	candidates, err := j.config.People.BridgeCandidates(ctx, bridgePerRun)
	if err != nil {
		return err
	}
	linked, merged := 0, 0
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return jobs.Retryablef("people.bridge.context", string(BridgeJobType), err)
		}
		id, ok := ParseID(candidate.TMDBID)
		if !ok {
			continue
		}
		person, err := j.config.Source.Person(ctx, id)
		if err != nil {
			j.logger.WarnContext(ctx, "bridge: tmdb person not read", slog.Int64("tmdb", id), slog.Any("error", err))
			continue
		}
		if person.ExternalIDs.Wikidata == "" {
			if err := j.config.People.RuleOutWikidata(ctx, candidate.PersonID, strconv.FormatInt(id, 10)); err != nil {
				j.logger.WarnContext(ctx, "bridge: could not record", slog.Int64("person_id", candidate.PersonID), slog.Any("error", err))
			}
			continue
		}
		survivor, err := j.config.People.LinkWikidata(ctx, candidate.PersonID, person.ExternalIDs.Wikidata)
		if err != nil {
			j.logger.WarnContext(ctx, "bridge: wikidata not linked", slog.Int64("person_id", candidate.PersonID), slog.Any("error", err))
			continue
		}
		linked++
		if survivor != candidate.PersonID {
			merged++
		}
	}
	j.logger.Info("people bridge sweep finished", slog.Int("asked", len(candidates)), slog.Int("linked", linked), slog.Int("merged", merged))
	return nil
}

// EnqueueBridge submits one bridge sweep, deduplicated by BridgeActionKey.
func EnqueueBridge(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{Type: BridgeJobType, ActionKey: BridgeActionKey, Priority: jobs.PriorityLow})
}
