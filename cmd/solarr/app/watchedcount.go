package app

import (
	"context"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// watchedDefaultDays is the cleanup list's default age; the Watched entry's count is the list at that default.
const watchedDefaultDays = 30

// recordWatchedCounts stores, per library that a syncing connection serves, how many works its Watched list holds.
func (p *process) recordWatchedCounts(ctx context.Context) error {
	servers, err := p.cache.MediaServers(ctx)
	if err != nil {
		return err
	}
	mapped, err := p.cache.AllMediaServerLibraries(ctx)
	if err != nil {
		return err
	}
	pairsOf := map[int64][]library.PlaybackPair{}
	for _, server := range servers {
		if !server.Enabled || !server.SyncPlayback {
			continue
		}
		users, err := p.cache.MediaServerUsers(ctx, server.ID)
		if err != nil {
			return err
		}
		served := map[int64]bool{}
		for _, one := range mapped[server.ID] {
			if served[one.CategoryID] {
				continue
			}
			served[one.CategoryID] = true
			for _, user := range users {
				if user.Counted {
					pairsOf[one.CategoryID] = append(pairsOf[one.CategoryID], library.PlaybackPair{ServerID: server.ID, ServerUserID: user.ServerUserID})
				}
			}
		}
	}
	now := clock.NewSystem().Now()
	snapshot := p.cache.Snapshot()
	store := library.NewPlayback(p.library)
	for categoryID, pairs := range pairsOf {
		category, found := snapshot.Category(categoryID)
		if !found {
			continue
		}
		var roots []int64
		for _, root := range snapshot.CategoryRoots() {
			if root.CategoryID == categoryID {
				roots = append(roots, root.RootFolderID)
			}
		}
		query := library.WatchedQuery{
			RootFolderIDs: roots, CategoryIDs: []int64{categoryID}, MediaTypes: []string{category.MediaType}, Pairs: pairs,
			LastPlayedBefore: now.Add(-watchedDefaultDays * 24 * time.Hour).Unix(),
		}
		if err := store.RecordWatchedCount(ctx, categoryID, query, now.Unix()); err != nil {
			return err
		}
	}
	return nil
}
