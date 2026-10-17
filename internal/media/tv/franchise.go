package tv

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/wikidata"
)

// ProviderWikidata names the franchise a series is part of by its Wikidata item.
const ProviderWikidata = "wikidata"

// FranchiseSource is Wikidata as the franchise pass asks it: the franchise of a series and the series of a franchise.
type FranchiseSource interface {
	SeriesFranchise(ctx context.Context, tvdb, tmdb int64) (wikidata.Franchise, bool, error)
	FranchiseSeriesOf(ctx context.Context, qid string) ([]wikidata.FranchiseSeries, error)
}

// FranchisePassJobType is the registered type of the pass that finds the franchises series belong to.
const FranchisePassJobType = jobs.Type("tv.franchises")

// FranchisePassDefinition declares the pass: background metadata work, retried on transient failure.
var FranchisePassDefinition = jobs.Definition{
	Name: "Finding the franchises series belong to",
	Type: FranchisePassJobType, Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 3,
}

// franchisesPerRun bounds one pass: Wikimedia asks for polite use of its query service, so a library is worked off over days.
const franchisesPerRun = 25

// FranchisePass puts each series in the franchise Wikidata says it is part of, with the other series of it the library holds.
type FranchisePass struct {
	Source     FranchiseSource
	Franchises library.Franchises
	Series     library.Series
	Logger     *slog.Logger
}

// Run implements jobs.Handler: it asks about the next series; a failing query ends the run to be tried again later.
func (p FranchisePass) Run(ctx context.Context, _ jobs.Job) error {
	unchecked, err := p.Franchises.SeriesWithoutFranchiseCheck(ctx, franchisesPerRun)
	if err != nil {
		return err
	}
	found := 0
	for _, series := range unchecked {
		if err := ctx.Err(); err != nil {
			return jobs.Retryablef("tv.franchises.context", string(FranchisePassJobType), err)
		}
		franchise, ok, err := p.Source.SeriesFranchise(ctx, series.TVDB, series.TMDB)
		if err != nil {
			p.Logger.Warn("franchise lookup failed", slog.Int64("work_id", series.WorkID), slog.Any("error", err))
			break
		}
		if !ok {
			if err := p.Franchises.MarkChecked(ctx, series.WorkID); err != nil {
				return err
			}
			continue
		}
		members, err := p.Source.FranchiseSeriesOf(ctx, franchise.QID)
		if err != nil {
			p.Logger.Warn("franchise members not read", slog.String("franchise", franchise.QID), slog.Any("error", err))
			break
		}
		held := p.heldWorks(ctx, members)
		if len(members) >= 2 {
			held[series.WorkID] = true
			for workID := range held {
				if _, err := p.Franchises.Join(ctx, MediaType, ProviderWikidata, franchise.QID, franchise.Label, workID); err != nil {
					return err
				}
			}
			found++
		}
		for workID := range held {
			if err := p.Franchises.MarkChecked(ctx, workID); err != nil {
				return err
			}
		}
		if err := p.Franchises.MarkChecked(ctx, series.WorkID); err != nil {
			return err
		}
	}
	p.Logger.Info("franchise pass finished", slog.Int("asked", len(unchecked)), slog.Int("franchises", found))
	return nil
}

// heldWorks is the library's work for each member it holds, found by TheTVDB or TMDB id.
func (p FranchisePass) heldWorks(ctx context.Context, members []wikidata.FranchiseSeries) map[int64]bool {
	held := map[int64]bool{}
	for _, member := range members {
		if workID, ok := heldMember(ctx, p.Series, member); ok {
			held[workID] = true
		}
	}
	return held
}

func heldMember(ctx context.Context, series library.Series, member wikidata.FranchiseSeries) (int64, bool) {
	if member.TVDB > 0 {
		if id, found, err := series.WorkByProviderID(ctx, ProviderTVDB, strconv.FormatInt(member.TVDB, 10)); err == nil && found {
			return id, true
		}
	}
	if member.TMDB > 0 {
		if id, found, err := series.WorkByProviderID(ctx, ProviderTMDB, strconv.FormatInt(member.TMDB, 10)); err == nil && found {
			return id, true
		}
	}
	return 0, false
}

