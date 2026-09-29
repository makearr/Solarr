package music

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
)

// RefreshArtistJobType is the artist page's "Refresh metadata" (asked for 2026-09-19).
const RefreshArtistJobType = jobs.Type("music.refresh_artist")

// RefreshArtistDefinition runs beside enrichment: the same provider, the refresh lane.
var RefreshArtistDefinition = jobs.Definition{
	Name: "Refreshing an artist",
	Type: RefreshArtistJobType, Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 2,
}

// RefreshArtistPayload names the artist.
type RefreshArtistPayload struct {
	PersonID int64 `json:"person_id"`
}

// EnqueueRefreshArtist asks for one artist's metadata again; a second click while one runs is folded in.
func EnqueueRefreshArtist(ctx context.Context, queue *jobs.Queue, personID int64) (jobs.EnqueueResult, error) {
	payload, err := json.Marshal(RefreshArtistPayload{PersonID: personID})
	if err != nil {
		return jobs.EnqueueResult{}, err
	}
	return queue.Enqueue(ctx, jobs.Request{
		Type: RefreshArtistJobType, ActionKey: fmt.Sprintf("%s:%d", RefreshArtistJobType, personID),
		Priority: jobs.PriorityNormal, Payload: payload,
	})
}

// RefreshArtistJob runs EnrichJob.RefreshArtist for one person.
type RefreshArtistJob struct{ Enrich *EnrichJob }

func (j RefreshArtistJob) Run(ctx context.Context, job jobs.Job) error {
	var payload RefreshArtistPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return jobs.Terminalf("refresh_artist.payload", string(job.Type), err)
	}
	return j.Enrich.RefreshArtist(ctx, payload.PersonID, true)
}

// RefreshArtistsJobType is the scheduled refresh of monitored artists (answered 2026-09-24).
const RefreshArtistsJobType = jobs.Type("music.refresh_artists")

// RefreshArtistsDefinition is background work on the refresh lane.
var RefreshArtistsDefinition = jobs.Definition{
	Name: "Refreshing artists",
	Type: RefreshArtistsJobType, Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 1,
}

// Each run refreshes at most this many artists, those not refreshed for refreshArtistsAge first,
// so a large library is covered over several runs without one run holding the lane for hours.
const (
	refreshArtistsBatch = 100
	refreshArtistsAge   = 6 * 24 * time.Hour
)

// RefreshArtistsJob is Sonarr's periodic "Refresh Series" for music: a scheduled, not manual,
// refresh, so each library's "Rescan after refresh" applies as Sonarr's does.
type RefreshArtistsJob struct{ Enrich *EnrichJob }

func (j RefreshArtistsJob) Run(ctx context.Context, _ jobs.Job) error {
	due, err := j.Enrich.people.ArtistsDueRefresh(ctx, j.Enrich.clock.Now().Add(-refreshArtistsAge).Unix(), refreshArtistsBatch)
	if err != nil {
		return err
	}
	for _, personID := range due {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := j.Enrich.RefreshArtist(ctx, personID, false); err != nil {
			j.Enrich.logger.WarnContext(ctx, "scheduled artist refresh failed", slog.Int64("person_id", personID), slog.Any("error", err))
		}
	}
	j.Enrich.logger.Info("scheduled artist refresh finished", slog.Int("artists", len(due)))
	return nil
}

// RefreshArtist fetches one artist's details, image and albums again, past the metadata cache,
// then has their folders rescanned as the library's "Rescan after refresh" says for a manual or
// a scheduled refresh.
func (j *EnrichJob) RefreshArtist(ctx context.Context, personID int64, manual bool) error {
	artistMBID, found, err := j.people.ExternalIDFor(ctx, personID, ProviderArtist)
	if err != nil {
		return fmt.Errorf("music: refresh artist %d: %w", personID, err)
	}
	if !found {
		return fmt.Errorf("music: refresh artist %d: no %s id to refresh from", personID, ProviderArtist)
	}
	fresh := musicbrainz.WithFresh(ctx)

	j.refreshDetails(fresh, personID, artistMBID)
	// The new discography lands in the cache; the next subscription expansion reads it from there.
	if _, err := j.provider.ArtistReleaseGroups(fresh, musicbrainz.PriorityRefresh, artistMBID); err != nil {
		j.logger.WarnContext(ctx, "artist refresh: discography not refreshed", slog.Int64("person_id", personID), slog.Any("error", err))
	}

	works, err := j.people.Discography(ctx, personID, CreditRoleArtist)
	if err != nil {
		return fmt.Errorf("music: refresh artist %d: discography: %w", personID, err)
	}
	var refreshed []int64
	var answers []albumAnswer
	artists := artistMemo{}
	for _, work := range works {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		releaseGroupMBID, ok, err := j.identity.WorkExternalID(ctx, work.ID, ProviderReleaseGroup)
		if err != nil || !ok {
			continue
		}
		rg, _, err := j.provider.Album(fresh, musicbrainz.PriorityRefresh, releaseGroupMBID, "")
		if err != nil {
			j.logger.WarnContext(ctx, "artist refresh: album not refreshed", slog.Int64("work_id", work.ID), slog.Any("error", err))
			continue
		}
		j.enrichCreditsAndGenres(ctx, work.ID, rg, artists)
		refreshed = append(refreshed, work.ID)
		answers = append(answers, albumAnswer{rg: rg})
	}
	j.describeAlbums(ctx, answers)
	for index, workID := range refreshed {
		j.enrichDescription(ctx, workID, answers[index].description)
	}

	if j.realigner != nil && len(refreshed) > 0 {
		if err := j.realigner.RealignWorks(ctx, refreshed, manual); err != nil {
			j.logger.WarnContext(ctx, "artist refresh: rescan failed", slog.Int64("person_id", personID), slog.Any("error", err))
		}
	}
	if err := j.people.RecordRefresh(ctx, personID, j.clock.Now().Unix()); err != nil {
		return fmt.Errorf("music: refresh artist %d: %w", personID, err)
	}
	j.logger.Info("artist refreshed", slog.Int64("person_id", personID), slog.Int("albums", len(refreshed)))
	return nil
}

// refreshDetails is the backfill's own details-and-picture pass for one artist; without a Wikidata
// source it refreshes the details alone, never recording that the artist has no picture.
func (j *EnrichJob) refreshDetails(ctx context.Context, personID int64, artistMBID string) {
	if j.artworks != (library.Artworks{}) && j.wikidataOn() {
		if j.backfillOne(ctx, library.PersonArtworkCandidate{PersonID: personID, ArtistMBID: artistMBID}) == backfillFailed {
			j.logger.WarnContext(ctx, "artist refresh: details not refreshed", slog.Int64("person_id", personID))
		}
		return
	}
	artist, err := j.provider.Artist(ctx, musicbrainz.PriorityRefresh, artistMBID)
	if err != nil {
		j.logger.WarnContext(ctx, "artist refresh: details not refreshed", slog.Int64("person_id", personID), slog.Any("error", err))
		return
	}
	if err := j.people.BackfillDetails(ctx, personID, library.PersonIdentity{
		Name: artist.Name, SortName: artist.SortName, Disambiguation: artist.Disambiguation,
		Kind: artist.Type, Gender: artist.Gender, Country: artist.Country, Born: artist.Born, Died: artist.Died,
	}); err != nil {
		j.logger.WarnContext(ctx, "artist refresh: details not saved", slog.Int64("person_id", personID), slog.Any("error", err))
	}
}
