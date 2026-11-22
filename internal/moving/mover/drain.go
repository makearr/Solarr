package mover

import (
	"fmt"
	"sort"
	"strings"

	"github.com/makearr/Solarr/internal/moving/placement"
)

// FileOnRoot is one file currently on the root being drained, exactly as
// the caller already read it from library.db -- Plan's own FileToMove
// cannot be used here, since drain has not chosen a destination yet.
type FileOnRoot struct {
	MediaObjectID int64
	RelPath       string
	SizeBytes     int64
}

// DrainRequest is what PlanDrain needs.
type DrainRequest struct {
	Source            placement.RootCandidate
	SourceReadOnly    bool
	SourceAllowRename bool
	Files             []FileOnRoot
	// Destinations are the other roots sharing a category with Source; gathering them is the caller's job (internal/db/settings), as with Plan's Roots.
	Destinations []placement.RootCandidate
}

// AffinityKeyForPath groups files by the top-level directory of rel_path, where every shipped template nests one artist or series (section 19.1), so it needs no media-type knowledge; a file directly under the root is its own group.
func AffinityKeyForPath(relPath string) string {
	if i := strings.IndexByte(relPath, '/'); i >= 0 {
		return relPath[:i]
	}
	return relPath
}

// rootGroup is one affinity key's own files on the root being drained --
// plan.go's own group cannot be reused here since it assumes a destination
// is already chosen, which drain has not done yet at grouping time.
type rootGroup struct {
	affinityKey string
	files       []FileOnRoot
	totalBytes  int64
}

func groupFilesOnRoot(files []FileOnRoot) []rootGroup {
	order := make([]string, 0, len(files))
	byKey := map[string]*rootGroup{}
	for _, f := range files {
		key := AffinityKeyForPath(f.RelPath)
		g, seen := byKey[key]
		if !seen {
			g = &rootGroup{affinityKey: key}
			byKey[key] = g
			order = append(order, key)
		}
		g.files = append(g.files, f)
		g.totalBytes += f.SizeBytes
	}
	groups := make([]rootGroup, len(order))
	for i, key := range order {
		groups[i] = *byKey[key]
	}
	return groups
}

// PlanDrain groups files by AffinityKeyForPath and places each group, largest first, on the destination with most free space, decrementing it as it goes; an unplaceable group refuses the whole drain (ADR-51) before Plan, whose guards are unchanged.
func PlanDrain(req DrainRequest) (MovePlan, error) {
	groups := groupFilesOnRoot(req.Files)
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].totalBytes > groups[j].totalBytes })

	remaining := make(map[int64]int64, len(req.Destinations))
	for _, d := range req.Destinations {
		remaining[d.RootFolderID] = d.FreeBytes
	}

	var files []FileToMove
	for _, g := range groups {
		candidates := make([]placement.RootCandidate, len(req.Destinations))
		for i, d := range req.Destinations {
			d.FreeBytes = remaining[d.RootFolderID]
			candidates[i] = d
		}
		chosen, decision, err := placement.Resolve(placement.Request{
			SizeBytes: g.totalBytes, Strategy: placement.StrategyMostFreeSpace,
		}, candidates)
		if err != nil {
			return MovePlan{}, fmt.Errorf("mover: resolve drain destination for %q: %w", g.affinityKey, err)
		}
		if decision.Outcome != placement.OutcomePlaced {
			return refusedDrainPlan(req, groups), nil
		}

		remaining[chosen.RootFolderID] -= g.totalBytes
		for _, f := range g.files {
			files = append(files, FileToMove{
				MediaObjectID: f.MediaObjectID, SourceRootFolderID: req.Source.RootFolderID,
				SourceRelPath: f.RelPath, SizeBytes: f.SizeBytes, AffinityKey: g.affinityKey,
				DestRootFolderID: chosen.RootFolderID, DestRelPath: f.RelPath,
			})
		}
	}

	roots := map[int64]placement.RootCandidate{req.Source.RootFolderID: req.Source}
	for _, d := range req.Destinations {
		roots[d.RootFolderID] = d
	}
	return Plan(Request{
		Reason: ReasonDrain, Files: files, Roots: roots,
		SourceReadOnly:    map[int64]bool{req.Source.RootFolderID: req.SourceReadOnly},
		SourceAllowRename: map[int64]bool{req.Source.RootFolderID: req.SourceAllowRename},
	})
}

// refusedDrainPlan names the shortfall across the whole drain, an approximation (total bytes versus headroom, not bin packing) like Plan's per-root message.
func refusedDrainPlan(req DrainRequest, groups []rootGroup) MovePlan {
	var totalBytes int64
	for _, g := range groups {
		totalBytes += g.totalBytes
	}
	var totalCapacity int64
	for _, d := range req.Destinations {
		if headroom := d.FreeBytes - d.MinFreeBytes; headroom > 0 {
			totalCapacity += headroom
		}
	}
	shortfall := totalBytes - totalCapacity
	if shortfall < 0 {
		shortfall = 0
	}
	reason := fmt.Sprintf(
		"drain refused: %d bytes on root %d exceed the %d bytes the remaining roots can hold (%d bytes short)",
		totalBytes, req.Source.RootFolderID, totalCapacity, shortfall)

	plan := MovePlan{Reason: ReasonDrain}
	for _, g := range groups {
		for _, f := range g.files {
			plan.Refused = append(plan.Refused, RefusedFile{
				MediaObjectID: f.MediaObjectID, AffinityKey: g.affinityKey, Reason: reason,
			})
		}
	}
	return plan
}
