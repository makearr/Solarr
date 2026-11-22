// Package mover is ADR-51's move engine for rename, category migration, rebalance and drain; Plan is pure logic over caller-supplied facts (named MovePlan since Go forbids a function and type sharing a name), and Apply is built on top.
package mover

import (
	"fmt"
	"sort"

	"github.com/makearr/Solarr/internal/moving/placement"
)

// Reason is why this move is happening -- ADR-51's own four callers.
type Reason string

// The four reasons ADR-51 names. Every guarantee below applies to all four;
// they differ only in how FileToMove.DestRootFolderID got chosen.
const (
	ReasonRename    Reason = "RENAME"
	ReasonCategory  Reason = "CATEGORY"
	ReasonRebalance Reason = "REBALANCE"
	ReasonDrain     Reason = "DRAIN"
	// ReasonMove is a person moving one work to another root, Sonarr's and Radarr's Edit -> root folder.
	ReasonMove Reason = "MOVE"
)

// FileToMove is one file Plan considers, exactly as the caller already read
// or decided it -- Plan touches no store and calls no resolver itself.
type FileToMove struct {
	MediaObjectID      int64
	SourceRootFolderID int64
	SourceRelPath      string
	SizeBytes          int64
	// AffinityKey groups files that must move together so a drain or rebalance never splits them; it is opaque here because deriving it is media-type knowledge (ADR-23), as in placement.Request.
	AffinityKey string
	// DestRootFolderID is resolved by the caller (the user's choice, or placement.Resolve for REBALANCE/DRAIN); Plan only checks whether it is safe and affordable.
	DestRootFolderID int64
	// DestRelPath is already computed by the caller (internal/moving/naming for a
	// rename, unchanged for category/rebalance/drain).
	DestRelPath string
	// Companions move with the file in the same item: files beside it that are no media objects.
	Companions []Companion
}

// Companion is a file that moves with a media file but is none itself (a subtitle, an nfo, a picture), on the root
// its file moves to.
type Companion struct {
	SourceRelPath string
	DestRelPath   string
}

// Request is what Plan needs.
type Request struct {
	Reason Reason
	Files  []FileToMove
	// Roots is every root any file reads from or writes to, keyed by id, reusing placement.RootCandidate so two copies of one policy shape cannot drift.
	Roots map[int64]placement.RootCandidate
	// SourceReadOnly and SourceAllowRename answer Invariant 78's other half, which RootCandidate lacks; keyed by root folder id, and an absent root is treated as not read-only.
	SourceReadOnly    map[int64]bool
	SourceAllowRename map[int64]bool
}

// PlannedMove is one approved file in Apply order, largest affinity group first (ADR-51 drain, generalised since moving a whole artist as a unit helps every reason).
type PlannedMove struct {
	MediaObjectID      int64
	SourceRootFolderID int64
	SourceRelPath      string
	DestRootFolderID   int64
	DestRelPath        string
	SizeBytes          int64
	AffinityKey        string
	// SameFilesystem tells Apply whether to rename(2) or copy, verify and unlink (ADR-51), decided once here from both roots' FSID.
	SameFilesystem bool
	Companions     []Companion
}

// RefusedFile is one file Plan could not place, and why -- ADR-10's own
// "structured data, never a formatted verdict" rule, the same shape
// placement.RejectedRoot already uses.
type RefusedFile struct {
	MediaObjectID int64
	AffinityKey   string
	Reason        string
}

// RootDelta is one root's preview of bytes in, bytes out and resulting free space (Invariant 73, ADR-51).
type RootDelta struct {
	RootFolderID       int64
	BytesIn            int64
	BytesOut           int64
	ResultingFreeBytes int64
}

// MovePlan is what one Plan call decided.
type MovePlan struct {
	Reason     Reason
	Moves      []PlannedMove
	Refused    []RefusedFile
	RootDeltas []RootDelta
}

// group is one affinity key's files, kept together through refusal and ordering: a shortfall for one file refuses the group, so no artist is split across roots (ADR-51).
type group struct {
	affinityKey string
	files       []FileToMove
	totalBytes  int64
}

