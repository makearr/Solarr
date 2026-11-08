package adult

import (
	"context"
	"log/slog"
	"sync"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/parseadult"
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

// Consumer is the adult module's scan.Consumer: it registers what a sweep found with its oshash, reads each file's
// studio, title and day from its path, finds or adds the scene it is (by fingerprint first), and records the file as
// that scene's. What it cannot place goes to the same Review queue every type uses.
type Consumer struct {
	works.Consumer
}

// scanning is the scene's part of works.Consumer.
type scanning struct {
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
		MediaType: MediaType, Plural: "scenes", OSHash: true, Resolver: scanning{resolver: resolver},
		FS: config.FS, Physical: config.Physical, Unidentified: config.Unidentified, Series: config.Service.Series,
		Clock: config.Clock, Logger: logger,
	})}
}

var _ scan.Consumer = Consumer{}

// ScanGather is how many files of a library's one-scene folders a scan hands over together: one fingerprint query's worth.
const ScanGather = fingerprintsPerQuery

// Readable implements works.ScanResolver: a file ffprobe could not read is no scene.
func (scanning) Readable(observed scan.Observed) bool { return observed.Probe.Err == nil }

// Resolve implements works.ScanResolver: by the batch's fingerprints first, then the file's studio, title and day.
func (s scanning) Resolve(ctx context.Context, _ settings.RootFolder, categoryID int64, observed []scan.Observed, files []library.PhysicalFile) []works.ScanMatch {
	hashes := make([]string, len(files))
	for i, file := range files {
		hashes[i] = file.OSHash
	}
	known := s.resolver.Service.ByFingerprints(ctx, categoryID, hashes)
	matches := make([]works.ScanMatch, len(observed))
	// A file waits on the providers far longer than it works, so a batch's files are placed side by side; each provider's own bucket sets the pace.
	var placing sync.WaitGroup
	slots := make(chan struct{}, resolveTogether)
	for i, file := range observed {
		info, ok := parseadult.ParsePath(file.File.RelPath)
		if !ok && files[i].OSHash == "" {
			continue
		}
		info.DurationSec = int(file.Probe.Measured.Duration.Seconds())
		placing.Add(1)
		slots <- struct{}{}
		go func() {
			defer func() { <-slots; placing.Done() }()
			matches[i].WorkID, matches[i].Candidates = s.resolver.ResolveKnown(ctx, categoryID, info, files[i].OSHash, known)
		}()
	}
	placing.Wait()
	return matches
}

// resolveTogether is how many files of a batch are placed at once.
const resolveTogether = 8
