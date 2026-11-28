package music

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

// splitVideos separates the video files of a scan batch from its sound.
func splitVideos(batch []scan.Observed) (sound, videos []scan.Observed) {
	for _, observed := range batch {
		if IsVideoFile(observed.File.RelPath) {
			videos = append(videos, observed)
		} else {
			sound = append(sound, observed)
		}
	}
	return sound, videos
}

// observeVideos records a music library's video files: each is one unverified video work named by the file itself,
// credited to its artist and left unmonitored, until a lookup names it (D6 for video).
func (c Consumer) observeVideos(ctx context.Context, root settings.RootFolder, categoryID int64, videos []scan.Observed) error {
	if len(videos) == 0 {
		return nil
	}
	videos, mediaObjectIDs, err := c.register(ctx, root, videos)
	if err != nil || len(videos) == 0 {
		return err
	}
	videos, mediaObjectIDs, err = c.withoutManual(ctx, videos, mediaObjectIDs)
	if err != nil {
		return fmt.Errorf("music: %w", err)
	}
	if len(videos) == 0 {
		return nil
	}
	kind := ""
	if c.categories != nil {
		if category, found := c.categories.Category(categoryID); found {
			kind = category.Kind
		}
	}
	now := c.clock.Now()
	links := make([]library.WorkLink, len(videos))
	artists := make([]string, len(videos))
	queue := make([]library.QueueEntry, len(videos))
	for index, observed := range videos {
		claim := InspectVideo(observed.File.RelPath, observed.Probe, kind)
		artists[index] = claim.Artist
		links[index] = library.WorkLink{
			Provider: ProviderLocalVideo, ExternalID: localVideoKey(root.Path, observed.File.RelPath), Confidence: unverifiedConfidence,
			Work: library.WorkIdentity{
				MediaType: "music", CanonicalTitle: claim.Title, SortTitle: library.SortTitle(claim.Title), Subtype: claim.Subtype,
			},
			RootFolderID: root.ID, MediaObjectID: mediaObjectIDs[index],
		}
		queue[index] = library.QueueEntry{MediaObjectID: mediaObjectIDs[index], RootFolderID: root.ID, RelPath: observed.File.RelPath, At: now.Unix()}
	}
	workIDs, err := c.identity.ResolveWorks(ctx, now, links)
	if err != nil {
		return fmt.Errorf("music: %w", err)
	}
	if err := c.unidentified.Record(ctx, queue); err != nil {
		return fmt.Errorf("music: %w", err)
	}
	for index, workID := range workIDs {
		if err := c.unmonitorWork(ctx, workID); err != nil {
			return fmt.Errorf("music: %w", err)
		}
		if artists[index] == "" || c.people == nil {
			continue
		}
		personID, err := c.people.ResolveFolderArtist(ctx, now, ProviderLocalArtist, artists[index])
		if err != nil {
			return fmt.Errorf("music: %w", err)
		}
		if err := c.people.AddLeadCredit(ctx, workID, personID, CreditRoleArtist); err != nil {
			return fmt.Errorf("music: %w", err)
		}
	}
	c.logger.Info("scan batch recorded", slog.Int64("root_folder_id", root.ID), slog.Int("videos", len(videos)))
	return nil
}

// localVideoKey is one unverified video work per file: the same file seen again is the same work.
func localVideoKey(sourceRoot, relPath string) string {
	return strings.ToLower(path.Join(sourceRoot, strings.TrimSuffix(relPath, path.Ext(relPath))))
}