// Plan checks every file's move against the destination's policy and the batch's space and returns what may proceed, largest group first; refusal is per group except ReasonDrain, where one shortfall refuses every group rather than evacuating partially.
func Plan(req Request) (MovePlan, error) {
	groups, err := groupByAffinity(req.Files)
	if err != nil {
		return MovePlan{}, err
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].totalBytes > groups[j].totalBytes })

	deltas := map[int64]*RootDelta{}
	deltaFor := func(rootFolderID int64) *RootDelta {
		if d, ok := deltas[rootFolderID]; ok {
			return d
		}
		free := int64(0)
		if candidate, ok := req.Roots[rootFolderID]; ok {
			free = candidate.FreeBytes
		}
		d := &RootDelta{RootFolderID: rootFolderID, ResultingFreeBytes: free}
		deltas[rootFolderID] = d
		return d
	}

	result := MovePlan{Reason: req.Reason}
	var pendingRefusal []RefusedFile
	for _, g := range groups {
		reason, ok := guardGroup(req, g)
		if !ok {
			for _, f := range g.files {
				pendingRefusal = append(pendingRefusal, RefusedFile{
					MediaObjectID: f.MediaObjectID, AffinityKey: g.affinityKey, Reason: reason,
				})
			}
			continue
		}

		dest := deltaFor(g.files[0].DestRootFolderID)
		// A same-root group (RENAME in place) consumes no new capacity since it is always rename(2), so checking the reserve would wrongly refuse a large but affordable rename.
		if !allSameRoot(g) && dest.ResultingFreeBytes-g.totalBytes < minFreeBytes(req.Roots, g.files[0].DestRootFolderID) {
			shortfall := minFreeBytes(req.Roots, g.files[0].DestRootFolderID) - (dest.ResultingFreeBytes - g.totalBytes)
			reason := fmt.Sprintf("insufficient space on root %d: %d bytes short of the reserve",
				g.files[0].DestRootFolderID, shortfall)
			for _, f := range g.files {
				pendingRefusal = append(pendingRefusal, RefusedFile{
					MediaObjectID: f.MediaObjectID, AffinityKey: g.affinityKey, Reason: reason,
				})
			}
			continue
		}

		for _, f := range g.files {
			source := req.Roots[f.SourceRootFolderID]
			destCandidate := req.Roots[f.DestRootFolderID]
			result.Moves = append(result.Moves, PlannedMove{
				MediaObjectID: f.MediaObjectID, SourceRootFolderID: f.SourceRootFolderID,
				SourceRelPath: f.SourceRelPath, DestRootFolderID: f.DestRootFolderID,
				DestRelPath: f.DestRelPath, SizeBytes: f.SizeBytes, AffinityKey: g.affinityKey, Companions: f.Companions,
				SameFilesystem: source.FSID != "" && source.FSID == destCandidate.FSID,
			})
			// Bytes leaving a root free up space there -- tracked per file
			// since one affinity group's files could in principle come from
			// different source roots, unlike its single shared destination.
			sourceDelta := deltaFor(f.SourceRootFolderID)
			sourceDelta.BytesOut += f.SizeBytes
			sourceDelta.ResultingFreeBytes += f.SizeBytes
		}
		dest.BytesIn += g.totalBytes
		dest.ResultingFreeBytes -= g.totalBytes
	}

	// A drain's own guarantee: any shortfall refuses the whole batch, named
	// in bytes, rather than evacuating what fits and stalling on the rest.
	if req.Reason == ReasonDrain && len(pendingRefusal) > 0 {
		result.Moves = nil
		for k := range deltas {
			delete(deltas, k)
		}
		for _, g := range groups {
			for _, f := range g.files {
				result.Refused = append(result.Refused, RefusedFile{
					MediaObjectID: f.MediaObjectID, AffinityKey: g.affinityKey,
					Reason: "drain refused: " + firstReasonFor(pendingRefusal, g.affinityKey),
				})
			}
		}
	} else {
		result.Refused = pendingRefusal
	}

	for _, d := range deltas {
		result.RootDeltas = append(result.RootDeltas, *d)
	}
	sort.Slice(result.RootDeltas, func(i, j int) bool {
		return result.RootDeltas[i].RootFolderID < result.RootDeltas[j].RootFolderID
	})
	return result, nil
}

