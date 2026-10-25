package book

import (
	"context"
	"fmt"
	"log/slog"
	"path"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/keyedlock"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

// ConsumerConfig is what a Consumer needs; every field but Logger is required.
type ConsumerConfig struct {
	FS               fsx.FS
	Physical         library.Physical
	Unidentified     library.Unidentified
	Service          Service
	Clock            clock.Clock
	Logger           *slog.Logger
	DefaultProfileID int64
	Categories       Categories
}

// Consumer is the book module's scan.Consumer: it registers what a sweep found, reads each book's author and title from
// its path (an ebook's file, an audiobook's folder), finds or adds the book it is, and records the files as that book's.
// What it cannot place goes to the same Review queue every type uses.
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
		Service: config.Service, Logger: logger, DefaultProfileID: config.DefaultProfileID,
		Categories: config.Categories, Locks: &keyedlock.Locks{},
	}}
}

var _ scan.Consumer = Consumer{}

// Observe implements scan.Consumer.
func (c Consumer) Observe(ctx context.Context, root settings.RootFolder, categoryID int64, batch []scan.Observed) error {
	format := c.config.Service.Format
	now := c.config.Clock.Now()
	// ffprobe reads no ebook; an audio file it cannot read is broken, as a movie's is.
	readable := make([]scan.Observed, 0, len(batch))
	for _, observed := range batch {
		if observed.Probe.Err == nil || !format.IsAudio() {
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
			return fmt.Errorf("book: %w", err)
		}
		files[i] = library.PhysicalFile{RelPath: observed.File.RelPath, SizeBytes: observed.File.SizeBytes, QuickHash: hash, FirstSeenAt: now}
	}
	registered, err := c.config.Physical.RegisterBatch(ctx, root.ID, files)
	if err != nil {
		return fmt.Errorf("book: %w", err)
	}

	type resolved struct {
		workID     int64
		candidates []model.Identification
	}
	// An audiobook's chapters are one book: its folder is resolved once.
	byUnit := map[string]resolved{}
	byWork := map[int64][]int64{}
	var order []int64
	var queued []library.QueueEntry
	for i, observed := range readable {
		unit := observed.File.RelPath
		if format.ByFolder {
			unit = path.Dir(unit)
		}
		found, seen := byUnit[unit]
		if !seen {
			if info, ok := format.Parse(observed.File.RelPath); ok {
				found.workID, found.candidates = c.resolver.Resolve(ctx, categoryID, info)
			}
			byUnit[unit] = found
		}
		switch found.workID {
		case Excluded:
			continue
		case 0:
			queued = append(queued, library.QueueEntry{
				MediaObjectID: registered[i].MediaObjectID, RootFolderID: root.ID, RelPath: observed.File.RelPath, Candidates: found.candidates, At: now.Unix(),
			})
			continue
		}
		if byWork[found.workID] == nil {
			order = append(order, found.workID)
		}
		byWork[found.workID] = append(byWork[found.workID], registered[i].MediaObjectID)
	}

	attached := 0
	for _, workID := range order {
		placed := byWork[workID]
		count, err := c.config.Service.Series.AttachWholeFiles(ctx, now, workID, root.ID, placed)
		if err != nil {
			return fmt.Errorf("book: %w", err)
		}
		attached += count
		if err := c.config.Unidentified.Clear(ctx, placed); err != nil {
			return fmt.Errorf("book: %w", err)
		}
	}
	if err := c.config.Unidentified.Record(ctx, queued); err != nil {
		return fmt.Errorf("book: %w", err)
	}
	if err := c.config.Unidentified.ClearPlaced(ctx); err != nil {
		return fmt.Errorf("book: %w", err)
	}
	c.logger.Info("book scan batch recorded", slog.String("media_type", format.MediaType), slog.Int64("root_folder_id", root.ID),
		slog.Int("files", len(readable)), slog.Int("books_attached", attached), slog.Int("queued", len(queued)))
	return nil
}
