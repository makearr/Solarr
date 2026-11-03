package movie

import (
	"context"
	"log/slog"
	"path"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/parsemovie"
	"github.com/makearr/Solarr/internal/media/works"
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
	// Threshold is import.auto_commit_confidence; nil takes its default.
	Threshold        func() float64
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
	works.Consumer
}

// scanning is the movie's part of works.Consumer.
type scanning struct {
	fs       fsx.FS
	resolver Resolver
}

// NewConsumer builds the consumer.
func NewConsumer(config ConsumerConfig) Consumer {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	resolver := Resolver{
		Service: config.Service, Reader: config.Reader, Logger: logger, DefaultProfileID: config.DefaultProfileID,
		Categories: config.Categories, Locks: &keyedlock.Locks{}, Threshold: config.Threshold,
	}
	return Consumer{works.NewConsumer(works.ConsumerConfig{
		MediaType: MediaType, Plural: "movies", OSHash: true, Resolver: scanning{fs: config.FS, resolver: resolver},
		FS: config.FS, Physical: config.Physical, Unidentified: config.Unidentified, Series: config.Service.Series,
		Clock: config.Clock, Logger: logger,
	})}
}

var _ scan.Consumer = Consumer{}

// Readable implements works.ScanResolver: a file ffprobe could not read is no film.
func (scanning) Readable(observed scan.Observed) bool { return observed.Probe.Err == nil }

// Resolve implements works.ScanResolver: from the file's title and year, the ids an nfo beside it names, and its folder's.
func (s scanning) Resolve(ctx context.Context, root settings.RootFolder, categoryID int64, observed []scan.Observed, _ []library.PhysicalFile) []works.ScanMatch {
	matches := make([]works.ScanMatch, len(observed))
	for i, file := range observed {
		info, ok := parsemovie.ParsePath(file.File.RelPath)
		if !ok {
			continue
		}
		named := NFOIDs(s.fs, path.Join(root.Path, file.File.RelPath))
		workID, candidates := s.resolver.Resolve(ctx, categoryID, info, named)
		if folderInfo, found := parsemovie.ParseFolderOf(file.File.RelPath); workID == 0 && found && folderInfo.Title != "" && (folderInfo.Year != info.Year || folderInfo.Title != info.Title) {
			// A file named after another year or spelling than its folder ("The Xbsebbxog Zxkuketjaz (2025)" in "... (2018)").
			if foundID, more := s.resolver.Resolve(ctx, categoryID, folderInfo, named); foundID != 0 {
				workID = foundID
			} else if len(candidates) == 0 {
				candidates = more
			}
		}
		matches[i] = works.ScanMatch{WorkID: workID, Candidates: candidates}
	}
	return matches
}
