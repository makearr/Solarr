package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
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
	// Credits are the part's own: an episode's guest stars, director and writers.
	Credits []PartCredit
	// AltNumbers are the part's numbers under other schemes, by scheme ("absolute").
	AltNumbers map[string]string
}

// PartCredit is one person credited on a part.
type PartCredit struct {
	PersonID   int64
	PersonName string
	Role       string
	Character  string
}

// PartFile is the one file covering a part, and what probing measured of it.
type PartFile struct {
	MediaObjectID int64
	AudioCodec    string
	VideoCodec    string
	Container     string
	BitrateKbps   int64
	Lossless      bool
	// Extension is the file's own type, "m4a": ffprobe's container is a demuxer list.
	Extension string
	SizeBytes int64
	// What the probe measured of a video; empty for a field it did not read.
	Resolution, HDR, AudioChannels string
	VideoBitrateKbps               int64
	AudioLanguages, Subtitles      []string
	// FrameRate is measured; CRF is the constant rate factor the encoder states, 0 when it states none.
	FrameRate, CRF float64
}

// Credit is one work-level credit, with the person's own name -- the work
// detail page has no use for a bare person id.
type Credit struct {
	PersonID   int64
	PersonName string
	Role       string
	Character  string
	// ImageURL is the person's photo at its provider, empty without one.
	ImageURL string
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
	// Groups are the work's SET items: an album's one, with no ref, or a series' seasons.
	Groups []GroupStatus
}

// GroupStatus is one SET item of a work: the parts "<Ref>:..." below it, or every part for an empty Ref.
type GroupStatus struct {
	Ref     string
	SortKey string
	DesiredItemStatus
}

