package music

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/moving/mover"
	"github.com/makearr/Solarr/internal/moving/placement"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

// Housekeeping is a library's File Management at disk scan (Sonarr's): unmonitor what went
// missing, delete empty folders, create missing artist folders (documentation/file-management.md).
type Housekeeping struct {
	fsys          fsx.FS
	categories    Categories
	roots         PlacementRoots
	affinity      PlacementAffinity
	desired       *library.Desired
	subscriptions library.Subscriptions
	people        library.People
	groupID       GroupID
}

// HousekeepingConfig is what Housekeeping needs; GroupID is optional.
type HousekeepingConfig struct {
	FS            fsx.FS
	Categories    Categories
	Roots         PlacementRoots
	Affinity      PlacementAffinity
	Desired       *library.Desired
	Subscriptions library.Subscriptions
	People        library.People
	GroupID       GroupID
}

// NewHousekeeping builds one.
func NewHousekeeping(config HousekeepingConfig) Housekeeping {
	return Housekeeping{
		fsys: config.FS, categories: config.Categories, roots: config.Roots, affinity: config.Affinity,
		desired: config.Desired, subscriptions: config.Subscriptions, people: config.People, groupID: config.GroupID,
	}
}

// AfterSweep applies the library's settings to the root just swept; every step runs even when
// an earlier one failed.
func (h Housekeeping) AfterSweep(
	ctx context.Context, root settings.RootFolder, categoryID int64, outcome scan.SweepOutcome,
) error {
	category, found := h.categories.Category(categoryID)
	if !found {
		return nil
	}
	fileManagement := category.FileManagement
	var failures []error
	if fileManagement.UnmonitorDeleted && !outcome.MissingSince.IsZero() {
		if _, err := h.desired.UnmonitorItemsLosingFiles(ctx, categoryID, root.ID, outcome.MissingSince.Unix()); err != nil {
			failures = append(failures, err)
		}
	}
	if fileManagement.MergeArtistAliases {
		if err := h.mergeArtists(ctx, categoryID); err != nil {
			failures = append(failures, err)
		}
	}
	if fileManagement.DeleteEmptyFolders {
		h.deleteEmptyFolders(root.Path, outcome.EmptyDirectories, !fileManagement.CreateEmptyArtistFolders)
	}
	if fileManagement.CreateEmptyArtistFolders {
		if err := h.createArtistFolders(ctx, root, category); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// mergeArtists folds the library's artists stored under several names into one (M1g 1.2).
func (h Housekeeping) mergeArtists(ctx context.Context, categoryID int64) error {
	merges, err := h.people.PlanArtistMerges(ctx, categoryID)
	if err != nil || len(merges) == 0 {
		return err
	}
	return h.people.MergeArtists(ctx, merges)
}

// deleteEmptyFolders removes each empty folder and the parents it leaves empty. An artist folder
// (directly below the root) goes only when artistFolders allows it, as Sonarr keeps a series
// folder while it may create one.
func (h Housekeeping) deleteEmptyFolders(rootPath string, empty []string, artistFolders bool) {
	for _, relative := range empty {
		for folder := relative; folder != "." && folder != ""; folder = path.Dir(folder) {
			if !artistFolders && !strings.Contains(folder, "/") {
				break
			}
			if err := h.fsys.Remove(path.Join(rootPath, folder)); err != nil {
				break
			}
		}
	}
}

// createArtistFolders creates the folder of each artist monitored in this library that has none
// on any of its roots, on the root an import would choose, so each is created exactly once.
func (h Housekeeping) createArtistFolders(ctx context.Context, root settings.RootFolder, category settings.Category) error {
	subscriptions, err := h.subscriptions.ListEnabled(ctx)
	if err != nil {
		return err
	}
	names := NamingFor(category.Naming)
	candidates := rootCandidates(h.roots, category.ID)
	var failures []error
	for _, subscription := range subscriptions {
		if subscription.SubjectType != "person" || subscription.CategoryID != category.ID {
			continue
		}
		person, err := h.people.Person(ctx, subscription.SubjectID)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		folder, err := names.RenderArtistFolder(person.Name)
		if err != nil || folder == "" || h.folderExists(candidates, folder) {
			continue
		}
		chosen, err := h.chooseRoot(ctx, category.ID, person.Name, candidates)
		if err != nil || chosen != root.ID {
			continue
		}
		if err := h.createFolder(root.Path, folder, category.FileManagement); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (h Housekeeping) folderExists(candidates []placement.RootCandidate, folder string) bool {
	for _, candidate := range candidates {
		if root, ok := h.roots.RootFolder(candidate.RootFolderID); ok {
			if _, err := h.fsys.Stat(path.Join(root.Path, folder)); err == nil {
				return true
			}
		}
	}
	return false
}

// chooseRoot is the root an import of this artist would be placed on: affinity, then strategy.
func (h Housekeeping) chooseRoot(
	ctx context.Context, categoryID int64, artist string, candidates []placement.RootCandidate,
) (int64, error) {
	key := artistAffinityKey(artist)
	var existing *int64
	if rootFolderID, found, err := h.affinity.PlacementAffinity(ctx, categoryID, key); err != nil {
		return 0, err
	} else if found {
		existing = &rootFolderID
	}
	chosen, decision, err := placement.Resolve(placement.Request{
		CategoryID: categoryID, Strategy: placement.StrategyMostFreeSpace,
		AffinityEnabled: true, AffinityKey: key, ExistingAffinityRoot: existing,
	}, candidates)
	if err != nil || decision.Outcome != placement.OutcomePlaced {
		return 0, err
	}
	return chosen.RootFolderID, nil
}

func (h Housekeeping) createFolder(rootPath, folder string, fileManagement settings.FileManagement) error {
	absolute := path.Join(rootPath, folder)
	if err := h.fsys.MkdirAll(absolute, 0o755); err != nil {
		return fmt.Errorf("music: create artist folder %q: %w", absolute, err)
	}
	if !fileManagement.SetPermissions {
		return nil
	}
	permissions, err := PermissionsFor(fileManagement, h.groupID)
	if err != nil {
		return err
	}
	// The folder stands in for the file: ApplyPermissions sets it and nothing above it.
	return mover.ApplyPermissions(h.fsys, rootPath, absolute, mover.Permissions{
		FolderMode: permissions.FolderMode, FileMode: permissions.FolderMode, GID: permissions.GID,
	})
}
