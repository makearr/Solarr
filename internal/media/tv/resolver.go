package tv

import (
	"context"
	"log/slog"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/parsetv"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/keyedlock"
)

// Resolver finds the series a folder or file name is: a {tvdb-N} id, a title or alias of a series the library holds,
// or a single exact-title hit at the providers, which it adds (Sonarr's Existing monitoring).
type Resolver struct {
	Service          Service
	Reader           library.SeriesReader
	Clock            clock.Clock
	Logger           *slog.Logger
	DefaultProfileID int64
	// Locks serialises the folders of one name, so the season directories of a series a scan reads side by side ask
	// the providers once between them; nil means no serialising.
	Locks      *keyedlock.Locks
	Categories interface {
		Category(id int64) (settings.Category, bool)
		ProfileFor(category settings.Category, fallback int64) int64
	}
}

// releaseMarker is what a download's folder name has and a library's series folder does not.
var releaseMarker = regexp.MustCompile(`(?i)(?:^|[\s._-])(?:S\d{1,3}(?:E\d{1,4})?|\d{3,4}[pi]|WEB(?:-?DL|RIP)?|HDTV|BluRay|BDRip|DVDRip|x26[45]|H[. ]?26[45]|HEVC|AAC|DDP?\d|REPACK|PROPER|COMPLETE|NF|AMZN)(?:$|[\s._-])`)

// looksLikeRelease says whether a folder is named like a release rather than a series: "Around the World in 80
// Gardens" has a number in it and is not one.
func looksLikeRelease(name string) bool {
	return releaseMarker.MatchString(name) || (!strings.Contains(name, " ") && strings.Count(name, ".") >= 3)
}

// FolderGuess is what a series folder's name says about the series.
type FolderGuess struct {
	Title  string
	Year   int
	TVDBID int64
	TMDBID int64
	IMDbID string
}

var (
	idInName   = regexp.MustCompile(`(?i)[\[{(]\s*tvdb(?:id)?[-= ]?(\d+)\s*[\]})]`)
	tmdbInName = regexp.MustCompile(`(?i)[\[{(]\s*tmdb(?:id)?[-= ]?(\d+)\s*[\]})]`)
	imdbInName = regexp.MustCompile(`(?i)[\[{(]\s*imdb(?:id)?[-= ]?(tt\d+)\s*[\]})]`)
	yearInName = regexp.MustCompile(`\s*[\[(]((?:19|20)\d{2})[\])]\s*$`)
	bracketIDs = regexp.MustCompile(`(?i)\s*[\[{(]\s*(?:tvdb|tmdb|imdb|tvmaze)(?:id)?[-= ]?[\w]+\s*[\]})]`)
)

