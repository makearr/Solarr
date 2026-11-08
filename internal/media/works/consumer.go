package works

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
	"github.com/makearr/Solarr/internal/scanning/scan"
)

// Excluded is what a media type's resolve answers for a work a person removed and asked not to see again: not a place
// to put the file, and not a question for the Review queue either.
const Excluded int64 = -1

// ScanResolver is a media type's part of a scan of a library that holds one work per file or folder.
type ScanResolver interface {
	// Readable says whether a file the sweep found is one to register.
	Readable(observed scan.Observed) bool
	// Resolve answers the work of each registered file, in order: 0 queues it for review with its candidates,
	// Excluded passes it by.
	Resolve(ctx context.Context, root settings.RootFolder, categoryID int64, observed []scan.Observed, files []library.PhysicalFile) []ScanMatch
}

// ScanMatch is the work a scanned file belongs to, or the candidates it might.
type ScanMatch struct {
	WorkID     int64
	Candidates []model.Identification
}

// ConsumerConfig is what a Consumer needs; Logger is optional.
type ConsumerConfig struct {
	// MediaType prefixes errors and what is logged; Plural names the works in the log ("movies").
	MediaType string
	Plural    string
	// OSHash fingerprints each file as it is registered, for a type that finds its works by fingerprint.
	OSHash       bool
	Resolver     ScanResolver
	FS           fsx.FS
	Physical     library.Physical
	Unidentified library.Unidentified
	Series       library.Series
	Clock        clock.Clock
	Logger       *slog.Logger
}

// Consumer is the scan.Consumer of movies, adult scenes, books and audiobooks: it registers what a sweep found, has the
// media type resolve each file's work, records the files as those works' and sends the rest to the Review queue.
type Consumer struct {
	config ConsumerConfig
	logger *slog.Logger
}

// NewConsumer builds the consumer.
func NewConsumer(config ConsumerConfig) Consumer {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return Consumer{config: config, logger: logger}
}

var _ scan.Consumer = Consumer{}

// Observe implements scan.Consumer.
func (c Consumer) Observe(ctx context.Context, root settings.RootFolder, categoryID int64, batch []scan.Observed) error {
	now := c.config.Clock.Now()
	readable := make([]scan.Observed, 0, len(batch))
	for _, observed := range batch {
		if c.config.Resolver.Readable(observed) {
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
			return fmt.Errorf("%s: %w", c.config.MediaType, err)
		}
		files[i] = library.PhysicalFile{RelPath: observed.File.RelPath, SizeBytes: observed.File.SizeBytes, QuickHash: hash, FirstSeenAt: now}
		if c.config.OSHash {
			if files[i].OSHash, err = library.OSHash(c.config.FS, absolute, observed.File.SizeBytes); err != nil {
				return fmt.Errorf("%s: %w", c.config.MediaType, err)
			}
		}
	}
	registered, err := c.config.Physical.RegisterBatch(ctx, root.ID, files)
	if err != nil {
		return fmt.Errorf("%s: %w", c.config.MediaType, err)
	}

	byWork := map[int64][]int64{}
	var order []int64
	var queued []library.QueueEntry
	matches := c.config.Resolver.Resolve(ctx, root, categoryID, readable, files)
	// A slice ending mid-lookup cancels the providers: what they did not answer is not unidentified, the batch is read again.
	if err := ctx.Err(); err != nil {
		return err
	}
	for i, match := range matches {
		switch match.WorkID {
		case Excluded:
			continue
		case 0:
			queued = append(queued, library.QueueEntry{
				MediaObjectID: registered[i].MediaObjectID, RootFolderID: root.ID, RelPath: readable[i].File.RelPath, Candidates: match.Candidates, At: now.Unix(),
			})
			continue
		}
		if byWork[match.WorkID] == nil {
			order = append(order, match.WorkID)
		}
		byWork[match.WorkID] = append(byWork[match.WorkID], registered[i].MediaObjectID)
	}

	attached := 0
	for _, workID := range order {
		placed := byWork[workID]
		count, err := c.config.Series.AttachWholeFiles(ctx, now, workID, root.ID, placed)
		if err != nil {
			return fmt.Errorf("%s: %w", c.config.MediaType, err)
		}
		attached += count
		if err := c.config.Unidentified.Clear(ctx, placed); err != nil {
			return fmt.Errorf("%s: %w", c.config.MediaType, err)
		}
	}
	if err := c.config.Unidentified.Record(ctx, queued); err != nil {
		return fmt.Errorf("%s: %w", c.config.MediaType, err)
	}
	if err := c.config.Unidentified.ClearPlaced(ctx); err != nil {
		return fmt.Errorf("%s: %w", c.config.MediaType, err)
	}
	c.logger.Info(c.config.MediaType+" scan batch recorded", slog.Int64("root_folder_id", root.ID), slog.Int("files", len(readable)),
		slog.Int(c.config.Plural+"_attached", attached), slog.Int("queued", len(queued)))
	return nil
}
