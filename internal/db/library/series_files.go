package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// PartKey names one episode a file covers: by media_ref, or by another numbering scheme's reference.
type PartKey struct {
	MediaRef  string
	AltScheme string
	AltRef    string
}

// SeriesFileLink is one scanned file and the episodes it covers (several for a multi-episode file).
type SeriesFileLink struct {
	MediaObjectID int64
	Parts         []PartKey
	// KeepMonitoring leaves the episodes' and seasons' monitoring as it is, instead of wanting what has a file.
	KeepMonitoring bool
}

// FileAttachment reports what attaching a batch did.
type FileAttachment struct {
	// Attached counts (file, episode) pairs now covered.
	Attached int
	// Unresolved lists links with an episode the series does not have, by media object.
	Unresolved []int64
}

// AttachFiles records scanned files against a series: the file belongs to the work and its root, and covers each
// episode it names. An episode with a file becomes monitored (Sonarr's Existing), and the season items' counts follow.
func (s Series) AttachFiles(ctx context.Context, at time.Time, workID, rootFolderID int64, links []SeriesFileLink) (FileAttachment, error) {
	var result FileAttachment
	err := s.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		if rootFolderID != 0 {
			if err := queries.LinkWorkRootFolder(ctx, librarydb.LinkWorkRootFolderParams{WorkID: workID, RootFolderID: rootFolderID, FirstSeenAt: at.Unix()}); err != nil {
				return fmt.Errorf("root folder: %w", err)
			}
		}
		items, err := queries.ListDesiredItemsForWork(ctx, workID)
		if err != nil {
			return fmt.Errorf("list items: %w", err)
		}
		itemBySeason := map[string]int64{}
		unmonitoredItem := map[int64]bool{}
		for _, item := range items {
			if item.SatisfactionKind == "SET" && item.MediaRef != "" {
				itemBySeason[item.MediaRef] = item.ID
				unmonitoredItem[item.ID] = item.Monitored == 0
			}
		}
		touched := map[int64]bool{}
		for _, link := range links {
			if err := queries.LinkWorkFile(ctx, librarydb.LinkWorkFileParams{WorkID: workID, MediaObjectID: link.MediaObjectID, FirstSeenAt: at.Unix()}); err != nil {
				return fmt.Errorf("link file: %w", err)
			}
			for _, key := range link.Parts {
				part, found, err := resolveSeriesPart(ctx, queries, workID, key)
				if err != nil {
					return err
				}
				itemID := itemBySeason[seasonOf(part.MediaRef)]
				if !found || itemID == 0 {
					result.Unresolved = append(result.Unresolved, link.MediaObjectID)
					continue
				}
				if part.Monitored == 0 && !link.KeepMonitoring {
					if err := queries.SetWorkPartMonitored(ctx, librarydb.SetWorkPartMonitoredParams{Monitored: 1, ID: part.ID}); err != nil {
						return fmt.Errorf("monitor part: %w", err)
					}
				}
				if err := queries.AttachFile(ctx, librarydb.AttachFileParams{
					DesiredItemID: itemID, MediaObjectID: link.MediaObjectID, Role: "primary", MediaRef: part.MediaRef, AddedAt: at.Unix(),
				}); err != nil {
					return fmt.Errorf("attach file: %w", err)
				}
				if unmonitoredItem[itemID] && !link.KeepMonitoring {
					if err := queries.SetDesiredItemMonitored(ctx, librarydb.SetDesiredItemMonitoredParams{Monitored: 1, ID: itemID}); err != nil {
						return fmt.Errorf("monitor season: %w", err)
					}
					unmonitoredItem[itemID] = false
				}
				touched[itemID] = true
				result.Attached++
			}
		}
		for itemID := range touched {
			if err := recomputeAlbumTx(ctx, queries, itemID, workID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return FileAttachment{}, fmt.Errorf("library: attach series files: %w", err)
	}
	return result, nil
}

func resolveSeriesPart(ctx context.Context, queries *librarydb.Queries, workID int64, key PartKey) (librarydb.WorkPart, bool, error) {
	if key.MediaRef != "" {
		row, err := queries.FindWorkPart(ctx, librarydb.FindWorkPartParams{WorkID: workID, MediaRef: key.MediaRef})
		if err == nil {
			return row, true, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return librarydb.WorkPart{}, false, fmt.Errorf("find part: %w", err)
		}
	}
	if key.AltScheme != "" {
		row, err := queries.FindPartByAltNumber(ctx, librarydb.FindPartByAltNumberParams{WorkID: workID, Scheme: key.AltScheme, AltRef: key.AltRef})
		if err == nil {
			return row, true, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return librarydb.WorkPart{}, false, fmt.Errorf("find part by alt number: %w", err)
		}
	}
	return librarydb.WorkPart{}, false, nil
}

// seasonOf is the season item reference of a part: "s03" for "s03:e10".
func seasonOf(mediaRef string) string {
	season, _, _ := strings.Cut(mediaRef, ":")
	return season
}

// ResolvedPart is the episode a PartKey names.
type ResolvedPart struct {
	PartID   int64
	MediaRef string
	Title    string
	Date     string
}

// ResolveParts looks each key up in a series: its media_ref, else its alternate numbering. A key naming no episode
// is absent from the result, which is parallel to keys only through Found.
func (s Series) ResolveParts(ctx context.Context, workID int64, keys []PartKey) (parts []ResolvedPart, missing int, err error) {
	queries := librarydb.New(s.store.Read())
	for _, key := range keys {
		part, found, err := resolveSeriesPart(ctx, queries, workID, key)
		if err != nil {
			return nil, 0, err
		}
		if !found {
			missing++
			continue
		}
		parts = append(parts, ResolvedPart{PartID: part.ID, MediaRef: part.MediaRef, Title: part.Title.String, Date: part.Date.String})
	}
	return parts, missing, nil
}