// GuessFolder reads a series folder name: the title, a trailing year and an embedded TheTVDB, TMDB or IMDb id
// ({tvdb-123}, [tmdbid-123], {imdb-tt123}), as Sonarr, Jellyfin and Plex write them.
func GuessFolder(name string) FolderGuess {
	guess := FolderGuess{}
	if m := idInName.FindStringSubmatch(name); m != nil {
		guess.TVDBID, _ = strconv.ParseInt(m[1], 10, 64)
	}
	if m := tmdbInName.FindStringSubmatch(name); m != nil {
		guess.TMDBID, _ = strconv.ParseInt(m[1], 10, 64)
	}
	if m := imdbInName.FindStringSubmatch(name); m != nil {
		guess.IMDbID = strings.ToLower(m[1])
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
	return c.ResolveFiles(ctx, categoryID, folder, []string{relPath})
}

// ResolveFiles is Resolve with every file of the folder, whose episode numbers settle a title the providers list twice.
func (c Resolver) ResolveFiles(ctx context.Context, categoryID int64, folder string, relPaths []string) (int64, []model.Identification) {
	return c.ResolveFolder(ctx, categoryID, folder, relPaths, FolderGuess{})
}

// ResolveFolder is ResolveFiles with the ids the folder's own tvshow.nfo names; an id in the folder's name wins over them.
func (c Resolver) ResolveFolder(ctx context.Context, categoryID int64, folder string, relPaths []string, named FolderGuess) (int64, []model.Identification) {
	relPath := ""
	if len(relPaths) > 0 {
		relPath = relPaths[0]
	}
	if c.Locks != nil {
		defer c.Locks.Lock(strconv.FormatInt(categoryID, 10) + "|" + strings.ToLower(folder))()
	}
	name := folder
	if name == "" {
		if info, ok := parsetv.Parse(path.Base(relPath), c.Clock.Now()); ok {
			name = info.SeriesTitle
		}
	}
	// A download folder is a release name, not a series folder: its own title is what the series is called.
	if info, ok := parsetv.Parse(name, c.Clock.Now()); ok && info.SeriesTitle != "" && looksLikeRelease(name) {
		if info.Year > 0 {
			name = info.TitleWithoutYear + " (" + strconv.Itoa(info.Year) + ")"
		} else {
			name = info.SeriesTitle
		}
	}
	guess := GuessFolder(name)
	if guess.TVDBID == 0 && guess.TMDBID == 0 && guess.IMDbID == "" {
		guess.TVDBID, guess.TMDBID, guess.IMDbID = named.TVDBID, named.TMDBID, named.IMDbID
	}
	if guess.Title == "" && guess.TVDBID == 0 && guess.TMDBID == 0 && guess.IMDbID == "" {
		return 0, nil
	}

	if guess.TVDBID > 0 {
		if id, held, err := c.Service.Series.WorkByProviderID(ctx, MediaType, ProviderTVDB, strconv.FormatInt(guess.TVDBID, 10)); err == nil && held {
			return id, nil
		}
		return c.add(ctx, categoryID, IDs{TVDB: guess.TVDBID}), nil
	}
	if id := c.byNamedID(ctx, categoryID, guess); id != 0 { // -1 is an excluded series, kept as such
		return id, nil
	}

	if id, found := c.inLibrary(ctx, categoryID, guess); found {
		return id, nil
	}
	candidates, err := c.search(ctx, guess)
	if err != nil {
		c.Logger.Warn("tv series lookup failed", slog.String("folder", folder), slog.Any("error", err))
		return 0, nil
	}
	if pick, ok := c.pick(ctx, candidates, guess, relPaths); ok {
		if id := c.add(ctx, categoryID, IDs{TVDB: pick.TVDBID, TMDB: pick.TMDBID}); id != 0 {
			return id, nil
		}
	}
	return 0, identifications(candidates)
}

// byNamedID places a folder that names a TMDB or IMDb id: the series held under it, else the one added from it; zero
// falls back to searching by title, as when TMDB is not configured to map an IMDb id.
func (c Resolver) byNamedID(ctx context.Context, categoryID int64, guess FolderGuess) int64 {
	if guess.TMDBID > 0 {
		if id, held, err := c.Service.Series.WorkByProviderID(ctx, MediaType, ProviderTMDB, strconv.FormatInt(guess.TMDBID, 10)); err == nil && held {
			return id
		}
		return c.add(ctx, categoryID, IDs{TMDB: guess.TMDBID})
	}
	if guess.IMDbID != "" {
		if id, held, err := c.Service.Series.WorkByProviderID(ctx, MediaType, ProviderIMDb, guess.IMDbID); err == nil && held {
			return id
		}
		return c.add(ctx, categoryID, IDs{IMDb: guess.IMDbID})
	}
	return 0
}

// searchLimit is how many hits one search keeps; a short list hides the right series behind its namesakes.
const searchLimit = 20

// search asks the providers for the folder's title and, when the folder gives a year, for the title with the year in
// brackets as TheTVDB names such a series: that puts "Forever (2014)" first where "Forever" buries it.
func (c Resolver) search(ctx context.Context, guess FolderGuess) ([]Candidate, error) {
	found, err := c.Service.Search(ctx, guess.Title, searchLimit)
	asked := 1
	failed := 0
	if err != nil {
		failed++
	}
	merge := func(term string) {
		more, moreErr := c.Service.Search(ctx, term, searchLimit)
		asked++
		if moreErr != nil {
			failed++
		}
		for _, candidate := range more {
			if !slices.ContainsFunc(found, func(have Candidate) bool {
				return (candidate.TVDBID != 0 && have.TVDBID == candidate.TVDBID) || (candidate.TVDBID == 0 && have.TMDBID == candidate.TMDBID)
			}) {
				found = append(found, candidate)
			}
		}
	}
	if guess.Year > 0 {
		merge(guess.Title + " (" + strconv.Itoa(guess.Year) + ")")
	}
	// Sonarr writes "Title - Subtitle" for TheTVDB's "Title: Subtitle".
	if len(titleMatches(found, guess)) == 0 && strings.Contains(guess.Title, " - ") {
		merge(strings.ReplaceAll(guess.Title, " - ", ": "))
	}
	if failed == asked {
		return nil, err
	}
	return found, nil
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

// pick chooses the candidate a folder is: the one with its title (and year), or, when several have it or none does, the
// one whose episodes the folder's files fit. Anything less clear is a person's decision.
func (c Resolver) pick(ctx context.Context, candidates []Candidate, guess FolderGuess, relPaths []string) (Candidate, bool) {
	matches := titleMatches(candidates, guess)
	if len(matches) == 1 {
		return matches[0], true
	}
	if len(matches) == 0 {
		matches = nearTitles(candidates, guess)
	}
	if len(matches) == 0 && countNumbered(relPaths, c.Clock.Now()) >= episodesToTrustTheNumbers {
		// A folder named in another language still has its episode numbers: the top hits are judged by those alone.
		matches = candidates[:min(len(candidates), maxEpisodeJudged)]
	}
	return c.byEpisodes(ctx, matches, relPaths, c.Clock.Now())
}

// titleMatches are the candidates named like the folder, ignoring TheTVDB's "(2014)" or "(US)" qualifier; a year the
// folder gives must be the candidate's, unless nothing has it and a candidate states none.
func titleMatches(candidates []Candidate, guess FolderGuess) []Candidate {
	want := parsetv.CleanTitle(guess.Title)
	var exact, yearless []Candidate
	for _, candidate := range candidates {
		if !hasTitle(candidate, want) {
			continue
		}
		switch {
		case guess.Year == 0 || yearsAgree(candidate, guess.Year):
			exact = append(exact, candidate)
		case candidate.Year == 0:
			yearless = append(yearless, candidate)
		}
	}
	if len(exact) == 0 {
		return yearless
	}
	return exact
}

// yearsAgree is a candidate first aired within a year of the folder's, or one whose own title carries that year in
// brackets: TheTVDB's first-aired date and a release's year differ by a year more often than they are wrong.
func yearsAgree(candidate Candidate, year int) bool {
	if candidate.Year-year >= -1 && candidate.Year-year <= 1 && candidate.Year != 0 {
		return true
	}
	return strings.Contains(candidate.Title, "("+strconv.Itoa(year)+")")
}

// hasTitle reports whether a title of the candidate, with or without TheTVDB's qualifier, is the wanted one.
func hasTitle(candidate Candidate, want string) bool {
	for _, title := range append([]string{candidate.Title}, candidate.AltTitles...) {
		if parsetv.CleanTitle(title) == want || parsetv.CleanTitle(trailingBrackets.ReplaceAllString(title, "")) == want {
			return true
		}
	}
	return false
}

// nearTitles are the first few candidates whose title starts or ends with the folder's, for the episodes to judge.
func nearTitles(candidates []Candidate, guess FolderGuess) []Candidate {
	want := parsetv.CleanTitle(guess.Title)
	var near []Candidate
	for _, candidate := range candidates {
		have := parsetv.CleanTitle(candidate.Title)
		if len(want) >= 4 && (strings.HasPrefix(have, want) || strings.HasSuffix(have, want)) && (guess.Year == 0 || candidate.Year == guess.Year || candidate.Year == 0) {
			near = append(near, candidate)
		}
		if len(near) == maxEpisodeJudged {
			break
		}
	}
	return near
}

// episodesToTrustTheNumbers is how many numbered files a folder needs for its numbers to name a series by themselves.
const episodesToTrustTheNumbers = 8

func countNumbered(relPaths []string, now time.Time) int {
	count := 0
	for _, relPath := range relPaths {
		if info, ok := parsetv.ParsePath(relPath, now); ok && !info.Special && len(info.EpisodeNumbers) > 0 {
			count++
		}
	}
	return count
}

const (
	// maxEpisodeJudged bounds the series fetched to compare episodes with.
	maxEpisodeJudged = 4
	// episodeFit is the share of a folder's numbered files a series must have the episodes of, and episodeLead how far
	// ahead of the next one it must be.
	episodeFit  = 0.8
	episodeLead = 0.15
)

// byEpisodes picks the candidate whose episode list has the most of the folder's files' numbers.
func (c Resolver) byEpisodes(ctx context.Context, candidates []Candidate, relPaths []string, now time.Time) (Candidate, bool) {
	if len(candidates) == 0 || c.Service.Skyhook == nil {
		return Candidate{}, false
	}
	type numbering struct{ season, episode int }
	titles := map[numbering]string{}
	var seasonal []numbering
	var absolute []int
	for _, relPath := range relPaths {
		info, ok := parsetv.ParsePath(relPath, now)
		if !ok || info.Special || info.SeasonNumber == 0 && len(info.AbsoluteNumbers) == 0 {
			continue
		}
		for _, number := range info.EpisodeNumbers {
			seasonal = append(seasonal, numbering{info.SeasonNumber, number})
			if title := fileEpisodeTitle(relPath); title != "" && len(info.EpisodeNumbers) == 1 {
				titles[numbering{info.SeasonNumber, number}] = title
			}
		}
		if len(info.EpisodeNumbers) == 0 {
			absolute = append(absolute, info.AbsoluteNumbers...)
		}
	}
	total := len(seasonal) + len(absolute)
	if total < 2 {
		return Candidate{}, false
	}
	best, second, bestAt, bestNumbers := -1.0, -1.0, -1, 0.0
	for index, candidate := range candidates {
		if candidate.TVDBID == 0 {
			continue
		}
		show, err := c.Service.Skyhook.Series(ctx, candidate.TVDBID)
		if err != nil {
			continue
		}
		seen := make(map[numbering]bool, len(show.Episodes))
		titleOf := make(map[numbering]string, len(show.Episodes))
		seenAbsolute := map[int]bool{}
		for _, episode := range show.Episodes {
			seen[numbering{episode.SeasonNumber, episode.EpisodeNumber}] = true
			titleOf[numbering{episode.SeasonNumber, episode.EpisodeNumber}] = episodeKey(episode.Title)
			if episode.AbsoluteEpisodeNumber != nil {
				seenAbsolute[*episode.AbsoluteEpisodeNumber] = true
			}
		}
		fits := 0
		for _, n := range seasonal {
			if seen[n] {
				fits++
			}
		}
		for _, n := range absolute {
			if seenAbsolute[n] {
				fits++
			}
		}
		numbersFit := float64(fits) / float64(total)
		fit := numbersFit
		if len(titles) >= 2 {
			// The names of the episodes break a tie the numbers cannot: two series both have an S01E01.
			same := 0
			for number, title := range titles {
				if titleOf[number] == title {
					same++
				}
			}
			fit = (numbersFit + float64(same)/float64(len(titles))) / 2
		}
		switch {
		case fit > best:
			best, second, bestAt, bestNumbers = fit, best, index, numbersFit
		case fit > second:
			second = fit
		}
	}
	if bestAt < 0 || bestNumbers < episodeFit || best-max(second, 0) < episodeLead {
		return Candidate{}, false
	}
	return candidates[bestAt], true
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

var fileTitle = regexp.MustCompile(`(?i)s\d{1,3}e\d{1,3}\s*-\s*(.+?)(?:\.[a-z0-9]{2,4})?$`)

// fileEpisodeTitle is the episode title a Sonarr-style file name carries after its numbering, normalised.
func fileEpisodeTitle(relPath string) string {
	if m := fileTitle.FindStringSubmatch(path.Base(relPath)); m != nil {
		return episodeKey(m[1])
	}
	return ""
}

func episodeKey(title string) string { return parsetv.CleanTitle(parsetv.NormalizeEpisodeTitle(title)) }
