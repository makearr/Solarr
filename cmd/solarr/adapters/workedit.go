package adapters

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
)

// WorkEdit satisfies apiserver.WorkEditor: an album's monitoring, own quality profile and tags in one place.
type WorkEdit struct {
	Monitor  WorksMonitor
	Desired  library.Desired
	People   library.People
	Settings *settings.Cache
	// Identity answers which roots hold a work's files.
	Identity library.Identity
	// Facets answers a media type's facets; the editable ones are the dialog's fields. Nil edits none.
	Facets func(mediaType string) []media.Facet
	// Refreshing answers a media type's refresh, for a field the type derives other data from; nil refreshes nothing.
	Refreshing func(mediaType string) (media.Refreshing, bool)
	// Genre tree answers a media type's genre tree key, which a typed tag name is looked up in first.
	GenreTree func(mediaType string) string
}

// editable answers the editable facets of a work's media type.
func (a WorkEdit) editable(mediaType string) []media.Facet {
	if a.Facets == nil {
		return nil
	}
	var editable []media.Facet
	for _, facet := range a.Facets(mediaType) {
		if facet.Editable {
			editable = append(editable, facet)
		}
	}
	return editable
}

// GetWorkEdit answers what the dialog opens with; an album nothing ever wanted reads as unmonitored.
func (a WorkEdit) GetWorkEdit(ctx context.Context, workID int64) (apiserver.WorkEditRow, error) {
	mediaType, err := a.Monitor.Works.MediaTypeOf(ctx, workID)
	if err != nil {
		return apiserver.WorkEditRow{}, err
	}
	row := apiserver.WorkEditRow{Tags: []string{}, RootFolderIDs: []int64{}}
	roots, err := a.Identity.WorkFileRoots(ctx, workID)
	if err != nil {
		return apiserver.WorkEditRow{}, err
	}
	for _, root := range roots {
		if !slices.Contains(row.RootFolderIDs, root.RootFolderID) {
			row.RootFolderIDs = append(row.RootFolderIDs, root.RootFolderID)
		}
	}
	if editable := a.editable(mediaType); len(editable) > 0 {
		manual, err := a.Monitor.Works.ManualFields(ctx, workID)
		if err != nil {
			return apiserver.WorkEditRow{}, err
		}
		for _, facet := range editable {
			if value, set := manual[facet.Source.Name]; set {
				if row.Fields == nil {
					row.Fields = map[string]string{}
				}
				row.Fields[facet.Key] = value
			}
		}
	}
	items, err := a.Desired.AlbumItemsForWork(ctx, workID)
	if err != nil {
		return apiserver.WorkEditRow{}, err
	}
	if len(items) > 0 {
		// Monitored when any group is: Sonarr's series switch.
		for _, item := range items {
			row.Monitored = row.Monitored || item.Monitored == 1
		}
		profileID, found, err := a.Desired.ProfileOverride(ctx, items[0].ID)
		if err != nil {
			return apiserver.WorkEditRow{}, err
		}
		if found {
			row.ProfileID = &profileID
		}
	}
	tags, err := a.People.UserTags(ctx, workID)
	if err != nil {
		return apiserver.WorkEditRow{}, err
	}
	for _, tag := range tags {
		row.Tags = append(row.Tags, tag.Name)
	}
	return row, nil
}