// EnqueueFranchisePass submits one pass; a second while one waits is the same work.
func EnqueueFranchisePass(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{Type: FranchisePassJobType, ActionKey: string(FranchisePassJobType), Priority: jobs.PriorityLow})
}

// Franchising is TV's part of a series' collections: the series Wikidata lists in a franchise that the library lacks,
// and following one, which adds them watching only what airs next.
type Franchising struct {
	Source           FranchiseSource
	Franchises       library.Franchises
	Series           library.Series
	Service          Service
	DefaultProfileID int64
	Categories       interface {
		Category(id int64) (settings.Category, bool)
		ProfileFor(category settings.Category, fallback int64) int64
	}
}

var _ media.Franchising = Franchising{}

func (f Franchising) missing(ctx context.Context, franchiseID int64) ([]wikidata.FranchiseSeries, []wikidata.FranchiseSeries, library.FranchiseRow, error) {
	row, err := f.Franchises.Get(ctx, franchiseID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, row, media.ErrNoFranchise
	}
	if err != nil || f.Source == nil || row.ExternalID == "" || row.Kind != library.FranchiseKind {
		return nil, nil, row, err
	}
	members, err := f.Source.FranchiseSeriesOf(ctx, row.ExternalID)
	if err != nil {
		return nil, nil, row, fmt.Errorf("%w: %w", media.ErrSourceUnavailable, err)
	}
	var absent []wikidata.FranchiseSeries
	for _, member := range members {
		if _, ok := heldMember(ctx, f.Series, member); !ok && (member.TVDB > 0 || member.TMDB > 0) {
			absent = append(absent, member)
		}
	}
	return members, absent, row, nil
}

// MissingFranchiseMembers implements media.Franchising.
func (f Franchising) MissingFranchiseMembers(ctx context.Context, _, franchiseID int64) ([]media.FranchiseMember, error) {
	_, absent, _, err := f.missing(ctx, franchiseID)
	if err != nil {
		return nil, err
	}
	out := make([]media.FranchiseMember, len(absent))
	for index, member := range absent {
		out[index] = media.FranchiseMember{Title: member.Label, Year: member.Year, Provider: ProviderTVDB, ExternalID: strconv.FormatInt(member.TVDB, 10)}
		if member.TVDB == 0 {
			out[index].Provider, out[index].ExternalID = ProviderTMDB, strconv.FormatInt(member.TMDB, 10)
		}
	}
	return out, nil
}

// FranchisePictures implements media.Franchising: Wikidata has none, so a franchise shows its first series' picture.
func (Franchising) FranchisePictures(context.Context, []media.FranchiseRef) map[int64]string {
	return nil
}

// MonitorFranchise implements media.Franchising: following adds every series of the franchise the library lacks.
func (f Franchising) MonitorFranchise(ctx context.Context, categoryID, franchiseID int64, monitored bool) (int, error) {
	added := 0
	var total int64
	if monitored {
		members, absent, _, err := f.missing(ctx, franchiseID)
		if err != nil {
			return 0, err
		}
		total = int64(len(members))
		category, _ := f.Categories.Category(categoryID)
		profile := f.Categories.ProfileFor(category, f.DefaultProfileID)
		for _, member := range absent {
			ids := IDs{TVDB: member.TVDB}
			if member.TVDB == 0 {
				ids = IDs{TMDB: member.TMDB}
			}
			if _, err := f.Service.Add(ctx, AddRequest{IDs: ids, CategoryID: categoryID, ProfileID: profile, Monitor: MonitorFuture}); err != nil {
				return added, fmt.Errorf("add %q: %w", member.Label, err)
			}
			added++
		}
	}
	if err := f.Franchises.SetMonitored(ctx, franchiseID, monitored, total); err != nil {
		return added, err
	}
	return added, nil
}
