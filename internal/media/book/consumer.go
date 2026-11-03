package book

import (
	"context"
	"log/slog"
	"path"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media/works"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/keyedlock"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

// ConsumerConfig is what a Consumer needs; every field but Logger is required.
type ConsumerConfig struct {
	// Threshold is import.auto_commit_confidence; nil takes its default.
	Threshold        func() float64
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
	works.Consumer
}

// scanning is the book's part of works.Consumer.
type scanning struct {
	format   Format
	resolver Resolver
}

// NewConsumer builds the consumer.
func NewConsumer(config ConsumerConfig) Consumer {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	resolver := Resolver{
		Service: config.Service, Logger: logger, DefaultProfileID: config.DefaultProfileID,
		Categories: config.Categories, Locks: &keyedlock.Locks{}, Threshold: config.Threshold,
	}
	return Consumer{works.NewConsumer(works.ConsumerConfig{
		MediaType: config.Service.Format.MediaType, Plural: "books", Resolver: scanning{format: config.Service.Format, resolver: resolver},
		FS: config.FS, Physical: config.Physical, Unidentified: config.Unidentified, Series: config.Service.Series,
		Clock: config.Clock, Logger: logger,
	})}
}

var _ scan.Consumer = Consumer{}

// Readable implements works.ScanResolver: ffprobe reads no ebook; an audio file it cannot read is broken, as a movie's is.
func (s scanning) Readable(observed scan.Observed) bool {
	return observed.Probe.Err == nil || !s.format.IsAudio()
}

// Resolve implements works.ScanResolver: from each book's author and title, once per audiobook folder, since its
// chapters are one book.
func (s scanning) Resolve(ctx context.Context, _ settings.RootFolder, categoryID int64, observed []scan.Observed, _ []library.PhysicalFile) []works.ScanMatch {
	byUnit := map[string]works.ScanMatch{}
	matches := make([]works.ScanMatch, len(observed))
	for i, file := range observed {
		unit := file.File.RelPath
		if s.format.ByFolder {
			unit = path.Dir(unit)
		}
		found, seen := byUnit[unit]
		if !seen {
			if info, ok := s.format.Parse(file.File.RelPath); ok {
				found.WorkID, found.Candidates = s.resolver.Resolve(ctx, categoryID, info)
			}
			byUnit[unit] = found
		}
		matches[i] = found
	}
	return matches
}