// SaveWorkEdit applies the whole dialog; a profile that does not exist is refused before anything is written.
func (a WorkEdit) SaveWorkEdit(ctx context.Context, workID int64, edit apiserver.WorkEditRow) error {
	mediaType, err := a.Monitor.Works.MediaTypeOf(ctx, workID)
	if err != nil {
		return err
	}
	if edit.ProfileID != nil {
		profile, found := a.Settings.Snapshot().QualityProfile(*edit.ProfileID)
		if !found || profile.MediaType != mediaType {
			return fmt.Errorf("quality profile %d is not a %s profile: %w", *edit.ProfileID, mediaType, apiserver.ErrInvalidWorkEdit)
		}
	}
	fields, err := a.fieldWrites(mediaType, edit.Fields)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(edit.Tags))
	for _, name := range edit.Tags {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	// Monitoring changes only when the person changed it, so saving tags keeps a series' season-by-season choice.
	items, err := a.Desired.AlbumItemsForWork(ctx, workID)
	if err != nil {
		return err
	}
	monitored := false
	for _, item := range items {
		monitored = monitored || item.Monitored == 1
	}
	if len(items) == 0 || monitored != edit.Monitored {
		if _, err := a.Monitor.MonitorWork(ctx, workID, apiserver.MonitorScope{Monitored: edit.Monitored}); err != nil {
			return err
		}
		if items, err = a.Desired.AlbumItemsForWork(ctx, workID); err != nil {
			return err
		}
	}
	// Written only where it changes: a write marks the item for the planner again, and a bulk edit that only tags
	// thousands of works must not have every one of them planned over.
	for _, item := range items {
		current, found, err := a.Desired.ProfileOverride(ctx, item.ID)
		if err != nil {
			return err
		}
		if found == (edit.ProfileID != nil) && (!found || current == *edit.ProfileID) {
			continue
		}
		if err := a.Desired.SetProfileOverride(ctx, item.ID, edit.ProfileID); err != nil {
			return err
		}
	}
	tags, err := a.People.UserTags(ctx, workID)
	if err != nil {
		return err
	}
	if !sameTagNames(tags, names) {
		if err := a.People.SetUserTags(ctx, workID, a.GenreTree(mediaType), names); err != nil {
			return err
		}
	}
	refresh := false
	manual, err := a.Monitor.Works.ManualFields(ctx, workID)
	if err != nil {
		return err
	}
	for field, value := range fields {
		if manual[field] == value {
			continue
		}
		if err := a.Monitor.Works.SetManualField(ctx, a.Monitor.Clock.Now(), workID, field, value); err != nil {
			return err
		}
		refresh = refresh || a.refreshesWork(mediaType, field)
	}
	if refresh && a.Refreshing != nil {
		if refreshing, ok := a.Refreshing(mediaType); ok {
			if _, err := refreshing.RefreshWork(ctx, workID); err != nil {
				a.Monitor.Logger.Warn("refresh after an edit failed", slog.Int64("work_id", workID), slog.Any("error", err))
			}
		}
	}
	return nil
}

// sameTagNames says whether the work already carries exactly these tags, in any order. A name that is an alias of the
// tag it resolves to reads as a change, which only costs the rewrite.
func sameTagNames(current []library.TagName, names []string) bool {
	if len(current) != len(names) {
		return false
	}
	have := make([]string, len(current))
	for i, tag := range current {
		have[i] = tag.Name
	}
	want := slices.Clone(names)
	slices.Sort(have)
	slices.Sort(want)
	return slices.Equal(have, want)
}

// refreshesWork says whether changing the work field reads the work again.
func (a WorkEdit) refreshesWork(mediaType, field string) bool {
	return slices.ContainsFunc(a.editable(mediaType), func(facet media.Facet) bool { return facet.Source.Name == field && facet.RefreshesWork })
}

// fieldWrites checks the edited facets and answers the work fields they set; a value outside an enum's choices or a
// facet that is not editable is refused before anything is written.
func (a WorkEdit) fieldWrites(mediaType string, edited map[string]string) (map[string]string, error) {
	writes := map[string]string{}
	editable := a.editable(mediaType)
	for key, value := range edited {
		index := slices.IndexFunc(editable, func(facet media.Facet) bool { return facet.Key == key })
		if index < 0 {
			return nil, fmt.Errorf("%s is not an editable field of a %s: %w", key, mediaType, apiserver.ErrInvalidWorkEdit)
		}
		facet := editable[index]
		if value != "" && facet.Kind == media.FacetEnum && !slices.ContainsFunc(facet.Choices, func(choice media.Choice) bool { return choice.Key == value }) {
			return nil, fmt.Errorf("%q is not a choice of %s: %w", value, key, apiserver.ErrInvalidWorkEdit)
		}
		writes[facet.Source.Name] = value
	}
	return writes, nil
}

// BulkEdit applies change to every matching work through the same per-album edit the dialog uses, so one album failing
// (a work with no category, a since-deleted one) is logged and skipped rather than aborting the selection.
func (a WorkEdit) BulkEdit(
	ctx context.Context, selection apiserver.BulkSelection, change apiserver.BulkEditChange, preview bool,
) (int64, error) {
	if change.SetProfile && change.ProfileID != nil {
		if _, found := a.Settings.Snapshot().QualityProfile(*change.ProfileID); !found {
			return 0, fmt.Errorf("quality profile %d: %w", *change.ProfileID, apiserver.ErrInvalidWorkEdit)
		}
	}
	matched, err := a.Monitor.matchingWorkIDs(ctx, selection)
	if err != nil || preview {
		return int64(len(matched)), err
	}
	var affected int64
	for _, workID := range matched {
		edit, err := a.GetWorkEdit(ctx, workID)
		if err == nil {
			err = a.SaveWorkEdit(ctx, workID, applyBulkChange(edit, change))
		}
		if err == nil && change.MonitorOption != "" {
			_, err = a.Monitor.MonitorWork(ctx, workID, apiserver.MonitorScope{Option: change.MonitorOption, Special: change.Specials})
		}
		if err != nil {
			a.Monitor.Logger.Warn("bulk edit: one work failed, continuing", slog.Int64("work_id", workID), slog.Any("error", err))
			continue
		}
		affected++
	}
	return affected, nil
}

