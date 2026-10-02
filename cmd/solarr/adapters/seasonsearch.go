package adapters

import (
	"context"
	"errors"
	"fmt"

	"github.com/makearr/Solarr/internal/acquiring/dispatch"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// SeasonSearch satisfies apiserver.SeasonAcquirer over the TV module's search and the shared dispatch. Decision and
// acquisition views are the music screens' own, so a TV season's results read the same.
type SeasonSearch struct {
	Acquire tv.Grab
	Reader  library.SeriesReader
	Desired library.Desired
	Conns   func(ctx context.Context) ([]judge.Conn, error)
	Clock   clock.Clock
	// Views builds the decision rows; its Search carries the stores they read.
	Views CandidateSearch
}

// item finds the season's SET item of a series in the library.
func (c SeasonSearch) item(ctx context.Context, categoryID, seriesID int64, season int) (int64, error) {
	if err := c.Reader.InCategory(ctx, categoryID, seriesID); err != nil {
		return 0, translateSeriesError(err)
	}
	items, err := c.Desired.AlbumItemsForWork(ctx, seriesID)
	if err != nil {
		return 0, err
	}
	for _, item := range items {
		if item.CategoryID == categoryID && item.SatisfactionKind == "SET" && item.MediaRef == tv.SeasonRef(season) {
			return item.ID, nil
		}
	}
	return 0, apiserver.ErrSeriesNotFound
}

func (c SeasonSearch) conns(ctx context.Context) ([]judge.Conn, error) {
	conns, err := c.Conns(ctx)
	if err != nil {
		return nil, fmt.Errorf("adapters: season search: indexers: %w", err)
	}
	if len(conns) == 0 {
		return nil, apiserver.ErrNoIndexersConfigured
	}
	return conns, nil
}

// Search runs the interactive search: decisions recorded, nothing sent.
func (c SeasonSearch) Search(ctx context.Context, categoryID, seriesID int64, season int) (apiserver.SearchItemResult, error) {
	itemID, err := c.item(ctx, categoryID, seriesID, season)
	if err != nil {
		return apiserver.SearchItemResult{}, err
	}
	conns, err := c.conns(ctx)
	if err != nil {
		return apiserver.SearchItemResult{}, err
	}
	result, err := c.Acquire.Search.SearchItem(ctx, itemID, conns, c.Clock.Now())
	if err != nil {
		return apiserver.SearchItemResult{}, seasonSearchError(err)
	}
	view := apiserver.SearchItemResult{TierUsed: result.TierUsed, Decisions: c.Views.decisionViews(ctx, result.Decisions)}
	for _, indexerError := range result.IndexerErrors {
		view.IndexerErrors = append(view.IndexerErrors, apiserver.IndexerSearchError{
			IndexerID: indexerError.IndexerID, IndexerName: indexerError.IndexerName, Error: indexerError.Error, TooSlow: indexerError.TooSlow,
		})
	}
	return view, nil
}

// Grab is the automatic search of the season: search, then send the best release.
func (c SeasonSearch) Grab(ctx context.Context, categoryID, seriesID int64, season int) (apiserver.GrabResult, error) {
	itemID, err := c.item(ctx, categoryID, seriesID, season)
	if err != nil {
		return apiserver.GrabResult{}, err
	}
	conns, err := c.conns(ctx)
	if err != nil {
		return apiserver.GrabResult{}, err
	}
	result, err := c.Acquire.SearchAndSend(ctx, itemID, conns, dispatch.GrabUser, c.Clock.Now())
	if err != nil {
		return apiserver.GrabResult{}, seasonSearchError(err)
	}
	view := apiserver.GrabResult{Found: len(result.Search.Decisions), AlreadyDownloading: result.AlreadyInFlight, Held: result.Held, Refused: result.Refused}
	if result.Sent != nil {
		sent := acquisitionView(*result.Sent)
		view.Sent = &sent
	}
	return view, nil
}

// Candidates answers the latest search's decisions for the season, best first.
func (c SeasonSearch) Candidates(ctx context.Context, categoryID, seriesID int64, season int) ([]apiserver.CandidateDecision, error) {
	itemID, err := c.item(ctx, categoryID, seriesID, season)
	if err != nil {
		return nil, err
	}
	details, err := c.Acquire.Search.Candidates.ListDecisionsForItem(ctx, itemID, candidateHistoryLimit)
	if err != nil {
		return nil, err
	}
	return c.Views.decisionViews(ctx, latestRun(details)), nil
}

// Send dispatches one accepted decision to a matching download client.
func (c SeasonSearch) Send(ctx context.Context, categoryID, seriesID int64, season int, decisionID int64) (apiserver.Acquisition, error) {
	itemID, err := c.item(ctx, categoryID, seriesID, season)
	if err != nil {
		return apiserver.Acquisition{}, err
	}
	acquisition, err := c.Acquire.Dispatch.Send(ctx, itemID, decisionID, c.Clock.Now())
	if err != nil {
		return apiserver.Acquisition{}, translateDispatchError(err)
	}
	return acquisitionView(acquisition), nil
}

func seasonSearchError(err error) error {
	if errors.Is(err, tv.ErrNotSearchable) {
		return apiserver.ErrWorkNotWanted
	}
	return translateDispatchError(err)
}
