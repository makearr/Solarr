package tv

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/parsetv"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

// VideoExtensions are the containers a TV scan reads.
var VideoExtensions = []string{".mkv", ".mp4", ".m4v", ".avi", ".ts", ".m2ts", ".mov", ".wmv", ".webm", ".mpg", ".mpeg", ".flv", ".ogm", ".iso"}

// IsVideo reports a path worth probing as an episode: a video file that is not a sample or an extra.
func IsVideo(relPath string) bool {
	lowered := strings.ToLower(relPath)
	if !slices.ContainsFunc(VideoExtensions, func(extension string) bool { return strings.HasSuffix(lowered, extension) }) {
		return false
	}
	return !isExtra(lowered)
}

// extraFolders are the folder names Sonarr, Plex and Jellyfin keep non-episode video in.
var extraFolders = map[string]bool{
	"extras": true, "featurettes": true, "behind the scenes": true, "deleted scenes": true, "interviews": true,
	"scenes": true, "shorts": true, "trailers": true, "other": true, "sample": true, "samples": true,
}

func isExtra(lowered string) bool {
	components := strings.Split(lowered, "/")
	for _, folder := range components[:len(components)-1] {
		if extraFolders[folder] {
			return true
		}
	}
	file := components[len(components)-1]
	return sampleName.MatchString(strings.TrimSuffix(file, path.Ext(file)))
}

var sampleName = regexp.MustCompile(`(?:^|[-._ \[(])sample(?:$|[-._ \])])`)

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
	// Profile resolves a category's quality profile, or the default when it has none.
	Categories interface {
		Category(id int64) (settings.Category, bool)
		ProfileFor(category settings.Category, fallback int64) int64
	}
}

// Consumer is the TV module's scan.Consumer: it registers what a sweep found, reads episode numbers from the paths,
// finds or adds the series each folder is, and records which episodes the files cover. What it cannot place goes to
// the same Review queue music uses.
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
		Service: config.Service, Reader: config.Reader, Clock: config.Clock, Logger: logger,
		DefaultProfileID: config.DefaultProfileID, Categories: config.Categories, Locks: &FolderLocks{},
	}}
}

var _ scan.Consumer = Consumer{}

// partKeys turns a parsed name into the episodes it covers: by season and number, by absolute number, or by air date.
func partKeys(info parsetv.Info) []library.PartKey {
	var keys []library.PartKey
	switch {
	case len(info.EpisodeNumbers) > 0:
		season := info.SeasonNumber
		for _, number := range info.EpisodeNumbers {
			keys = append(keys, library.PartKey{MediaRef: MediaRef(season, number)})
		}
	case len(info.AbsoluteNumbers) > 0:
		for _, number := range info.AbsoluteNumbers {
			keys = append(keys, library.PartKey{AltScheme: "absolute", AltRef: strconv.Itoa(number)})
		}
	case info.AirDate != "":
		keys = append(keys, library.PartKey{AltScheme: "date", AltRef: info.AirDate})
	}
	return keys
}

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
			return fmt.Errorf("tv: %w", err)
		}
		files[i] = library.PhysicalFile{RelPath: observed.File.RelPath, SizeBytes: observed.File.SizeBytes, QuickHash: hash, FirstSeenAt: now}
	}
	registered, err := c.config.Physical.RegisterBatch(ctx, root.ID, files)
	if err != nil {
		return fmt.Errorf("tv: %w", err)
	}

	byFolder := map[string][]int{}
	for i, observed := range readable {
		folder, _, _ := strings.Cut(observed.File.RelPath, "/")
		if !strings.Contains(observed.File.RelPath, "/") {
			folder = ""
		}
		byFolder[folder] = append(byFolder[folder], i)
	}

	var queued []library.QueueEntry
	attached, matched := 0, 0
	for folder, indexes := range byFolder {
		relPaths := make([]string, len(indexes))
		for n, i := range indexes {
			relPaths[n] = readable[i].File.RelPath
		}
		series, candidates := c.resolver.ResolveFiles(ctx, categoryID, folder, relPaths)
		if series == ExcludedSeries {
			continue
		}
		keepMonitoring := false
		if category, found := c.config.Categories.Category(categoryID); found {
			keepMonitoring = category.TVMonitor == settings.TVMonitorNone
		}
		var links []library.SeriesFileLink
		var leftover []int
		for _, i := range indexes {
			info, ok := parsetv.ParsePath(readable[i].File.RelPath, now)
			keys := partKeys(info)
			if series == 0 || !ok || len(keys) == 0 {
				leftover = append(leftover, i)
				continue
			}
			links = append(links, library.SeriesFileLink{MediaObjectID: registered[i].MediaObjectID, Parts: keys, KeepMonitoring: keepMonitoring})
		}
		if len(links) > 0 {
			result, err := c.config.Service.Series.AttachFiles(ctx, now, series, root.ID, links)
			if err != nil {
				return fmt.Errorf("tv: %w", err)
			}
			attached += result.Attached
			matched += len(links) - len(result.Unresolved)
			placed := make([]int64, 0, len(links))
			for _, link := range links {
				if !slices.Contains(result.Unresolved, link.MediaObjectID) {
					placed = append(placed, link.MediaObjectID)
				}
			}
			if err := c.config.Unidentified.Clear(ctx, placed); err != nil {
				return fmt.Errorf("tv: %w", err)
			}
			for _, id := range result.Unresolved {
				for _, i := range indexes {
					if registered[i].MediaObjectID == id {
						leftover = append(leftover, i)
					}
				}
			}
		}
		for _, i := range leftover {
			queued = append(queued, library.QueueEntry{
				MediaObjectID: registered[i].MediaObjectID, RootFolderID: root.ID, RelPath: readable[i].File.RelPath,
				Candidates: candidates, At: now.Unix(),
			})
		}
	}
	if err := c.config.Unidentified.Record(ctx, queued); err != nil {
		return fmt.Errorf("tv: %w", err)
	}
	if err := c.config.Unidentified.ClearPlaced(ctx); err != nil {
		return fmt.Errorf("tv: %w", err)
	}
	c.logger.Info("tv scan batch recorded", slog.Int64("root_folder_id", root.ID), slog.Int("files", len(readable)),
		slog.Int("episodes_attached", attached), slog.Int("queued", len(queued)))
	return nil
}