// applyBulkChange is edit with change laid over it; tags compare case-insensitively, as the tag table's own slugs do.
func applyBulkChange(edit apiserver.WorkEditRow, change apiserver.BulkEditChange) apiserver.WorkEditRow {
	if change.Monitored != nil {
		edit.Monitored = *change.Monitored
	}
	if change.SetProfile {
		edit.ProfileID = change.ProfileID
	}
	if len(change.Fields) > 0 {
		edit.Fields = change.Fields
	} else {
		edit.Fields = nil
	}
	removed := make(map[string]bool, len(change.RemoveTags))
	for _, name := range change.RemoveTags {
		removed[strings.ToLower(strings.TrimSpace(name))] = true
	}
	tags := make([]string, 0, len(edit.Tags)+len(change.AddTags))
	seen := map[string]bool{}
	for _, name := range append(slices.Clone(edit.Tags), change.AddTags...) {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" || removed[key] && !slices.Contains(change.AddTags, name) || seen[key] {
			continue
		}
		seen[key] = true
		tags = append(tags, strings.TrimSpace(name))
	}
	edit.Tags = tags
	return edit
}

// LibraryTags answers the tags the libraries' works carry, with how many carry each or one below it.
func (a WorkEdit) LibraryTags(ctx context.Context, categoryIDs []int64) ([]apiserver.TagRow, error) {
	nodes, err := a.Monitor.Works.TagsInCategories(ctx, categoryIDs)
	if err != nil {
		return nil, err
	}
	rows := make([]apiserver.TagRow, len(nodes))
	for i, node := range nodes {
		rows[i] = apiserver.TagRow(node)
	}
	return rows, nil
}

// TagTree answers the tags of a genre tree, "*" for all.
func (a WorkEdit) TagTree(ctx context.Context, genreTree string) ([]apiserver.TagRow, error) {
	nodes, err := a.People.TagTree(ctx, genreTree)
	if err != nil {
		return nil, err
	}
	rows := make([]apiserver.TagRow, len(nodes))
	for i, node := range nodes {
		rows[i] = apiserver.TagRow(node)
	}
	return rows, nil
}

// CreateTag adds a person's tag.
func (a WorkEdit) CreateTag(ctx context.Context, name, genreTree string, parentID int64) (apiserver.TagRow, error) {
	id, err := a.People.CreateTag(ctx, name, genreTree, parentID)
	if err != nil {
		return apiserver.TagRow{}, tagError(err)
	}
	return apiserver.TagRow{ID: id, Name: name, ParentID: parentID, GenreTree: genreTree, Source: "user"}, nil
}

// EditTag renames and places a tag.
func (a WorkEdit) EditTag(ctx context.Context, id int64, name string, parentID int64) error {
	return tagError(a.People.EditTag(ctx, id, name, parentID))
}

// MergeTag folds one tag into another.
func (a WorkEdit) MergeTag(ctx context.Context, fromID, intoID int64) error {
	return tagError(a.People.MergeTag(ctx, fromID, intoID))
}

// DeleteTag removes a tag.
func (a WorkEdit) DeleteTag(ctx context.Context, id int64) error {
	return a.People.DeleteTag(ctx, id)
}

// tagError maps the library's refusals to the ones writeWorkEditError knows.
func tagError(err error) error {
	switch {
	case errors.Is(err, library.ErrTagExists):
		return fmt.Errorf("%w: %w", apiserver.ErrTagNameTaken, err)
	case errors.Is(err, library.ErrTagMove):
		return fmt.Errorf("%w: %w", apiserver.ErrTagMove, err)
	case errors.Is(err, library.ErrTagName):
		return fmt.Errorf("%w: %w", apiserver.ErrInvalidWorkEdit, err)
	}
	return err
}
