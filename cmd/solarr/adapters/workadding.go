package adapters

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// WorkAdding satisfies apiserver.WorkAdder through the library's type's media.Adding, with the kinds it declares.
type WorkAdding struct {
	Categories Categories
	Adding     func(mediaType string) (media.Adding, bool)
	// AddKinds answers a media type's declared add kinds, the first being its default.
	AddKinds func(mediaType string) []media.Choice
	// AfterAdd runs once something was added, so the library's counts follow at once; nil does nothing.
	AfterAdd func(ctx context.Context)
	// Suggest names the library a candidate belongs in by the libraries' rules; nil suggests none.
	Suggest func(ctx context.Context, mediaType string, traits media.Traits) (apiserver.LibrarySuggestion, bool, error)
	// Forget drops a recommendation of the added item from every pool; nil does nothing.
	Forget func(ctx context.Context, provider, externalID string) error
	// Placement records the root an add chose; nil ignores the choice.
	Placement *AddPlacement
}

// AddPlacement turns the root an add dialog chose into the work's placement affinity, so its first file lands there.
type AddPlacement struct {
	// Titled reads a stored work's title and year.
	Titled   func(ctx context.Context, mediaType string, categoryID, workID int64) (title string, year int64, err error)
	Affinity interface {
		SetPlacementAffinity(ctx context.Context, categoryID int64, affinityKey string, rootFolderID, updatedAt int64) error
	}
	Clock clock.Clock
	// Keys answers a media type's affinity key for a stored work's title and year; a type missing here ignores the choice.
	Keys map[string]func(title string, year int64) string
}

func (p AddPlacement) record(ctx context.Context, mediaType string, categoryID, workID, rootFolderID int64) error {
	keyOf, ok := p.Keys[mediaType]
	if !ok {
		return nil
	}
	title, year, err := p.Titled(ctx, mediaType, categoryID, workID)
	if err != nil {
		return fmt.Errorf("read added work: %w", err)
	}
	key := keyOf(title, year)
	if key == "" {
		return nil
	}
	return p.Affinity.SetPlacementAffinity(ctx, categoryID, key, rootFolderID, p.Clock.Now().Unix())
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
		return nil, lookupFailure(err)
	}
	mediaType, _ := a.Categories.MediaTypeOf(categoryID)
	rows := make([]apiserver.AddCandidateRow, len(found))
	for i, candidate := range found {
		rows[i] = apiserver.AddCandidateRow{
			Kind: candidate.Kind, Provider: candidate.Provider, ExternalID: candidate.ExternalID, Title: candidate.Title,
			Subtitle: candidate.Subtitle, Overview: candidate.Overview, PosterURL: candidate.PosterURL, Status: candidate.Status,
			Year: candidate.Year, LibraryID: candidate.LibraryID,
		}
		if a.Suggest == nil {
			continue
		}
		suggestion, found, err := a.Suggest(ctx, mediaType, candidate.Traits)
		if err != nil {
			return nil, err
		}
		if found {
			rows[i].Suggestion = &suggestion
		}
	}
	return rows, nil
}