func firstReasonFor(refused []RefusedFile, affinityKey string) string {
	for _, r := range refused {
		if r.AffinityKey == affinityKey {
			return r.Reason
		}
	}
	return "the remaining roots cannot hold the content"
}

// groupByAffinity partitions files by AffinityKey and refuses a group whose files disagree on destination, since ADR-51 never splits a group and that would mask a caller bug.
func groupByAffinity(files []FileToMove) ([]group, error) {
	order := make([]string, 0, len(files))
	byKey := map[string]*group{}
	for _, f := range files {
		g, seen := byKey[f.AffinityKey]
		if !seen {
			g = &group{affinityKey: f.AffinityKey}
			byKey[f.AffinityKey] = g
			order = append(order, f.AffinityKey)
		} else if g.files[0].DestRootFolderID != f.DestRootFolderID {
			return nil, fmt.Errorf("mover: affinity group %q disagrees on destination root (%d vs %d)",
				f.AffinityKey, g.files[0].DestRootFolderID, f.DestRootFolderID)
		}
		g.files = append(g.files, f)
		g.totalBytes += f.SizeBytes
	}
	groups := make([]group, len(order))
	for i, key := range order {
		groups[i] = *byKey[key]
	}
	return groups, nil
}

// guardGroup checks Invariant 78 (source and destination policy) for every
// file in a group -- one guard failure refuses the whole group, matching
// "whole artists and whole series move together" applied to refusal.
func guardGroup(req Request, g group) (reason string, ok bool) {
	destID := g.files[0].DestRootFolderID
	dest, destKnown := req.Roots[destID]
	if !destKnown {
		return fmt.Sprintf("destination root %d is not known to this plan", destID), false
	}
	if !dest.Online {
		return fmt.Sprintf("destination root %d is offline", destID), false
	}
	if !dest.Writable {
		return fmt.Sprintf("destination root %d is not writable", destID), false
	}
	// Section 11.8 allows RENAME within one root with read_only plus allow_rename together; every other reason targets a different root and keeps the unconditional read-only refusal.
	if dest.ReadOnly && !isSameRootRename(req, g, destID) {
		return fmt.Sprintf("destination root %d is read-only", destID), false
	}

	for _, f := range g.files {
		if isSameRootRename(req, g, destID) && f.SourceRootFolderID == destID {
			continue // covered by the read_only+allow_rename check above
		}
		if req.SourceReadOnly[f.SourceRootFolderID] && !req.SourceAllowRename[f.SourceRootFolderID] {
			return fmt.Sprintf("source root %d is read-only and does not allow rename", f.SourceRootFolderID), false
		}
		source, sourceKnown := req.Roots[f.SourceRootFolderID]
		if sourceKnown && !source.Online {
			return fmt.Sprintf("source root %d is offline", f.SourceRootFolderID), false
		}
	}
	return "", true
}

// allSameRoot reports whether every file's destination equals its source, for the reserve exemption; isSameRootRename is narrower because read_only's exemption needs ReasonRename and allow_rename.
func allSameRoot(g group) bool {
	for _, f := range g.files {
		if f.SourceRootFolderID != f.DestRootFolderID {
			return false
		}
	}
	return true
}

// isSameRootRename reports a true in-place rename (same root, ReasonRename, allow_rename set), the narrow case section 11.8 permits; never true for the other three reasons.
func isSameRootRename(req Request, g group, destID int64) bool {
	if req.Reason != ReasonRename || !req.SourceAllowRename[destID] {
		return false
	}
	return allSameRoot(g)
}

func minFreeBytes(roots map[int64]placement.RootCandidate, rootFolderID int64) int64 {
	if candidate, ok := roots[rootFolderID]; ok {
		return candidate.MinFreeBytes
	}
	return 0
}
