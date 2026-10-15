package adapters

import (
	"context"
	"errors"
	"fmt"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/library"
)

// Crossovers implements apiserver.Crossovers over library.Crossovers.
type Crossovers struct {
	Crossovers library.Crossovers
	Desired    library.Desired
	// SharedFiles asks for the pass placing a linked broadcast's file into both series; nil never asks.
	SharedFiles func(ctx context.Context) error
}

// suggestionsShown bounds the likely one-broadcast pairs a library lists at once.
const suggestionsShown = 50

func crossoverError(err error) error {
	if errors.Is(err, library.ErrNotACrossover) {
		return fmt.Errorf("%w: %w", apiserver.ErrNoCrossover, err)
	}
	return err
}

func crossoverView(event library.Crossover) gen.Crossover {
	view := gen.Crossover{Id: event.ID, Name: event.Name, Monitored: event.Monitored}
	view.Members = make([]gen.CrossoverMember, len(event.Members))
	for i, member := range event.Members {
		view.Members[i].PartId, view.Members[i].WorkId, view.Members[i].WorkTitle = member.PartID, member.WorkID, member.WorkTitle
		view.Members[i].MediaRef, view.Members[i].Title, view.Members[i].Monitored = member.MediaRef, member.PartTitle, member.Monitored
		view.Members[i].HasFile = member.HasFile
		if member.Date != "" {
			date := member.Date
			view.Members[i].Date = &date
		}
	}
	return view
}

func crossoverViews(events []library.Crossover) []gen.Crossover {
	views := make([]gen.Crossover, len(events))
	for i, event := range events {
		views[i] = crossoverView(event)
	}
	return views
}

// List implements apiserver.Crossovers.
func (a Crossovers) List(ctx context.Context, mediaType string, categoryID int64) ([]gen.Crossover, []gen.SameContentSuggestion, error) {
	events, err := a.Crossovers.List(ctx, mediaType)
	if err != nil {
		return nil, nil, err
	}
	suggestions, err := a.Crossovers.Suggestions(ctx, mediaType, categoryID, suggestionsShown)
	if err != nil {
		return nil, nil, err
	}
	views := make([]gen.SameContentSuggestion, len(suggestions))
	for i, s := range suggestions {
		views[i] = gen.SameContentSuggestion{
			PartId: s.PartID, WorkId: s.WorkID, WorkTitle: s.WorkTitle, MediaRef: s.MediaRef, Title: s.Title, Date: s.Date,
			OtherPartId: s.OtherPartID, OtherWorkId: s.OtherWorkID, OtherWorkTitle: s.OtherWorkTitle, OtherRef: s.OtherRef,
		}
	}
	return crossoverViews(events), views, nil
}

// Create implements apiserver.Crossovers.
func (a Crossovers) Create(ctx context.Context, mediaType, name string, partIDs []int64) (gen.Crossover, error) {
	id, err := a.Crossovers.Create(ctx, mediaType, name, partIDs)
	if err != nil {
		return gen.Crossover{}, err
	}
	return a.Get(ctx, id)
}

// Get implements apiserver.Crossovers.
func (a Crossovers) Get(ctx context.Context, id int64) (gen.Crossover, error) {
	event, err := a.Crossovers.Get(ctx, id)
	if err != nil {
		return gen.Crossover{}, crossoverError(err)
	}
	return crossoverView(event), nil
}

// Update implements apiserver.Crossovers.
func (a Crossovers) Update(ctx context.Context, id int64, name string, partIDs []int64) (gen.Crossover, error) {
	if err := a.Crossovers.Update(ctx, id, name, partIDs); err != nil {
		return gen.Crossover{}, crossoverError(err)
	}
	return a.Get(ctx, id)
}

// Delete implements apiserver.Crossovers.
func (a Crossovers) Delete(ctx context.Context, id int64) error {
	return crossoverError(a.Crossovers.Delete(ctx, id))
}

// Monitor implements apiserver.Crossovers.
func (a Crossovers) Monitor(ctx context.Context, id int64, monitored bool) (gen.Crossover, error) {
	if err := a.Crossovers.SetMonitored(ctx, a.Desired, id, monitored); err != nil {
		return gen.Crossover{}, crossoverError(err)
	}
	return a.Get(ctx, id)
}

// ForWork implements apiserver.Crossovers.
func (a Crossovers) ForWork(ctx context.Context, workID int64) ([]gen.Crossover, []gen.SameContentLink, error) {
	events, err := a.Crossovers.ForWork(ctx, workID)
	if err != nil {
		return nil, nil, err
	}
	links, err := a.Crossovers.SameContentOfWork(ctx, workID)
	if err != nil {
		return nil, nil, err
	}
	views := make([]gen.SameContentLink, len(links))
	for i, link := range links {
		views[i] = gen.SameContentLink{
			PartId: link.PartID, OtherPartId: link.OtherPartID, OtherWorkId: link.OtherWorkID, OtherRef: link.OtherRef, OtherWorkTitle: link.OtherWorkTitle,
		}
	}
	return crossoverViews(events), views, nil
}

// Link implements apiserver.Crossovers.
func (a Crossovers) Link(ctx context.Context, partID, otherPartID int64) error {
	if err := a.Crossovers.LinkSameContent(ctx, partID, otherPartID); err != nil {
		return err
	}
	if a.SharedFiles == nil {
		return nil
	}
	return a.SharedFiles(ctx)
}

// Unlink implements apiserver.Crossovers.
func (a Crossovers) Unlink(ctx context.Context, partID, otherPartID int64) error {
	return a.Crossovers.UnlinkSameContent(ctx, partID, otherPartID)
}
