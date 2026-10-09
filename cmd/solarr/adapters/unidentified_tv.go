package adapters

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/tv"
)

// UnidentifiedTV is TV's media.PlacingFiles over tv.Identifier.
type UnidentifiedTV struct {
	Identifier   tv.Identifier
	Unidentified library.Unidentified
	// Exclusions lifts a series' exclusion when a person picks it by hand.
	Exclusions WorkExclusions
}

// PlaceFile places one queued file in the series, season and episodes named, and with wholeFolder every other queued
// file directly beside it by its own name.
func (a UnidentifiedTV) PlaceFile(
	ctx context.Context, categoryID, mediaObjectID int64, placement media.Placement, wholeFolder bool,
) (int64, int64, error) {
	provider, externalID, season, episodes := placement.Provider, placement.ExternalID, placement.Group, placement.Parts
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
	if err := a.Exclusions.Allow(ctx, tv.MediaType, provider, externalID); err != nil {
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

func translateTVResolveError(err error) error {
	if errors.Is(err, tv.ErrNoEpisodes) || errors.Is(err, tv.ErrEpisodeUnknown) {
		return fmt.Errorf("%w: %w", apiserver.ErrInvalidChoice, err)
	}
	return err
}
