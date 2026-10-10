package tv

import (
	"context"
	"log/slog"
	"strconv"
	"strings"

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

// bridgePerRun bounds one sweep; the shared TMDB bucket paces it, so a larger batch takes longer, never bursts.
const bridgePerRun = 300

// BridgeSource is the TMDB client as the bridge needs it.
type BridgeSource interface {
	Configured() bool
	Person(ctx context.Context, id int64) (tmdb.Person, error)
}

// ArtistIDSource is Wikidata as the bridge asks it: the MusicBrainz artist each item is, where it says.
type ArtistIDSource interface {
	MusicBrainzArtists(ctx context.Context, qids []string) (map[string]string, error)
}

// BridgeConfig is what a BridgeJob needs; Logger and Wikidata are optional (without it only people whose music side
// already carries the same item are merged).
type BridgeConfig struct {
	People   library.People
	Source   BridgeSource
	Wikidata ArtistIDSource
	Logger   *slog.Logger
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
	learned := map[string]int64{}
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
		} else {
			learned[strings.ToUpper(person.ExternalIDs.Wikidata)] = candidate.PersonID
		}
	}
	if already, err := j.config.People.BridgeLinked(ctx, bridgePerRun); err == nil {
		for id, qid := range already {
			learned[qid] = id
		}
	}
	merged += j.mergeIntoArtists(ctx, learned)
	j.logger.Info("people bridge sweep finished", slog.Int("asked", len(candidates)), slog.Int("linked", linked), slog.Int("merged", merged))
	return nil
}

// mergeIntoArtists asks Wikidata which of the items just learned are music artists the library already holds, and
// links the item to that artist, which merges the two (the artist survives).
func (j *BridgeJob) mergeIntoArtists(ctx context.Context, learned map[string]int64) int {
	if j.config.Wikidata == nil || len(learned) == 0 {
		return 0
	}
	qids := make([]string, 0, len(learned))
	for qid := range learned {
		qids = append(qids, qid)
	}
	artists, err := j.config.Wikidata.MusicBrainzArtists(ctx, qids)
	if err != nil {
		j.logger.WarnContext(ctx, "bridge: wikidata artist ids not read", slog.Any("error", err))
		return 0
	}
	merged := 0
	for qid, mbid := range artists {
		artist, found, err := j.config.People.FindByExternalID(ctx, "musicbrainz:artist", mbid)
		if err != nil || !found || artist == learned[qid] {
			continue
		}
		if survivor, err := j.config.People.LinkWikidata(ctx, artist, qid); err == nil && survivor == artist {
			merged++
		}
	}
	return merged
}

// EnqueueBridge submits one bridge sweep, deduplicated by BridgeActionKey.
func EnqueueBridge(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{Type: BridgeJobType, ActionKey: BridgeActionKey, Priority: jobs.PriorityLow})
}
