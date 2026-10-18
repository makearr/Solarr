package movie

import (
	"context"
	"fmt"
	"log/slog"
	"path"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/parsemovie"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/filekind"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/keyedlock"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

// IsFeature reports a path worth probing as a movie: a video file that is not a sample, trailer or extra.
func IsFeature(relPath string) bool { return filekind.IsFeature(relPath) }

// ConsumerConfig is what a Consumer needs; every field but Logger is required.
type ConsumerConfig struct {
	FS               fsx.FS
	Physical         library.Physical
	Unidentified     library.Unidentified
	Service          Service
	Reader           library.SeriesReader
	Clock            clock.Clock
	Logger           *slog.Logger
	DefaultProfileID int64
	Categories       interface {
		Category(id int64) (settings.Category, bool)
		ProfileFor(category settings.Category, fallback int64) int64
	}
}

// Consumer is the movie module's scan.Consumer: it registers what a sweep found, reads each file's title and year from
// its path, finds or adds the movie it is, and records the file as that movie's. What it cannot place goes to the same
// Review queue music and TV use.
type Consumer struct {
	config   ConsumerConfig
	logger   *slog.Logger
	resolver Resolver
}

// NewConsumer builds the consumer.
func NewConsumer(config ConsumerConfig) Consumer {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return Consumer{config: config, logger: logger, resolver: Resolver{
		Service: config.Service, Reader: config.Reader, Logger: logger, DefaultProfileID: config.DefaultProfileID,
		Categories: config.Categories, Locks: &keyedlock.Locks{},
	}}
}

var _ scan.Consumer = Consumer{}

// Observe implements scan.Consumer.
func (c Consumer) Observe(ctx context.Context, root settings.RootFolder, categoryID int64, batch []scan.Observed) error {
	now := c.config.Clock.Now()
	readable := make([]scan.Observed, 0, len(batch))
	for _, observed := range batch {
		if observed.Probe.Err == nil {
			readable = append(readable, observed)
		}
	}
	if len(readable) == 0 {
		return nil
	}
	files := make([]library.PhysicalFile, len(readable))
	for i, observed := range readable {
		hash, err := library.QuickHash(c.config.FS, path.Join(root.Path, observed.File.RelPath), observed.File.SizeBytes)
		if err != nil {
			return fmt.Errorf("movie: %w", err)
		}
		files[i] = library.PhysicalFile{RelPath: observed.File.RelPath, SizeBytes: observed.File.SizeBytes, QuickHash: hash, FirstSeenAt: now}
	}
	registered, err := c.config.Physical.RegisterBatch(ctx, root.ID, files)
	if err != nil {
		return fmt.Errorf("movie: %w", err)
	}

	byWork := map[int64][]int64{}
	var order []int64
	var queued []library.QueueEntry
	for i, observed := range readable {
		info, ok := parsemovie.ParsePath(observed.File.RelPath)
		if !ok {
			queued = append(queued, c.queueEntry(registered[i].MediaObjectID, root.ID, observed.File.RelPath, nil, now.Unix()))
			continue
		}
		named := NFOIDs(c.config.FS, path.Join(root.Path, observed.File.RelPath))
		workID, candidates := c.resolver.Resolve(ctx, categoryID, info, named)
		if folderInfo, found := parsemovie.ParseFolderOf(observed.File.RelPath); workID == 0 && found && folderInfo.Title != "" && (folderInfo.Year != info.Year || folderInfo.Title != info.Title) {
			// A file named after another year or spelling than its folder ("The Xbsebbxog Zxkuketjaz (2025)" in "... (2018)").
			if foundID, more := c.resolver.Resolve(ctx, categoryID, folderInfo, named); foundID != 0 {
				workID = foundID
			} else if len(candidates) == 0 {
				candidates = more
			}
		}
		switch workID {
		case ExcludedMovie:
			continue
		case 0:
			queued = append(queued, c.queueEntry(registered[i].MediaObjectID, root.ID, observed.File.RelPath, candidates, now.Unix()))
			continue
		}
		if byWork[workID] == nil {
			order = append(order, workID)
		}
		byWork[workID] = append(byWork[workID], registered[i].MediaObjectID)
	}

	attached := 0
	for _, workID := range order {
		placed := byWork[workID]
		count, err := c.config.Service.Series.AttachWholeFiles(ctx, now, workID, root.ID, placed)
		if err != nil {
			return fmt.Errorf("movie: %w", err)
		}
		attached += count
		if err := c.config.Unidentified.Clear(ctx, placed); err != nil {
			return fmt.Errorf("movie: %w", err)
		}
	}
	if err := c.config.Unidentified.Record(ctx, queued); err != nil {
		return fmt.Errorf("movie: %w", err)
	}
	if err := c.config.Unidentified.ClearPlaced(ctx); err != nil {
		return fmt.Errorf("movie: %w", err)
	}
	c.logger.Info("movie scan batch recorded", slog.Int64("root_folder_id", root.ID), slog.Int("files", len(readable)),
		slog.Int("movies_attached", attached), slog.Int("queued", len(queued)))
	return nil
}

func (c Consumer) queueEntry(mediaObjectID, rootID int64, relPath string, candidates []model.Identification, at int64) library.QueueEntry {
	return library.QueueEntry{MediaObjectID: mediaObjectID, RootFolderID: rootID, RelPath: relPath, Candidates: candidates, At: at}
}
