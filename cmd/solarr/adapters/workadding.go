package adapters

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/media"
)

// WorkAdding satisfies apiserver.WorkAdder through the library's type's media.Adding, with the kinds it declares.
type WorkAdding struct {
	Categories Categories
	Adding     func(mediaType string) (media.Adding, bool)
	// AddKinds answers a media type's declared add kinds, the first being its default.
	AddKinds func(mediaType string) []media.Choice
	// AfterAdd runs once something was added, so the library's counts follow at once; nil does nothing.
	AfterAdd func(ctx context.Context)
}

// adder resolves a library's type's add and the kind asked for, the type's first when none is.
func (a WorkAdding) adder(categoryID int64, kind string) (media.Adding, string, error) {
	mediaType, ok := a.Categories.MediaTypeOf(categoryID)
	if !ok {
		return nil, "", apiserver.ErrDiscoverUnsupportedMediaType
	}
	adding, ok := a.Adding(mediaType)
	kinds := a.AddKinds(mediaType)
	if !ok || len(kinds) == 0 {
		return nil, "", apiserver.ErrDiscoverUnsupportedMediaType
	}
	if kind == "" {
		return adding, kinds[0].Key, nil
	}
	if !slices.ContainsFunc(kinds, func(choice media.Choice) bool { return choice.Key == kind }) {
		return nil, "", fmt.Errorf("%w: a %s library adds no %s", apiserver.ErrInvalidChoice, mediaType, kind)
	}
	return adding, kind, nil
}

// Lookup searches the library's type's sources.
func (a WorkAdding) Lookup(ctx context.Context, categoryID int64, kind, query string, limit int) ([]apiserver.AddCandidateRow, error) {
	adding, kind, err := a.adder(categoryID, kind)
	if err != nil {
		return nil, err
	}
	found, err := adding.LookupWorks(ctx, media.AddLookup{CategoryID: categoryID, Kind: kind, Query: query, Limit: limit})
	if err != nil {
		return nil, notWired(err)
	}
	rows := make([]apiserver.AddCandidateRow, len(found))
	for i, candidate := range found {
		rows[i] = apiserver.AddCandidateRow{
			Kind: candidate.Kind, Provider: candidate.Provider, ExternalID: candidate.ExternalID, Title: candidate.Title,
			Subtitle: candidate.Subtitle, Overview: candidate.Overview, PosterURL: candidate.PosterURL, Status: candidate.Status,
			Year: candidate.Year, LibraryID: candidate.LibraryID,
		}
	}
	return rows, nil
}

// Add adds one candidate through the library's type.
func (a WorkAdding) Add(ctx context.Context, request apiserver.AddWorkRequest) (apiserver.AddWorkResult, error) {
	adding, kind, err := a.adder(request.CategoryID, request.Kind)
	if err != nil {
		return apiserver.AddWorkResult{}, err
	}
	result, err := adding.AddWork(ctx, media.AddRequest{
		CategoryID: request.CategoryID, UserID: request.UserID, Kind: kind, Provider: request.Provider, ExternalID: request.ExternalID,
		Monitored: request.Monitored, Option: request.MonitorOption, Specials: request.Specials, ProfileID: request.ProfileID, Search: request.Search,
	})
	if err != nil {
		return apiserver.AddWorkResult{}, notWired(err)
	}
	if a.AfterAdd != nil {
		a.AfterAdd(ctx)
	}
	return apiserver.AddWorkResult{Kind: result.Kind, WorkID: result.WorkID, PersonID: result.PersonID, Searching: result.Searching}, nil
}

func notWired(err error) error {
	switch {
	case errors.Is(err, media.ErrNotWired):
		return apiserver.ErrDiscoverUnsupportedMediaType
	case errors.Is(err, media.ErrSourceUnavailable):
		return fmt.Errorf("%w: %w", apiserver.ErrSeriesSourceUnavailable, err)
	case errors.Is(err, media.ErrInvalidAdd):
		return fmt.Errorf("%w: %w", apiserver.ErrInvalidChoice, err)
	case errors.Is(err, media.ErrQueryTooShort):
		return fmt.Errorf("%w: %w", apiserver.ErrQueryTooShort, err)
	}
	return err
}
