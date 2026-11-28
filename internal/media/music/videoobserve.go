package music

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"time"

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
	threshold := DefaultAmbiguityThreshold
	if c.threshold != nil {
		threshold = c.threshold()
	}
	links := make([]library.WorkLink, len(videos))
	artists := make([]string, len(videos))
	identities := make([]*VideoIdentity, len(videos))
	var queue []library.QueueEntry
	for index, observed := range videos {
		claim := InspectVideo(observed.File.RelPath, observed.Probe, kind)
		artists[index] = claim.Artist
		identity, err := IdentifyVideo(ctx, c.videos, claim, threshold)
		if err != nil {
			c.logger.Warn("video lookup failed; left unverified", slog.String("path", observed.File.RelPath), slog.Any("error", err))
		}
		identities[index] = identity
		links[index] = videoLink(claim, identity, localVideoKey(root.Path, observed.File.RelPath), root.ID, mediaObjectIDs[index])
		if identity == nil {
			queue = append(queue, library.QueueEntry{MediaObjectID: mediaObjectIDs[index], RootFolderID: root.ID, RelPath: observed.File.RelPath, At: now.Unix()})
		}
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
		if err := linkVideoAlbum(ctx, c.identity, now, workID, identities[index]); err != nil {
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

// videoLink is the work a video belongs to: what MusicBrainz named it when it did, else the one named by its file (D6 for video).
func videoLink(claim VideoClaim, identity *VideoIdentity, localKey string, rootFolderID, mediaObjectID int64) library.WorkLink {
	link := library.WorkLink{
		Provider: ProviderLocalVideo, ExternalID: localKey, Confidence: unverifiedConfidence,
		Work: library.WorkIdentity{
			MediaType: "music", CanonicalTitle: claim.Title, SortTitle: library.SortTitle(claim.Title), Subtype: claim.Subtype,
		},
		RootFolderID: rootFolderID, MediaObjectID: mediaObjectID,
	}
	if identity != nil {
		link.Provider, link.ExternalID, link.Confidence = identity.Provider, identity.ExternalID, identity.Confidence
		link.Work.CanonicalTitle, link.Work.SortTitle = identity.Title, library.SortTitle(identity.Title)
	}
	return link
}

// VideoAlbumID is the id a music video holds for its album: the release group then the recording, since a provider id belongs to one work and an album holds several videos.
func VideoAlbumID(releaseGroupMBID, recordingMBID string) string {
	return releaseGroupMBID + "/" + recordingMBID
}

// linkVideoAlbum ties a music video to the release group of its track; a concert is already keyed by its live album.
func linkVideoAlbum(ctx context.Context, identity library.Identity, at time.Time, workID int64, named *VideoIdentity) error {
	if named == nil || named.Provider != ProviderVideoRecording || named.ReleaseGroupMBID == "" {
		return nil
	}
	return identity.LinkExternalIDIfFree(ctx, at, workID, ProviderVideoAlbum, VideoAlbumID(named.ReleaseGroupMBID, named.ExternalID), named.Confidence)
}
