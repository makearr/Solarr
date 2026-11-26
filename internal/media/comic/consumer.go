package comic

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/comicinfo"
	"github.com/makearr/Solarr/internal/identify/parsecomic"
	"github.com/makearr/Solarr/internal/media/works"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

// maxRange bounds a file naming a range ("001-006"): a range past it is a mistake, not a hundred parts.
const maxRange = 500

// Extensions are the files of a comic library.
var Extensions = []string{"cbz", "cbr", "cb7", "cbt", "pdf"}

// Includes says a library file is one of the type's: a comic archive, not a macOS resource fork.
func Includes(relPath string) bool {
	return slices.Contains(Extensions, strings.ToLower(strings.TrimPrefix(path.Ext(relPath), "."))) && !strings.HasPrefix(path.Base(relPath), "._")
}

// ConsumerConfig is what a Consumer needs.
type ConsumerConfig struct {
	Resolver     Resolver
	FS           fsx.FS
	Physical     library.Physical
	Unidentified library.Unidentified
	Service      Service
	Clock        clock.Clock
	Logger       *slog.Logger
}

// Consumer is the comic module's scan.Consumer: it registers what a sweep found, reads each file's page facts and
// embedded metadata, finds or adds the series, and records which issues or volumes each file covers. What it cannot place
// goes to the same Review queue every type uses.
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

// observedFile is a registered file with what reading it gave.
type observedFile struct {
	scan.Observed
	registered library.RegisteredFile
	facts      Facts
	readable   bool
}

// Observe implements scan.Consumer.
func (c Consumer) Observe(ctx context.Context, root settings.RootFolder, categoryID int64, batch []scan.Observed) error {
	now := c.config.Clock.Now()
	files := make([]library.PhysicalFile, 0, len(batch))
	kept := make([]scan.Observed, 0, len(batch))
	for _, observed := range batch {
		hash, oshash, err := library.Hashes(c.config.FS, path.Join(root.Path, observed.File.RelPath), observed.File.SizeBytes, false)
		if library.FileVanished(err) {
			c.logger.Debug("file gone or shorter than scanned; left for the next scan", slog.String("path", observed.File.RelPath), slog.Any("error", err))
			continue
		}
		if err != nil {
			return fmt.Errorf("comic: %w", err)
		}
		kept = append(kept, observed)
		files = append(files, library.PhysicalFile{RelPath: observed.File.RelPath, SizeBytes: observed.File.SizeBytes, QuickHash: hash, OSHash: oshash, FirstSeenAt: now})
	}
	if len(kept) == 0 {
		return nil
	}
	registered, err := c.config.Physical.RegisterBatch(ctx, root.ID, files)
	if err != nil {
		return fmt.Errorf("comic: %w", err)
	}
	manga := isMangaKind(c.config.Service.kind(categoryID))
	byGroup := map[string][]observedFile{}
	for i, observed := range kept {
		file := observedFile{Observed: observed, registered: registered[i]}
		facts, err := Probe(ctx, c.config.FS, path.Join(root.Path, observed.File.RelPath))
		markDefect(ctx, c.config.Physical, c.logger, file.registered.MediaObjectID, observed.File.SizeBytes, path.Ext(observed.File.RelPath), facts, err, now)
		if err != nil {
			c.logger.Warn("comic file not readable; imported unidentified", slog.String("path", observed.File.RelPath), slog.Any("error", err))
		} else {
			file.facts, file.readable = facts, true
		}
		group := groupOf(file)
		byGroup[group] = append(byGroup[group], file)
	}

	var queued []library.QueueEntry
	attached := 0
	for _, group := range byGroup {
		first := group[0]
		series, candidates := c.config.Resolver.Resolve(ctx, categoryID, evidenceOf(first))
		if series == works.Excluded {
			continue
		}
		var links []library.SeriesFileLink
		var leftover []observedFile
		for _, file := range group {
			keys := PartKeys(claimsOf(file), manga)
			if series <= 0 || len(keys) == 0 {
				leftover = append(leftover, file)
				continue
			}
			if manga {
				c.config.Service.EnsureParts(ctx, categoryID, series, keys)
			}
			links = append(links, library.SeriesFileLink{MediaObjectID: file.registered.MediaObjectID, Parts: keys})
		}
		if len(links) > 0 {
			result, err := c.config.Service.Series.AttachFiles(ctx, now, series, root.ID, links)
			if err != nil {
				return fmt.Errorf("comic: %w", err)
			}
			attached += result.Attached
			placed := make([]int64, 0, len(links))
			for _, link := range links {
				if !slices.Contains(result.Unresolved, link.MediaObjectID) {
					placed = append(placed, link.MediaObjectID)
				}
			}
			if err := c.config.Unidentified.Clear(ctx, placed); err != nil {
				return fmt.Errorf("comic: %w", err)
			}
			for _, file := range group {
				if slices.Contains(result.Unresolved, file.registered.MediaObjectID) {
					leftover = append(leftover, file)
				}
			}
			c.recordFacts(ctx, group, placed, now.Unix())
		}
		for _, file := range leftover {
			queued = append(queued, library.QueueEntry{
				MediaObjectID: file.registered.MediaObjectID, RootFolderID: root.ID, RelPath: file.File.RelPath, Candidates: candidates, At: now.Unix(),
			})
		}
	}
	if err := c.config.Unidentified.Record(ctx, queued); err != nil {
		return fmt.Errorf("comic: %w", err)
	}
	if err := c.config.Unidentified.ClearPlaced(ctx); err != nil {
		return fmt.Errorf("comic: %w", err)
	}
	c.logger.Info("comic scan batch recorded", slog.Int64("root_folder_id", root.ID), slog.Int("files", len(kept)),
		slog.Int("parts_attached", attached), slog.Int("queued", len(queued)))
	return nil
}

