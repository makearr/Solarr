package library

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// EditionDetail is one edition with the external ids recorded against it.
type EditionDetail struct {
	Edition     librarydb.Edition
	ExternalIDs []librarydb.EditionExternalID
}

// PartDetail is one work_part with the external ids recorded against it.
type PartDetail struct {
	Part        librarydb.WorkPart
	ExternalIDs []librarydb.WorkPartExternalID
	// File is what is actually on disk for this part, nil when nothing
	// covers it. Aligned per media_ref, the same link present_count is
	// counted from.
	File *PartFile
	// Wanted is the track's own SINGLE desired_item, when one exists -- nil
	// until someone explicitly wants this one track on its own (todo 4.4),
	// independent of Part.Monitored (which only shrinks or grows the
	// album's own required_count).
	Wanted *DesiredItemStatus
}

// PartFile is the one file covering a part, and what probing measured of it.
type PartFile struct {
	MediaObjectID int64
	AudioCodec    string
	VideoCodec    string
	Container     string
	BitrateKbps   int64
	Lossless      bool
}

// Credit is one work-level credit, with the person's own name -- the work
// detail page has no use for a bare person id.
type Credit struct {
	PersonID   int64
	PersonName string
	Role       string
}

// DesiredItemStatus is one desired_item's own status -- the work's
// album-level SET (section 19.6) or one track's own SINGLE (todo 4.4) --
// what makes "9 of 12 tracks" a fact rather than OnDisk's coarse badge.
type DesiredItemStatus struct {
	ID            int64
	Monitored     bool
	RequiredCount *int64
	PresentCount  int64
	State         string
	// LastSearchedAt is the hunt queue's own record, 0 when this item has
	// never been searched for.
	LastSearchedAt int64
}

// WorkDetail is a work's own fields, its editions, its parts, its credits and
// genre tags, and every level's external ids. RootFolderIDs doubles as
// display data (OnDisk, section 21.3) and one half of the ADR-27 visibility
// check; CategoryIDs is the other half -- a subscription-discovered work has
// no root folder yet but does have a desired_item.category_id.
type WorkDetail struct {
	Work          librarydb.Work
	ExternalIDs   []librarydb.WorkExternalID
	Editions      []EditionDetail
	Parts         []PartDetail
	Credits       []Credit
	Genres        []string
	RootFolderIDs []int64
	CategoryIDs   []int64
	// Wanted is nil when nothing has ever wanted this album -- no scan, no
	// subscription, no user request.
	Wanted *DesiredItemStatus
}

// OnDisk reports whether this work is linked to any root folder at all
// (work_root_folder, migration 0012's bridge). Per-part presence is
// PartDetail.File, aligned through desired_item_file.media_ref.
func (d WorkDetail) OnDisk() bool { return len(d.RootFolderIDs) > 0 }

