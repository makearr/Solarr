// Package adapters bridges each internal interface to its implementation, so neither package imports the other.
package adapters

import (
	"context"

	"github.com/makearr/MediaNexus/internal/db/library"
	"github.com/makearr/MediaNexus/internal/db/settings"
	"github.com/makearr/MediaNexus/internal/moving/moverjob"
)

// Roots adapts the ADR-25 settings snapshot to scan.Roots: it asks the cache
// fresh on every lookup rather than holding a copy that could go stale underneath it.
type Roots struct{ Cache *settings.Cache }

func (c Roots) RootFolder(id int64) (settings.RootFolder, bool) {
	return c.Cache.Snapshot().RootFolder(id)
}

// Categories adapts the settings snapshot to music.Categories, the same
// "ask fresh, never cache" shape as Roots.
type Categories struct{ Cache *settings.Cache }

func (c Categories) Category(id int64) (settings.Category, bool) {
	return c.Cache.Snapshot().Category(id)
}

// CategoryForMediaType implements music.CategoryFinder: the first category
// configured for a media type, for a caller with no root folder to inherit
// one from the way a scan does (subscription.expand's own need).
func (c Categories) CategoryForMediaType(mediaType string) (settings.Category, bool) {
	for _, category := range c.Cache.Snapshot().Categories() {
		if category.MediaType == mediaType {
			return category, true
		}
	}
	return settings.Category{}, false
}

// ImportPolicy adapts the settings snapshot to importjob.Policy, the same
// "ask fresh, never cache" shape as Roots/Categories.
type ImportPolicy struct{ Cache *settings.Cache }

func (c ImportPolicy) ImportAutoCommitConfidence() float64 {
	return c.Cache.Snapshot().ImportAutoCommitConfidence()
}

// RefusedByExclusion implements importjob.Policy's own ADR-53 gate (todo
// 4.15) over settings.MatchExclusion -- an ignore-action match is never
// reported found, since only a refuse action poisons a unit.
func (c ImportPolicy) RefusedByExclusion(name, mediaType string, categoryID *int64) (string, bool) {
	action, matched, ok := settings.MatchExclusion(c.Cache.Snapshot().ExclusionPatterns(), name, mediaType, categoryID)
	if !ok || action != settings.ExclusionRefuse {
		return "", false
	}
	return matched.Pattern, true
}

// MoverRoots adapts Roots to moverjob.RootFolders -- same settings snapshot,
// just the path and filesystem id Apply needs rather than scan's own policy fields.
type MoverRoots struct{ Roots Roots }

func (m MoverRoots) RootFolder(id int64) (moverjob.RootFolder, bool) {
	root, ok := m.Roots.RootFolder(id)
	if !ok {
		return moverjob.RootFolder{}, false
	}
	return moverjob.RootFolder{Path: root.Path, FSID: root.FSID}, true
}

// DrainRoots adapts *settings.Cache to drainjob.Roots -- internal/moving/drainjob
// takes settings.RootFolder/CategoryRoot for their types only (the same
// lightweight use internal/scanning/scan's own Roots interface already makes), so
// this is a thin method-shape bridge, not a translation of any real logic.
type DrainRoots struct{ Cache *settings.Cache }

func (a DrainRoots) RootFolder(id int64) (settings.RootFolder, bool) {
	return a.Cache.Snapshot().RootFolder(id)
}

func (a DrainRoots) CategoryRoots() []settings.CategoryRoot {
	return a.Cache.Snapshot().CategoryRoots()
}

func (a DrainRoots) SetAcceptsNewForRootFolder(ctx context.Context, rootFolderID int64, acceptsNew bool) error {
	return a.Cache.SetAcceptsNewForRootFolder(ctx, rootFolderID, acceptsNew)
}

func (a DrainRoots) SetRootFolderDrainState(ctx context.Context, id int64, state string, drainJobID *int64) error {
	return a.Cache.SetRootFolderDrainState(ctx, id, state, drainJobID)
}

// ScheduleSource adapts settings.Cache to jobs.ScheduleSource -- the
// snapshot is re-read on every call, so a cadence or enabled-flag change
// takes effect on the task's own next tick, no restart needed.
type ScheduleSource struct{ Cache *settings.Cache }

func (c ScheduleSource) Cadence(taskKey, defaultCadence string) string {
	return c.Cache.Snapshot().ScheduleCadence(taskKey, defaultCadence)
}

func (c ScheduleSource) Enabled(taskKey string) bool {
	return c.Cache.Snapshot().ScheduleEnabled(taskKey)
}

// RootFolderPath adapts settings.Cache to library.RootFolderPath --
// library.RemoveAndBlock never reaches into settings.db itself (ADR-25's own
// database split).
func RootFolderPath(cache *settings.Cache) library.RootFolderPath {
	return func(rootFolderID int64) (string, bool) {
		folder, found := cache.Snapshot().RootFolder(rootFolderID)
		if !found {
			return "", false
		}
		return folder.Path, true
	}
}
