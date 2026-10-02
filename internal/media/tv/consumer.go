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
	"github.com/makearr/Solarr/internal/identify/model"
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

// FolderGuess is what a series folder's name says about the series.
type FolderGuess struct {
	Title  string
	Year   int
	TVDBID int64
}

var (
	idInName   = regexp.MustCompile(`(?i)[\[{(]\s*tvdb(?:id)?[-= ]?(\d+)\s*[\]})]`)
	yearInName = regexp.MustCompile(`\s*[\[(]((?:19|20)\d{2})[\])]\s*$`)
	bracketIDs = regexp.MustCompile(`(?i)\s*[\[{(]\s*(?:tvdb|tmdb|imdb|tvmaze)(?:id)?[-= ]?[\w]+\s*[\]})]`)
)

// GuessFolder reads a series folder name: the title, a trailing year and an embedded TheTVDB id ({tvdb-123}).
func GuessFolder(name string) FolderGuess {
	guess := FolderGuess{}
	if m := idInName.FindStringSubmatch(name); m != nil {
		guess.TVDBID, _ = strconv.ParseInt(m[1], 10, 64)
	}
	title := strings.TrimSpace(bracketIDs.ReplaceAllString(name, ""))
	if m := yearInName.FindStringSubmatch(title); m != nil {
		guess.Year, _ = strconv.Atoi(m[1])
		title = strings.TrimSpace(yearInName.ReplaceAllString(title, ""))
	}
	guess.Title = title
	return guess
}

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
		series, candidates := c.resolveSeries(ctx, categoryID, folder, readable[indexes[0]].File.RelPath)
		var links []library.SeriesFileLink
		var leftover []int
		for _, i := range indexes {
			info, ok := parsetv.ParsePath(readable[i].File.RelPath, now)
			keys := partKeys(info)
			if series == 0 || !ok || len(keys) == 0 {
				leftover = append(leftover, i)
				continue
			}
			links = append(links, library.SeriesFileLink{MediaObjectID: registered[i].MediaObjectID, Parts: keys})
		}
		if len(links) > 0 {
			result, err := c.config.Service.Series.AttachFiles(ctx, now, series, root.ID, links)
			if err != nil {
				return fmt.Errorf("tv: %w", err)
			}
			attached += result.Attached
			matched += len(links) - len(result.Unresolved)
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
	c.logger.Info("tv scan batch recorded", slog.Int64("root_folder_id", root.ID), slog.Int("files", len(readable)),
		slog.Int("episodes_attached", attached), slog.Int("queued", len(queued)))
	return nil
}

// resolveSeries finds the series a folder is, adding it from the providers when it is new and unambiguous. Zero means
// it could not be placed; the candidates then say what was considered.
func (c Consumer) resolveSeries(ctx context.Context, categoryID int64, folder, relPath string) (int64, []model.Identification) {
	name := folder
	if name == "" {
		if info, ok := parsetv.Parse(path.Base(relPath), c.config.Clock.Now()); ok {
			name = info.SeriesTitle
		}
	}
	guess := GuessFolder(name)
	if guess.Title == "" && guess.TVDBID == 0 {
		return 0, nil
	}

	if guess.TVDBID > 0 {
		if id, held, err := c.config.Service.Series.WorkByProviderID(ctx, ProviderTVDB, strconv.FormatInt(guess.TVDBID, 10)); err == nil && held {
			return id, nil
		}
		return c.add(ctx, categoryID, IDs{TVDB: guess.TVDBID}), nil
	}

	if id, found := c.inLibrary(ctx, categoryID, guess); found {
		return id, nil
	}
	candidates, err := c.config.Service.Search(ctx, guess.Title, 10)
	if err != nil {
		c.logger.Warn("tv series lookup failed", slog.String("folder", folder), slog.Any("error", err))
		return 0, nil
	}
	if pick, ok := pickCandidate(candidates, guess); ok {
		if id := c.add(ctx, categoryID, IDs{TVDB: pick.TVDBID}); id != 0 {
			return id, nil
		}
	}
	return 0, identifications(candidates)
}

func (c Consumer) inLibrary(ctx context.Context, categoryID int64, guess FolderGuess) (int64, bool) {
	titles, err := c.config.Reader.Titles(ctx, categoryID)
	if err != nil {
		c.logger.Warn("tv series titles unreadable", slog.Any("error", err))
		return 0, false
	}
	want := parsetv.CleanTitle(guess.Title)
	seen := map[int64]int64{}
	for _, title := range titles {
		if parsetv.CleanTitle(title.Title) == want {
			seen[title.WorkID] = title.Year
		}
	}
	if guess.Year > 0 {
		for id, year := range seen {
			if year != int64(guess.Year) {
				delete(seen, id)
			}
		}
	}
	if len(seen) != 1 {
		return 0, false
	}
	for id := range seen {
		return id, true
	}
	return 0, false
}

func (c Consumer) add(ctx context.Context, categoryID int64, ids IDs) int64 {
	profile := c.config.DefaultProfileID
	if category, ok := c.config.Categories.Category(categoryID); ok {
		profile = c.config.Categories.ProfileFor(category, profile)
	}
	result, err := c.config.Service.Add(ctx, AddRequest{IDs: ids, CategoryID: categoryID, ProfileID: profile, Monitor: MonitorExisting})
	if err != nil {
		c.logger.Warn("tv series could not be added", slog.Int64("tvdb", ids.TVDB), slog.Any("error", err))
		return 0
	}
	return result.WorkID
}

// pickCandidate accepts a search result only when exactly one candidate has the folder's title (and year when the
// folder gives one); anything less is a person's decision.
func pickCandidate(candidates []Candidate, guess FolderGuess) (Candidate, bool) {
	want := parsetv.CleanTitle(guess.Title)
	var exact []Candidate
	for _, candidate := range candidates {
		if parsetv.CleanTitle(candidate.Title) != want {
			continue
		}
		if guess.Year > 0 && candidate.Year != guess.Year {
			continue
		}
		exact = append(exact, candidate)
	}
	if len(exact) != 1 {
		return Candidate{}, false
	}
	return exact[0], true
}

func identifications(candidates []Candidate) []model.Identification {
	out := make([]model.Identification, 0, len(candidates))
	for rank, candidate := range candidates {
		out = append(out, model.Identification{
			Strategy:   "parse",
			ExternalID: &model.ExternalID{Provider: ProviderTVDB, ExternalID: strconv.FormatInt(candidate.TVDBID, 10), Confidence: 1 - 0.05*float64(rank)},
			Precedence: model.PrecedenceSearched,
			Confidence: max(0.1, 0.6-0.05*float64(rank)),
			Evidence:   []model.MatchEvidence{{Kind: "parse", Detail: candidate.Title}},
		})
	}
	return out
}
