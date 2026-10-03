package adapters

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media/tv"
)

// UnidentifiedTV satisfies apiserver.TVUnidentifiedResolver over tv.Identifier.
type UnidentifiedTV struct {
	Identifier   tv.Identifier
	Unidentified library.Unidentified
	Cache        *settings.Cache
	// Exclusions lifts a series' exclusion when a person picks it by hand.
	Exclusions interface {
		RemoveExclusion(ctx context.Context, provider, externalID string) error
	}
}

// ResolveTV places one queued file, and with wholeFolder every other queued file directly beside it by its own name.
func (a UnidentifiedTV) ResolveTV(
	ctx context.Context, mediaObjectID int64, provider, externalID string, season int, episodes []int, wholeFolder bool,
) (int64, int64, error) {
	id, err := strconv.ParseInt(externalID, 10, 64)
	if err != nil || id <= 0 {
		return 0, 0, fmt.Errorf("%w: %q is not a series id", apiserver.ErrInvalidChoice, externalID)
	}
	ids := tv.IDs{TVDB: id}
	if provider == tv.ProviderTMDB {
		ids = tv.IDs{TMDB: id}
	}
	entry, err := a.Unidentified.Get(ctx, mediaObjectID)
	if err != nil {
		return 0, 0, err
	}
	categoryID, ok := a.tvCategory(entry.RootFolderID)
	if !ok {
		return 0, 0, fmt.Errorf("%w: the file is not in a TV library", apiserver.ErrInvalidChoice)
	}
	if err := a.Exclusions.RemoveExclusion(ctx, provider, externalID); err != nil {
		return 0, 0, err
	}
	workID, err := a.Identifier.Resolve(ctx, categoryID, entry, ids, season, episodes)
	if err != nil {
		return 0, 0, translateTVResolveError(err)
	}
	resolved := int64(1)
	if wholeFolder {
		beside, err := a.Unidentified.InFolder(ctx, entry.RootFolderID, entry.RelPath)
		if err != nil {
			return workID, resolved, err
		}
		for _, other := range beside {
			if other == mediaObjectID {
				continue
			}
			row, err := a.Unidentified.Get(ctx, other)
			if err != nil {
				return workID, resolved, err
			}
			// A name that says no episode is left in the queue for its own decision.
			if _, err := a.Identifier.Resolve(ctx, categoryID, row, ids, 0, nil); err == nil {
				resolved++
			} else if !errors.Is(err, tv.ErrNoEpisodes) && !errors.Is(err, tv.ErrEpisodeUnknown) {
				return workID, resolved, err
			}
		}
	}
	return workID, resolved, nil
}

func (a UnidentifiedTV) tvCategory(rootFolderID int64) (int64, bool) {
	snapshot := a.Cache.Snapshot()
	for _, id := range snapshot.CategoriesForRootFolder(rootFolderID) {
		if category, found := snapshot.Category(id); found && category.MediaType == tv.MediaType {
			return id, true
		}
	}
	return 0, false
}

func translateTVResolveError(err error) error {
	if errors.Is(err, tv.ErrNoEpisodes) || errors.Is(err, tv.ErrEpisodeUnknown) {
		return fmt.Errorf("%w: %w", apiserver.ErrInvalidChoice, err)
	}
	return err
}