// PartCursor is a position in a work's parts: the sort key, the id breaking ties.
type PartCursor struct {
	SortKey string
	ID      int64
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
	partDetails, err := detailParts(ctx, queries, id, parts)
	if err != nil {
		return WorkDetail{}, err
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
		credits[index] = Credit{PersonID: row.PersonID, PersonName: row.PersonName, Role: row.Role, Character: row.Character, ImageURL: row.ImageUrl}
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
	var groups []GroupStatus
	for _, item := range items {
		if item.SatisfactionKind != "SET" || item.EditionID.Valid {
			continue
		}
		group := GroupStatus{Ref: item.MediaRef, SortKey: item.MediaSortKey, DesiredItemStatus: DesiredItemStatus{
			ID: item.ID, Monitored: item.Monitored == 1, PresentCount: item.PresentCount, State: item.State,
			LastSearchedAt: lastSearched[item.ID],
		}}
		if item.RequiredCount.Valid {
			group.RequiredCount = &item.RequiredCount.Int64
		}
		groups = append(groups, group)
	}
	slices.SortStableFunc(groups, func(a, b GroupStatus) int { return strings.Compare(a.SortKey, b.SortKey) })

	return WorkDetail{
		Work: work, ExternalIDs: externalIDs, Editions: editionDetails,
		Parts: partDetails, Credits: credits, Genres: genres, RootFolderIDs: rootFolderIDs,
		CategoryIDs: categoryIDs, Wanted: wanted, Groups: groups,
	}, nil
}

// Parts answers one page of a work's parts, of one group or of all with groupRef "", each with its file and its own
// wanted item.
func (w Works) Parts(ctx context.Context, workID int64, groupRef string, after PartCursor, limit int) ([]PartDetail, error) {
	queries := librarydb.New(w.store.Read())
	parts, err := queries.ListWorkPartsPage(ctx, librarydb.ListWorkPartsPageParams{
		WorkID: workID, GroupRef: groupRef, AfterSortKey: after.SortKey, AfterID: after.ID, PageSize: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("library: parts of work %d: %w", workID, err)
	}
	details, err := detailParts(ctx, queries, workID, parts)
	if err != nil {
		return nil, err
	}
	items, err := queries.ListDesiredItemsForWork(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("library: parts of work %d: %w", workID, err)
	}
	for _, item := range items {
		if item.SatisfactionKind != "SINGLE" || item.MediaRef == "" {
			continue
		}
		for index := range details {
			if details[index].Part.MediaRef == item.MediaRef {
				status := DesiredItemStatus{ID: item.ID, Monitored: item.Monitored == 1, PresentCount: item.PresentCount, State: item.State}
				if item.RequiredCount.Valid {
					status.RequiredCount = &item.RequiredCount.Int64
				}
				details[index].Wanted = &status
			}
		}
	}
	return details, nil
}

// detailParts gives parts their external ids and the file covering each.
func detailParts(ctx context.Context, queries *librarydb.Queries, id int64, parts []librarydb.WorkPart) ([]PartDetail, error) {
	partFiles, err := queries.ListPartFilesForWork(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("library: work detail: part files: %w", err)
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
			Extension:     strings.ToLower(strings.TrimPrefix(path.Ext(row.RelPath), ".")),
			SizeBytes:     row.SizeBytes, Resolution: row.Resolution.String, HDR: row.Hdr.String,
			AudioChannels: row.AudioChannels.String, VideoBitrateKbps: row.VideoBitrate.Int64 / 1000,
			AudioLanguages: jsonStrings(row.Languages), Subtitles: jsonStrings(row.Subtitles),
			FrameRate: row.FrameRate.Float64, CRF: row.Crf.Float64,
		}
	}
	partIDs := make([]int64, len(parts))
	for index, part := range parts {
		partIDs[index] = part.ID
	}
	creditsOf := map[int64][]PartCredit{}
	altNumbersOf := map[int64]map[string]string{}
	if len(partIDs) > 0 {
		credits, err := queries.ListCreditsForParts(ctx, librarydb.ListCreditsForPartsParams{WorkID: id, PartIds: partIDs})
		if err != nil {
			return nil, fmt.Errorf("library: work detail: part credits: %w", err)
		}
		for _, credit := range credits {
			creditsOf[credit.PartID] = append(creditsOf[credit.PartID], PartCredit{
				PersonID: credit.PersonID, PersonName: credit.PersonName, Role: credit.Role, Character: credit.Character,
			})
		}
		numbers, err := queries.ListAltNumbersForParts(ctx, partIDs)
		if err != nil {
			return nil, fmt.Errorf("library: work detail: part numbers: %w", err)
		}
		for _, number := range numbers {
			if altNumbersOf[number.PartID] == nil {
				altNumbersOf[number.PartID] = map[string]string{}
			}
			if _, have := altNumbersOf[number.PartID][number.Scheme]; !have {
				altNumbersOf[number.PartID][number.Scheme] = number.AltRef
			}
		}
	}

	partDetails := make([]PartDetail, len(parts))
	for index, part := range parts {
		partExternalIDs, err := queries.ListWorkPartExternalIDs(ctx, part.ID)
		if err != nil {
			return nil, fmt.Errorf("library: work detail: part external ids: %w", err)
		}
		partDetails[index] = PartDetail{Part: part, ExternalIDs: partExternalIDs, Credits: creditsOf[part.ID], AltNumbers: altNumbersOf[part.ID]}
		if file, found := fileByRef[part.MediaRef]; found {
			partDetails[index].File = &file
		}
	}

	return partDetails, nil
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

// Places answers the root folders a work is on and the libraries that want it; sql.ErrNoRows for no such work.
func (w Works) Places(ctx context.Context, id int64) (rootFolderIDs, categoryIDs []int64, err error) {
	queries := librarydb.New(w.store.Read())
	if _, err := queries.GetWork(ctx, id); err != nil {
		return nil, nil, fmt.Errorf("library: places of work %d: %w", id, err)
	}
	if rootFolderIDs, err = queries.ListRootFoldersForWork(ctx, id); err != nil {
		return nil, nil, fmt.Errorf("library: places of work %d: %w", id, err)
	}
	items, err := queries.ListDesiredItemsForWork(ctx, id)
	if err != nil {
		return nil, nil, fmt.Errorf("library: places of work %d: %w", id, err)
	}
	for _, item := range items {
		if !slices.Contains(categoryIDs, item.CategoryID) {
			categoryIDs = append(categoryIDs, item.CategoryID)
		}
	}
	return rootFolderIDs, categoryIDs, nil
}

// ErrNoWantedItem reports a work, group or part that no wanted item covers, so there is nothing to search.
var ErrNoWantedItem = errors.New("library: nothing wanted covers it")

// SearchScope names what of a work a search is for: one group by its ref, one part by its id, or neither for the
// work's own item.
type SearchScope struct {
	Group  *string
	PartID *int64
}

// WantedItemFor answers the SET item a scope is searched through and the ref of the part it narrows to, if any.
func (w Works) WantedItemFor(ctx context.Context, workID int64, scope SearchScope) (int64, string, error) {
	queries := librarydb.New(w.store.Read())
	items, err := queries.ListDesiredItemsForWork(ctx, workID)
	if err != nil {
		return 0, "", fmt.Errorf("library: wanted item of work %d: %w", workID, err)
	}
	items = slices.DeleteFunc(items, func(item librarydb.DesiredItem) bool { return item.SatisfactionKind != "SET" })
	ref := ""
	switch {
	case scope.PartID != nil:
		part, err := queries.GetWorkPart(ctx, *scope.PartID)
		if errors.Is(err, sql.ErrNoRows) || err == nil && part.WorkID != workID {
			return 0, "", ErrPartNotFound
		}
		if err != nil {
			return 0, "", fmt.Errorf("library: wanted item of work %d: %w", workID, err)
		}
		partRef := part.MediaRef
		// The group whose ref scopes the part, before the work's own item.
		slices.SortStableFunc(items, func(a, b librarydb.DesiredItem) int { return len(b.MediaRef) - len(a.MediaRef) })
		for _, item := range items {
			if partInScope(partRef, item.MediaRef) {
				return item.ID, partRef, nil
			}
		}
		return 0, "", ErrNoWantedItem
	case scope.Group != nil:
		ref = *scope.Group
	}
	for _, item := range items {
		if item.MediaRef == ref {
			return item.ID, "", nil
		}
	}
	return 0, "", ErrNoWantedItem
}

// MediaTypeOf answers a work's media type; sql.ErrNoRows for no such work.
func (w Works) MediaTypeOf(ctx context.Context, workID int64) (string, error) {
	work, err := librarydb.New(w.store.Read()).GetWork(ctx, workID)
	if err != nil {
		return "", fmt.Errorf("library: media type of work %d: %w", workID, err)
	}
	return work.MediaType, nil
}

// SetManualField gives a work field a value of the user's own, which every provider's value gives way to; an empty
// value returns the field to the providers'.
func (w Works) SetManualField(ctx context.Context, at time.Time, workID int64, field, value string) error {
	err := w.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		if value == "" {
			return queries.DeleteWorkFieldByProvider(ctx, librarydb.DeleteWorkFieldByProviderParams{WorkID: workID, Field: field, Provider: manualProvider})
		}
		return queries.SetWorkField(ctx, librarydb.SetWorkFieldParams{
			WorkID: workID, Field: field, Provider: manualProvider, Value: sql.NullString{String: value, Valid: true}, FetchedAt: at.Unix(),
		})
	})
	if err != nil {
		return fmt.Errorf("library: set %s of work %d: %w", field, workID, err)
	}
	return nil
}

// ManualFields answers the work's fields the user set themselves, by field.
func (w Works) ManualFields(ctx context.Context, workID int64) (map[string]string, error) {
	fields, err := librarydb.New(w.store.Read()).ListWorkFields(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("library: fields of work %d: %w", workID, err)
	}
	manual := map[string]string{}
	for _, field := range fields {
		if field.Provider == manualProvider && field.Value.Valid {
			manual[field.Field] = field.Value.String
		}
	}
	return manual, nil
}
