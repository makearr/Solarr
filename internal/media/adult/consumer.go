package adult

import (
	"context"
	"fmt"
	"log/slog"
	"path"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/parseadult"
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

// Consumer is the adult module's scan.Consumer: it registers what a sweep found with its oshash, reads each file's
// studio, title and day from its path, finds or adds the scene it is (by fingerprint first), and records the file as
// that scene's. What it cannot place goes to the same Review queue every type uses.
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
		absolute := path.Join(root.Path, observed.File.RelPath)
		hash, err := library.QuickHash(c.config.FS, absolute, observed.File.SizeBytes)
		if err != nil {
			return fmt.Errorf("adult: %w", err)
		}
		oshash, err := library.OSHash(c.config.FS, absolute, observed.File.SizeBytes)
		if err != nil {
			return fmt.Errorf("adult: %w", err)
		}
		files[i] = library.PhysicalFile{RelPath: observed.File.RelPath, SizeBytes: observed.File.SizeBytes, QuickHash: hash, OSHash: oshash, FirstSeenAt: now}
	}
	registered, err := c.config.Physical.RegisterBatch(ctx, root.ID, files)
	if err != nil {
		return fmt.Errorf("adult: %w", err)
	}

	byWork := map[int64][]int64{}
	var order []int64
	var queued []library.QueueEntry
	for i, observed := range readable {
		info, ok := parseadult.ParsePath(observed.File.RelPath)
		var workID int64
		var candidates []model.Identification
		if ok || files[i].OSHash != "" {
			workID, candidates = c.resolver.Resolve(ctx, categoryID, info, files[i].OSHash)
		}
		switch workID {
		case Excluded:
			continue
		case 0:
			queued = append(queued, library.QueueEntry{MediaObjectID: registered[i].MediaObjectID, RootFolderID: root.ID, RelPath: observed.File.RelPath, Candidates: candidates, At: now.Unix()})
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
			return fmt.Errorf("adult: %w", err)
		}
		attached += count
		if err := c.config.Unidentified.Clear(ctx, placed); err != nil {
			return fmt.Errorf("adult: %w", err)
		}
	}
	if err := c.config.Unidentified.Record(ctx, queued); err != nil {
		return fmt.Errorf("adult: %w", err)
	}
	if err := c.config.Unidentified.ClearPlaced(ctx); err != nil {
		return fmt.Errorf("adult: %w", err)
	}
	c.logger.Info("adult scan batch recorded", slog.Int64("root_folder_id", root.ID), slog.Int("files", len(readable)),
		slog.Int("scenes_attached", attached), slog.Int("queued", len(queued)))
	return nil
}