// Detail composes one work's full structural detail. A missing id surfaces
// sql.ErrNoRows through GetWork, wrapped: callers use errors.Is to detect it.
func (w Works) Detail(ctx context.Context, id int64) (WorkDetail, error) {
	queries := librarydb.New(w.store.Read())

	work, err := queries.GetWork(ctx, id)
	if err != nil {
		return WorkDetail{}, fmt.Errorf("library: work detail: get work: %w", err)
	}

	externalIDs, err := queries.ListWorkExternalIDs(ctx, id)
	if err != nil {
		return WorkDetail{}, fmt.Errorf("library: work detail: work external ids: %w", err)
	}

	editions, err := queries.ListEditions(ctx, id)
	if err != nil {
		return WorkDetail{}, fmt.Errorf("library: work detail: editions: %w", err)
	}
	editionDetails := make([]EditionDetail, len(editions))
	for index, edition := range editions {
		editionExternalIDs, err := queries.ListEditionExternalIDs(ctx, edition.ID)
		if err != nil {
			return WorkDetail{}, fmt.Errorf("library: work detail: edition external ids: %w", err)
		}
		editionDetails[index] = EditionDetail{Edition: edition, ExternalIDs: editionExternalIDs}
	}

	parts, err := queries.ListWorkParts(ctx, id)
	if err != nil {
		return WorkDetail{}, fmt.Errorf("library: work detail: parts: %w", err)
	}
	partFiles, err := queries.ListPartFilesForWork(ctx, id)
	if err != nil {
		return WorkDetail{}, fmt.Errorf("library: work detail: part files: %w", err)
	}
	fileByRef := make(map[string]PartFile, len(partFiles))
	for _, row := range partFiles {
		if _, seen := fileByRef[row.MediaRef]; seen {
			continue
		}
		fileByRef[row.MediaRef] = PartFile{
			MediaObjectID: row.MediaObjectID,
			AudioCodec:    row.AudioCodec.String,
			VideoCodec:    row.VideoCodec.String,
			Container:     row.Container.String,
			BitrateKbps:   bitrateKbps(row.OverallBitrate, row.AudioBitrate),
			Lossless:      row.Lossless.Valid && row.Lossless.Int64 == 1,
		}
	}

	partDetails := make([]PartDetail, len(parts))
	for index, part := range parts {
		partExternalIDs, err := queries.ListWorkPartExternalIDs(ctx, part.ID)
		if err != nil {
			return WorkDetail{}, fmt.Errorf("library: work detail: part external ids: %w", err)
		}
		partDetails[index] = PartDetail{Part: part, ExternalIDs: partExternalIDs}
		if file, found := fileByRef[part.MediaRef]; found {
			partDetails[index].File = &file
		}
	}

	rootFolderIDs, err := queries.ListRootFoldersForWork(ctx, id)
	if err != nil {
		return WorkDetail{}, fmt.Errorf("library: work detail: root folders: %w", err)
	}

	creditRows, err := queries.ListCreditsForWorkWithPerson(ctx, id)
	if err != nil {
		return WorkDetail{}, fmt.Errorf("library: work detail: credits: %w", err)
	}
	credits := make([]Credit, len(creditRows))
	for index, row := range creditRows {
		credits[index] = Credit{PersonID: row.PersonID, PersonName: row.PersonName, Role: row.Role}
	}

	tagRows, err := queries.ListTagsForSubjectWithName(ctx, librarydb.ListTagsForSubjectWithNameParams{
		SubjectType: "work", SubjectID: id,
	})
	if err != nil {
		return WorkDetail{}, fmt.Errorf("library: work detail: genres: %w", err)
	}
	genres := make([]string, len(tagRows))
	for index, row := range tagRows {
		genres[index] = row.TagName
	}

	items, err := queries.ListDesiredItemsForWork(ctx, id)
	if err != nil {
		return WorkDetail{}, fmt.Errorf("library: work detail: desired items: %w", err)
	}
	searchTimes, err := queries.ListHuntSearchTimesForWork(ctx, id)
	if err != nil {
		return WorkDetail{}, fmt.Errorf("library: work detail: hunt times: %w", err)
	}
	lastSearched := make(map[int64]int64, len(searchTimes))
	for _, row := range searchTimes {
		lastSearched[row.DesiredItemID] = row.LastSearchedAt
	}
	var wanted *DesiredItemStatus
	trackWanted := make(map[string]*DesiredItemStatus, len(items))
	categoryIDs := make([]int64, 0, len(items))
	seenCategory := make(map[int64]bool, len(items))
	for _, item := range items {
		if !seenCategory[item.CategoryID] {
			seenCategory[item.CategoryID] = true
			categoryIDs = append(categoryIDs, item.CategoryID)
		}
		status := DesiredItemStatus{
			ID: item.ID, Monitored: item.Monitored == 1, PresentCount: item.PresentCount,
			State: item.State, LastSearchedAt: lastSearched[item.ID],
		}
		if item.RequiredCount.Valid {
			status.RequiredCount = &item.RequiredCount.Int64
		}
		switch {
		case wanted == nil && item.SatisfactionKind == "SET" && !item.EditionID.Valid && item.MediaRef == "":
			wanted = &status
		case item.SatisfactionKind == "SINGLE" && item.MediaRef != "":
			trackWanted[item.MediaRef] = &status
		}
	}
	for index := range partDetails {
		partDetails[index].Wanted = trackWanted[partDetails[index].Part.MediaRef]
	}

	return WorkDetail{
		Work: work, ExternalIDs: externalIDs, Editions: editionDetails,
		Parts: partDetails, Credits: credits, Genres: genres, RootFolderIDs: rootFolderIDs,
		CategoryIDs: categoryIDs, Wanted: wanted,
	}, nil
}

// bitrateKbps prefers the whole file's measured rate and falls back to the
// audio stream's, both stored in bits per second.
func bitrateKbps(overall, audio sql.NullInt64) int64 {
	switch {
	case overall.Valid && overall.Int64 > 0:
		return overall.Int64 / 1000
	case audio.Valid && audio.Int64 > 0:
		return audio.Int64 / 1000
	default:
		return 0
	}
}