// recordFacts stores the page facts of the files that were placed: the median page edges as the file's size, the page
// count and the commonest image format, where the quality step reads them.
func (c Consumer) recordFacts(ctx context.Context, group []observedFile, placed []int64, at int64) {
	for _, file := range group {
		if !file.readable || !slices.Contains(placed, file.registered.MediaObjectID) {
			continue
		}
		recordPageFacts(ctx, c.config.Physical, file.registered.MediaObjectID, path.Ext(file.File.RelPath), file.facts, at)
	}
}

// groupOf groups a batch by the series its files name, a series folder keeping its files together.
func groupOf(file observedFile) string {
	ev := evidenceOf(file)
	key := titleKey(ev.Series)
	if key == "" {
		key = "/" + file.File.RelPath
	}
	return key + "|" + ev.IDs[ProviderComicVine] + ev.IDs[ProviderMetron] + "|" + strconv.Itoa(ev.Year)
}

// claimsOf are the file's claims, strongest first: its embedded metadata, its name, its folder, its parent folder.
func claimsOf(file observedFile) []parsecomic.Claim {
	return claimsOfPath(file.File.RelPath, file.facts)
}

func claimsOfPath(rel string, facts Facts) []parsecomic.Claim {
	var claims []parsecomic.Claim
	if info := embedded(facts); info != nil {
		claims = append(claims, claimOfInfo(*info))
	}
	claims = append(claims, parsecomic.Parse(path.Base(rel)))
	if folder := path.Dir(rel); folder != "." {
		claims = append(claims, parsecomic.Parse(path.Base(folder)))
		if parent := path.Dir(folder); parent != "." {
			claims = append(claims, parsecomic.Parse(path.Base(parent)))
		}
	}
	return claims
}

func evidenceOf(file observedFile) Evidence { return evidenceOfPath(file.File.RelPath, file.facts) }

func evidenceOfPath(rel string, facts Facts) Evidence {
	// The file's own name first: a folder named after the series only fills what the file's name lacks.
	ev := EvidenceOf(embedded(facts), claimsOfPath(rel, facts)...)
	if ev.Series == "" {
		ev.Series = path.Base(path.Dir(rel))
	}
	return ev
}

// embedded is the file's own metadata: MetronInfo, which carries ids, before ComicInfo.
func embedded(facts Facts) *comicinfo.Info {
	if facts.MetronInfo != nil {
		return facts.MetronInfo
	}
	return facts.ComicInfo
}

func claimOfInfo(info comicinfo.Info) parsecomic.Claim {
	claim := parsecomic.Claim{Series: info.Series, IssueNumber: info.Number, Publisher: info.Publisher}
	if info.Manga == "YesAndRightToLeft" || info.MangaFlag {
		claim.Volume = info.Volume
	}
	return claim
}

// PartKeys are the parts a file's claims name: the issue or issues, the volume or volumes, or the chapter, which a
// volume holds when a provider listed it there and which stands alone otherwise.
func PartKeys(claims []parsecomic.Claim, manga bool) []library.PartKey {
	for _, claim := range claims {
		if keys := partKeysOf(claim, manga); len(keys) > 0 {
			return keys
		}
	}
	return nil
}

func partKeysOf(claim parsecomic.Claim, manga bool) []library.PartKey {
	if manga {
		switch {
		case claim.Volume > 0 && claim.Chapter == "":
			var keys []library.PartKey
			for number := claim.Volume; number <= max(claim.Volume, claim.VolumeEnd) && number-claim.Volume < maxRange; number++ {
				keys = append(keys, library.PartKey{MediaRef: PartVolume + ":" + strconv.Itoa(number)})
			}
			return keys
		case claim.Chapter != "":
			var keys []library.PartKey
			for _, number := range numbersBetween(claim.Chapter, claim.ChapterEnd) {
				keys = append(keys, library.PartKey{MediaRef: PartChapter + ":" + number, AltScheme: AltSchemeChapter, AltRef: number})
			}
			return keys
		}
		return nil
	}
	kind := PartIssue
	switch claim.Format {
	case "annual":
		kind = PartAnnual
	case "special":
		kind = PartSpecial
	}
	switch {
	case claim.IssueNumber != "":
		var keys []library.PartKey
		for _, number := range numbersBetween(claim.IssueNumber, claim.IssueEnd) {
			keys = append(keys, library.PartKey{MediaRef: kind + ":" + number})
		}
		return keys
	case claim.Format == "one-shot" || claim.Format == "tpb" || claim.Format == "hc" || claim.Format == "gn" || claim.Format == "omnibus":
		return []library.PartKey{{MediaRef: PartIssue + ":1"}}
	}
	return nil
}

// numbersBetween lists a printed range's whole numbers; a single number, or one that is not whole, stands for itself.
func numbersBetween(from, to string) []string {
	first, errFirst := strconv.Atoi(from)
	last, errLast := strconv.Atoi(to)
	if to == "" || errFirst != nil || errLast != nil || last <= first || last-first >= maxRange {
		return []string{from}
	}
	numbers := make([]string, 0, last-first+1)
	for number := first; number <= last; number++ {
		numbers = append(numbers, strconv.Itoa(number))
	}
	return numbers
}