// Describe asks the library's type to describe one lookup result; a type that cannot is not wired for it.
func (a WorkAdding) Describe(ctx context.Context, categoryID int64, kind, provider, externalID string) (apiserver.WorkPreviewRow, error) {
	adding, kind, err := a.adder(categoryID, kind)
	if err != nil {
		return apiserver.WorkPreviewRow{}, err
	}
	describing, ok := adding.(media.Describing)
	if !ok {
		return apiserver.WorkPreviewRow{}, apiserver.ErrDiscoverUnsupportedMediaType
	}
	preview, err := describing.DescribeWork(ctx, media.DescribeRequest{CategoryID: categoryID, Kind: kind, Provider: provider, ExternalID: externalID})
	if err != nil {
		return apiserver.WorkPreviewRow{}, notWired(err)
	}
	row := apiserver.WorkPreviewRow{
		Title: preview.Title, Subtitle: preview.Subtitle, Tagline: preview.Tagline, Overview: preview.Overview, PosterURL: preview.PosterURL,
		Status: preview.Status, Year: preview.Year, Genres: preview.Genres,
	}
	for _, fact := range preview.Facts {
		row.Facts = append(row.Facts, apiserver.PreviewFactRow{Key: fact.Key, Value: fact.Value})
	}
	for _, rating := range preview.Ratings {
		row.Ratings = append(row.Ratings, apiserver.PreviewRatingRow{Provider: rating.Provider, Score: rating.Score, Votes: rating.Votes})
	}
	for _, person := range preview.People {
		row.People = append(row.People, apiserver.PreviewPersonRow{Name: person.Name, Role: person.Role, Character: person.Character, ImageURL: person.ImageURL})
	}
	for _, group := range preview.Groups {
		out := apiserver.PreviewGroupRow{Number: group.Number, Title: group.Title}
		for _, part := range group.Parts {
			out.Parts = append(out.Parts, apiserver.PreviewPartRow{Number: part.Number, Title: part.Title, Date: part.Date, DurationSec: part.DurationSec})
		}
		row.Groups = append(row.Groups, out)
	}
	return row, nil
}

// Add adds one candidate through the library's type.
func (a WorkAdding) Add(ctx context.Context, request apiserver.AddWorkRequest) (apiserver.AddWorkResult, error) {
	adding, kind, err := a.adder(request.CategoryID, request.Kind)
	if err != nil {
		return apiserver.AddWorkResult{}, err
	}
	result, err := adding.AddWork(ctx, media.AddRequest{
		CategoryID: request.CategoryID, UserID: request.UserID, Kind: kind, Provider: request.Provider, ExternalID: request.ExternalID,
		Monitored: request.Monitored, Option: request.MonitorOption, Specials: request.Specials, ProfileID: request.ProfileID, Search: request.Search, RootFolderID: request.RootFolderID,
	})
	if err != nil {
		return apiserver.AddWorkResult{}, notWired(err)
	}
	if mediaType, found := a.Categories.MediaTypeOf(request.CategoryID); found && a.Placement != nil && request.RootFolderID != nil && result.WorkID != 0 {
		if err := a.Placement.record(ctx, mediaType, request.CategoryID, result.WorkID, *request.RootFolderID); err != nil {
			return apiserver.AddWorkResult{}, err
		}
	}
	if a.Forget != nil {
		if err := a.Forget(ctx, request.Provider, request.ExternalID); err != nil {
			return apiserver.AddWorkResult{}, err
		}
	}
	if a.AfterAdd != nil {
		a.AfterAdd(ctx)
	}
	return apiserver.AddWorkResult{Kind: result.Kind, WorkID: result.WorkID, PersonID: result.PersonID, Searching: result.Searching}, nil
}

// lookupFailure is notWired for a lookup, where an error no sentinel names means the provider did not answer; it names the provider.
func lookupFailure(err error) error {
	if !errors.Is(err, media.ErrNotWired) && !errors.Is(err, media.ErrUnknownCandidate) && !errors.Is(err, media.ErrInvalidAdd) &&
		!errors.Is(err, media.ErrQueryTooShort) && !errors.Is(err, media.ErrSourceUnavailable) {
		return fmt.Errorf("%w: %w", apiserver.ErrSeriesSourceUnavailable, err)
	}
	return notWired(err)
}

func notWired(err error) error {
	switch {
	case errors.Is(err, media.ErrNotWired):
		return apiserver.ErrDiscoverUnsupportedMediaType
	case errors.Is(err, media.ErrSourceUnavailable):
		return fmt.Errorf("%w: %w", apiserver.ErrSeriesSourceUnavailable, err)
	case errors.Is(err, media.ErrUnknownCandidate):
		return fmt.Errorf("%w: %w", apiserver.ErrCandidateNotFound, err)
	case errors.Is(err, media.ErrInvalidAdd):
		return fmt.Errorf("%w: %w", apiserver.ErrInvalidChoice, err)
	case errors.Is(err, media.ErrQueryTooShort):
		return fmt.Errorf("%w: %w", apiserver.ErrQueryTooShort, err)
	}
	return err
}
