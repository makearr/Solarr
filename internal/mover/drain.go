package mover

import (
	"fmt"
	"sort"
	"strings"

	"github.com/makearr/MediaNexus/internal/placement"
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
	// Destinations are the other root folders sharing a category with
	// Source, excluding Source itself -- gathering them is the caller's job
	// (internal/settings), the same separation Plan's own Roots parameter
	// already keeps from this package.
	Destinations []placement.RootCandidate
}

// AffinityKeyForPath groups files by the top-level directory of their
// rel_path -- the same directory every shipped naming template already
// nests one artist, show or series beneath (section 19.1), so this needs no
// media-type knowledge at all: it reads path structure only, unlike a
// caller deriving FileToMove.AffinityKey from real credits. A file directly
// under the root (no separator) is its own group.
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

// PlanDrain groups every file on Source by AffinityKeyForPath, then asks
// internal/placement.Resolve to place each group (largest first) on
// whichever destination has the most free space -- decrementing that
// destination's own remaining bytes before resolving the next group, so
// later groups see the capacity earlier ones already claimed, rather than
// every group racing for the same "most free" root as of the start of the
// drain. A group Resolve cannot place anywhere refuses the whole drain up
// front (ADR-51's own "refuses to start if the remaining roots cannot hold
// the content"), naming the shortfall, without ever calling Plan with a
// partial assignment. Once every group has a destination, the real guards
// (read-only, reserve, online) are Plan's own job, unchanged.
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

// refusedDrainPlan names the shortfall across the whole drain, not just the
// one group placement.Resolve could not place -- an honest approximation
// (total bytes vs. total headroom, not a real bin-packing proof) rather
// than a precise guarantee, the same spirit as Plan's own per-root shortfall
// message.
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
