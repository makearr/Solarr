package app

import (
	"context"
	"errors"
	"log/slog"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// readCertificationAges reads the age of each certification stored before ages were, with its media type's reading;
// a failure is logged, never fatal: those works count as unrated until their next refresh.
func (p *process) readCertificationAges(ctx context.Context) {
	read, err := library.NewPeople(p.library).BackfillCertificationAges(ctx, func(mediaType, rating string) int64 {
		if reader, ok := media.Capability[media.Rating](p.media, media.Key(mediaType)); ok {
			if age, known := reader.RatingAge(rating); known {
				return age
			}
		}
		return -1
	})
	if err != nil {
		p.logger.Warn("certification ages not read", slog.Any("error", err))
	}
	if read > 0 {
		p.logger.Info("certification ages read", slog.Int("certifications", read))
	}
}

// ratingAdmits says whether a library's maximum rating lets in a work admitting age (-1 unrated).
func (p *process) ratingAdmits(categoryID, age int64) bool {
	category, found := p.cache.Snapshot().Category(categoryID)
	return !found || category.AdmitsAge(age)
}

// ratingLimit is a library's maximum rating; false when it has none.
func (p *process) ratingLimit(categoryID int64) (library.RatingLimit, bool) {
	category, found := p.cache.Snapshot().Category(categoryID)
	if !found || category.MaxAge == nil {
		return library.RatingLimit{}, false
	}
	return library.RatingLimit{MaxAge: *category.MaxAge, AllowUnrated: category.AllowUnrated}, true
}

// suggester names the library a work belongs in by the libraries' rules (plan 10-libraries L5).
func (p *process) suggester() adapters.Suggester {
	return adapters.Suggester{
		Registry: p.media, Settings: p.cache, People: library.NewPeople(p.library), Works: library.NewWorks(p.library),
	}
}

// pinProfilesOfLibrariesWithoutAList runs once: items added under another profile than their library's default keep
// judging by it as a visible override, since the stored profile id is no longer read (QUAL-6).
func (p *process) pinProfilesOfLibrariesWithoutAList(ctx context.Context) {
	const repair = "pin-stored-quality-profiles"
	if p.cache.Snapshot().OneOffDone(repair) {
		return
	}
	desired := library.NewDesired(p.library)
	snapshot := p.cache.Snapshot()
	for _, category := range snapshot.Categories() {
		if len(category.QualityProfileIDs) > 0 {
			continue
		}
		defaultID := judge.DefaultProfileID(snapshot, category)
		if defaultID == 0 {
			defaultID = p.defaultQualityProfileID
		}
		pinned, err := desired.PinOffDefaultProfiles(ctx, category.ID, defaultID)
		if err != nil {
			p.logger.Warn("stored quality profiles not pinned", slog.Any("error", err))
			return
		}
		if pinned > 0 {
			p.logger.Info("items keep the quality profile they were added with", slog.Int64("library", category.ID), slog.Int64("items", pinned))
		}
	}
	if err := p.cache.MarkOneOffDone(ctx, clock.NewSystem(), repair); err != nil {
		p.logger.Warn("quality profile pinning not recorded", slog.Any("error", err))
	}
}

// forgetRecordsOfRemovedLists runs once: the TMDB chart lists Discover took over were deleted by a settings migration, which
// cannot reach the records library.db keeps for them.
func (p *process) forgetRecordsOfRemovedLists(ctx context.Context) {
	const repair = "forget-records-of-folded-chart-lists"
	if p.cache.Snapshot().OneOffDone(repair) {
		return
	}
	records := library.NewImportLists(p.library)
	ids, err := records.ListIDs(ctx)
	if err != nil {
		p.logger.Warn("import list records not listed", slog.Any("error", err))
		return
	}
	for _, id := range ids {
		if _, err := p.cache.ImportList(ctx, id); !errors.Is(err, settings.ErrNotFound) {
			if err != nil {
				p.logger.Warn("import list not read", slog.Int64("list_id", id), slog.Any("error", err))
				return
			}
			continue
		}
		if err := records.Forget(ctx, id); err != nil {
			p.logger.Warn("import list records not dropped", slog.Int64("list_id", id), slog.Any("error", err))
			return
		}
	}
	if err := p.cache.MarkOneOffDone(ctx, clock.NewSystem(), repair); err != nil {
		p.logger.Warn("import list cleanup not recorded", slog.Any("error", err))
	}
}
