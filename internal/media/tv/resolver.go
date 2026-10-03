package tv

import (
	"context"
	"log/slog"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/parsetv"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// Resolver finds the series a folder or file name is: a {tvdb-N} id, a title or alias of a series the library holds,
// or a single exact-title hit at the providers, which it adds (Sonarr's Existing monitoring).
type Resolver struct {
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

// ExcludedSeries is what Resolve answers for a series the user deleted and asked not to see again: not a place to put
// the file, and not something to queue for review either.
const ExcludedSeries int64 = -1

// Resolve finds the series a folder is, adding it from the providers when it is new and unambiguous. Zero means
// it could not be placed; the candidates then say what was considered.
func (c Resolver) Resolve(ctx context.Context, categoryID int64, folder, relPath string) (int64, []model.Identification) {
	name := folder
	if name == "" {
		if info, ok := parsetv.Parse(path.Base(relPath), c.Clock.Now()); ok {
			name = info.SeriesTitle
		}
	}
	// A download folder is a release name, not a series folder: its own title is what the series is called.
	if info, ok := parsetv.Parse(name, c.Clock.Now()); ok && info.SeriesTitle != "" {
		if info.Year > 0 {
			name = info.TitleWithoutYear + " (" + strconv.Itoa(info.Year) + ")"
		} else {
			name = info.SeriesTitle
		}
	}
	guess := GuessFolder(name)
	if guess.Title == "" && guess.TVDBID == 0 {
		return 0, nil
	}

	if guess.TVDBID > 0 {
		if id, held, err := c.Service.Series.WorkByProviderID(ctx, ProviderTVDB, strconv.FormatInt(guess.TVDBID, 10)); err == nil && held {
			return id, nil
		}
		return c.add(ctx, categoryID, IDs{TVDB: guess.TVDBID}), nil
	}

	if id, found := c.inLibrary(ctx, categoryID, guess); found {
		return id, nil
	}
	candidates, err := c.Service.Search(ctx, guess.Title, 10)
	if err != nil {
		c.Logger.Warn("tv series lookup failed", slog.String("folder", folder), slog.Any("error", err))
		return 0, nil
	}
	if pick, ok := pickCandidate(candidates, guess); ok {
		if id := c.add(ctx, categoryID, IDs{TVDB: pick.TVDBID}); id != 0 {
			return id, nil
		}
	}
	return 0, identifications(candidates)
}

func (c Resolver) inLibrary(ctx context.Context, categoryID int64, guess FolderGuess) (int64, bool) {
	titles, err := c.Reader.Titles(ctx, categoryID)
	if err != nil {
		c.Logger.Warn("tv series titles unreadable", slog.Any("error", err))
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

func (c Resolver) add(ctx context.Context, categoryID int64, ids IDs) int64 {
	if c.Service.IsExcluded(ctx, ids) {
		return ExcludedSeries
	}
	profile := c.DefaultProfileID
	if category, ok := c.Categories.Category(categoryID); ok {
		profile = c.Categories.ProfileFor(category, profile)
	}
	monitor := MonitorExisting
	if category, ok := c.Categories.Category(categoryID); ok {
		monitor = scanMonitor(category.TVMonitor)
	}
	result, err := c.Service.Add(ctx, AddRequest{IDs: ids, CategoryID: categoryID, ProfileID: profile, Monitor: monitor})
	if err != nil {
		c.Logger.Warn("tv series could not be added", slog.Int64("tvdb", ids.TVDB), slog.Any("error", err))
		return 0
	}
	return result.WorkID
}

// scanMonitor is the monitoring a scan gives a series it finds, from the library's own choice.
func scanMonitor(policy string) MonitorOption {
	switch policy {
	case settings.TVMonitorAll:
		return MonitorAll
	case settings.TVMonitorNone:
		return MonitorNone
	default:
		return MonitorExisting
	}
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
