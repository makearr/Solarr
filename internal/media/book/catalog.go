package book

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/parsebook"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/works"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// Catalog is the book module's add and refresh: the shared works.Catalog over the format's providers.
type Catalog struct {
	Service          Service
	Categories       Categories
	DefaultProfileID int64
	// AllowAgain lifts the exclusion of a book a person removed and now adds on purpose; nil lifts nothing.
	AllowAgain func(ctx context.Context, provider, externalID string) error
}

var _ media.Adding = Catalog{}

func (c Catalog) shared() works.Catalog {
	return works.Catalog{Source: catalogSource{c.Service}, Categories: c.Categories, DefaultProfileID: c.DefaultProfileID, AllowAgain: c.AllowAgain}
}

// LookupWorks implements media.Adding.
func (c Catalog) LookupWorks(ctx context.Context, lookup media.AddLookup) ([]media.AddCandidate, error) {
	return c.shared().LookupWorks(ctx, lookup)
}

// AddWork implements media.Adding: the book with Readarr's Monitor choice.
func (c Catalog) AddWork(ctx context.Context, request media.AddRequest) (media.AddResult, error) {
	return c.shared().AddWork(ctx, request)
}

// catalogSource is works.Source for a book format.
type catalogSource struct{ service Service }

// Lookup implements works.Source: the sources' books for a title, "Title by Author" or "Author - Title" narrowing it
// to a writer.
func (s catalogSource) Lookup(ctx context.Context, lookup media.AddLookup) ([]media.AddCandidate, error) {
	query := strings.TrimSpace(lookup.Query)
	title, author := query, ""
	if info, ok := parsebook.Parse(query); ok && info.Author != "" && (strings.Contains(query, " by ") || strings.Contains(query, " - ")) {
		title, author = info.Title, info.Author
	}
	found, err := s.service.Search(ctx, lookup.CategoryID, title, author, lookup.Limit)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", media.ErrSourceUnavailable, err)
	}
	format := s.service.Format
	candidates := make([]media.AddCandidate, 0, len(found))
	for _, book := range found {
		provider, externalID := format.CandidateID(book.IDs)
		if externalID == "" {
			continue
		}
		candidates = append(candidates, media.AddCandidate{
			Kind: format.MediaType, Provider: provider, ExternalID: externalID, Title: book.Title, Subtitle: book.Author,
			Overview: book.Overview, PosterURL: book.Cover, Year: int64(book.Year), Traits: book.Traits,
		})
	}
	return candidates, nil
}

// Held implements works.Source.
func (s catalogSource) Held(ctx context.Context, provider, externalID string) (int64, bool) {
	ids, ok := s.service.Format.NamedIDs(provider, externalID)
	if !ok {
		return 0, false
	}
	return s.service.WorkByIDs(ctx, ids)
}

// Monitor implements works.Source: Readarr's three choices.
func (catalogSource) Monitor() (string, string, func(string) bool) {
	return string(MonitorBook), string(MonitorNone), func(option string) bool { return MonitorOption(option).Valid() }
}

// Add implements works.Source.
func (s catalogSource) Add(ctx context.Context, request works.Add) (media.AddResult, error) {
	format := s.service.Format
	ids, ok := format.NamedIDs(request.Provider, request.ExternalID)
	if !ok {
		return media.AddResult{}, fmt.Errorf("%w: %s %q is not a book id", media.ErrInvalidAdd, request.Provider, request.ExternalID)
	}
	var fallback *Book
	if request.Fallback != nil {
		known := ids
		for provider, id := range request.Fallback.ExternalIDs {
			if more, ok := format.NamedIDs(provider, id); ok {
				known = Merge(Book{IDs: known}, Book{IDs: more}).IDs
			}
		}
		fallback = &Book{IDs: known, Title: request.Fallback.Title, Year: request.Fallback.Year}
	}
	result, err := s.service.Add(ctx, AddRequest{
		IDs: ids, CategoryID: request.CategoryID, ProfileID: request.ProfileID, Monitor: MonitorOption(request.Option),
		AddedBy: request.AddedBy, Fallback: fallback,
	})
	if errors.Is(err, ErrNoSource) {
		return media.AddResult{}, fmt.Errorf("%w: %w", media.ErrSourceUnavailable, err)
	}
	if err != nil {
		return media.AddResult{}, err
	}
	return media.AddResult{Kind: format.MediaType, WorkID: result.WorkID, Created: result.Created}, nil
}

// Refresh re-reads one book from the sources; monitoring and the person's own fields stay as they are.
func (c Catalog) Refresh(ctx context.Context, categoryID, workID int64) error {
	_, err := c.Service.Refresh(ctx, categoryID, workID)
	if errors.Is(err, ErrNoSource) {
		return fmt.Errorf("%w: %w", media.ErrSourceUnavailable, err)
	}
	return err
}

// NewFiles is the format's media.PlacingFiles, the shared one with the book a person names; an audiobook's queued
// chapters go with the one named.
func NewFiles(resolver Resolver, unidentified library.Unidentified, clock clock.Clock, allowAgain func(ctx context.Context, provider, externalID string) error) works.Files {
	format := resolver.Service.Format
	return works.Files{
		Series: resolver.Service.Series, Unidentified: unidentified, Clock: clock, AllowAgain: allowAgain, WholeFolder: format.ByFolder,
		Resolve: func(ctx context.Context, categoryID int64, placement media.Placement) (int64, error) {
			ids, ok := format.NamedIDs(placement.Provider, placement.ExternalID)
			if !ok {
				return 0, fmt.Errorf("%w: %s %q is not a book id", media.ErrInvalidAdd, placement.Provider, placement.ExternalID)
			}
			return resolver.ByIDs(ctx, categoryID, ids), nil
		},
	}
}
