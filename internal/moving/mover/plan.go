// Package mover is ADR-51's move engine: one mechanism behind renaming to
// another root, category migration, rebalancing and draining a disk. Plan is
// pure logic over caller-supplied facts, no I/O of its own -- the same
// separation internal/moving/placement already keeps from internal/db/settings, reused
// here rather than reinvented. This is a deliberate reading of architecture.md
// section 11.11's own sketch ("func Plan(ctx, req) (Plan, error) // pure; no
// I/O beyond DB reads" -- Plan's own result is named MovePlan here since Go
// does not allow a function and a type to share one name): the DB reads
// are the caller's, the same way
// placement.Resolve takes an already-fetched []RootCandidate rather than
// reading settings.db itself. Apply (the resumable job that actually copies,
// verifies and repoints) is a later, separate piece built on top of this one.
package mover

import (
	"fmt"
	"sort"

	"github.com/makearr/MediaNexus/internal/moving/placement"
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
)

// FileToMove is one file Plan considers, exactly as the caller already read
// or decided it -- Plan touches no store and calls no resolver itself.
type FileToMove struct {
	MediaObjectID      int64
	SourceRootFolderID int64
	SourceRelPath      string
	SizeBytes          int64
	// AffinityKey groups files that must move together (an artist's whole
	// discography, a series' whole run) so a drain or rebalance never
	// splits one across roots. Opaque to this package -- deriving it from
	// a work's own credits is media-type knowledge that stays out of core
	// (ADR-23), the identical choice placement.Request.AffinityKey already
	// made.
	AffinityKey string
	// DestRootFolderID is already resolved by the caller: the user's
	// explicit choice (RENAME/CATEGORY), or placement.Resolve's own answer
	// for this file's AffinityKey and the group's total size (REBALANCE/
	// DRAIN). Plan decides nothing about *where*; it only checks whether
	// the choice is safe and affordable.
	DestRootFolderID int64
	// DestRelPath is already computed by the caller (internal/moving/naming for a
	// rename, unchanged for category/rebalance/drain).
	DestRelPath string
}

// Request is what Plan needs.
type Request struct {
	Reason Reason
	Files  []FileToMove
	// Roots is every root folder any file in Files reads from or writes
	// to, keyed by id -- the same RootCandidate shape internal/moving/placement
	// already uses for its own guard checks, reused rather than duplicated
	// (this project's own established lesson: two independent copies of
	// one policy shape is how they drift).
	Roots map[int64]placement.RootCandidate
	// SourceReadOnly and SourceAllowRename answer Invariant 78's other
	// half -- placement.RootCandidate has ReadOnly but no AllowRename
	// field of its own (a destination is never read from a read-only
	// root's own perspective the way a source moving *out* of one needs).
	// Keyed by root folder id; a root absent here is treated as not
	// read-only (never blocks a move out).
	SourceReadOnly    map[int64]bool
	SourceAllowRename map[int64]bool
}

// PlannedMove is one file Plan approved, in the order Apply should run them
// (largest affinity group first, ADR-51's own drain requirement generalised
// to every reason -- moving a whole artist or a whole series as one unit
// benefits every caller, not only drain).
type PlannedMove struct {
	MediaObjectID      int64
	SourceRootFolderID int64
	SourceRelPath      string
	DestRootFolderID   int64
	DestRelPath        string
	SizeBytes          int64
	AffinityKey        string
	// SameFilesystem tells Apply whether to rename(2) (atomic) or copy,
	// verify and unlink (ADR-51's own two-path Apply algorithm) -- decided
	// once, here, from both roots' FSID, rather than re-derived per file
	// during Apply.
	SameFilesystem bool
}

// RefusedFile is one file Plan could not place, and why -- ADR-10's own
// "structured data, never a formatted verdict" rule, the same shape
// placement.RejectedRoot already uses.
type RefusedFile struct {
	MediaObjectID int64
	AffinityKey   string
	Reason        string
}

// RootDelta is one root's own preview: what moves in, what moves out, and
// what its free space becomes -- Invariant 73's "preview first", the exact
// figures ADR-51's own text promises ("how many bytes and how much free
// space each root ends with").
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

// group is one affinity key's own files, kept together through refusal and
// ordering decisions -- ADR-51's "whole artists and whole series move
// together, never file-by-file" applies to being refused too: a shortfall
// that blocks one file in a group blocks the whole group, not a partial move
// that leaves an artist split across two roots.
type group struct {
	affinityKey string
	files       []FileToMove
	totalBytes  int64
}

// Plan checks every file's move against its destination's policy and the
// whole batch's space, then returns what may proceed, largest affinity group
// first. Refusing is per-group (ADR-51's own "whole artists... together"),
// except for ReasonDrain: architecture.md is explicit that a drain "refuses
// to start if the remaining roots cannot hold the content" rather than
// partially evacuating and getting stuck, so a single shortfall refuses
// every group in a drain request, not just the one that could not fit.
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
		// A same-root group (every file's source equals its own destination
		// -- RENAME's own in-place case) consumes no new capacity at all: it
		// is always a rename(2), which duplicates no bytes. Checking the
		// reserve against g.totalBytes here would count space leaving and
		// arriving on the very same root as pure consumption, wrongly
		// refusing a rename of a large, otherwise perfectly affordable
		// group purely because it is large.
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
				DestRelPath: f.DestRelPath, SizeBytes: f.SizeBytes, AffinityKey: g.affinityKey,
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

// groupByAffinity partitions files by AffinityKey, refusing outright a group
// whose files disagree about their own destination -- ADR-51 has no
// provision for splitting one affinity group across two destinations, so a
// caller that tried would be masking a bug in how it built the request, not
// expressing a real move.
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
	// section 11.8's own permitted-check table is explicit that RENAME is
	// the one reason where source and destination are the same root
	// ("APPLY: atomic rename within the root; cross-root is a move, not a
	// rename"), and for exactly that case read_only + allow_rename together
	// permit it (read_only=1 AND allow_rename=1 -> allowed) -- the
	// destination-side override this comment used to say did not exist.
	// Every other reason keeps the unconditional refusal: a category
	// migration, rebalance or drain always targets a different root, where
	// "read-only into" has no rename-style escape hatch at all.
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

// allSameRoot reports whether every file in g has its own destination equal
// to its own source -- true in-place movement, regardless of Reason. Used
// for the reserve check's own "no new capacity consumed" exemption above;
// isSameRootRename below additionally requires ReasonRename and
// allow_rename, since read_only's own exemption is deliberately narrower
// than "physically the same disk" alone.
func allSameRoot(g group) bool {
	for _, f := range g.files {
		if f.SourceRootFolderID != f.DestRootFolderID {
			return false
		}
	}
	return true
}

// isSameRootRename reports whether every file in g is a true in-place
// rename (source root == destination root) under ReasonRename, with the
// one root's own allow_rename set -- the narrow condition section 11.8's
// permitted-check table describes, never true for the other three reasons
// (which always target a different root).
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
