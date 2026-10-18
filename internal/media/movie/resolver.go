package movie

import (
	"context"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/parsemovie"
	"github.com/makearr/Solarr/internal/identify/parsetv"
	"github.com/makearr/Solarr/internal/platform/keyedlock"
)

// ExcludedMovie is what Resolve answers for a movie the user deleted and asked not to see again: not a place to put the
// file, and not something to queue for review either.
const ExcludedMovie int64 = -1

// searchLimit is how many hits one search keeps.
const searchLimit = 10

// Resolver finds the movie a file is: an id in its name or nfo, a title and year of a movie the library holds, or a
// single exact title-and-year hit at the providers, which it adds. Anything less clear is a person's decision.
type Resolver struct {
	Service          Service
	Reader           library.SeriesReader
	Logger           *slog.Logger
	DefaultProfileID int64
	Categories       interface {
		Category(id int64) (settings.Category, bool)
		ProfileFor(category settings.Category, fallback int64) int64
	}
	Locks *keyedlock.Locks
}

// Resolve answers the work a parsed name (and the ids its nfo names) is: 0 when it could not be placed, in which case the
// candidates say what was considered, or ExcludedMovie.
func (r Resolver) Resolve(ctx context.Context, categoryID int64, info parsemovie.Info, named IDs) (int64, []model.Identification) {
	ids := IDs{TMDB: info.TMDB, IMDb: info.IMDb}
	if ids == (IDs{}) {
		ids = named
	}
	if r.Locks != nil {
		key := strconv.FormatInt(categoryID, 10) + "|" + parsetv.CleanTitle(info.Title) + "|" + strconv.Itoa(info.Year)
		if ids != (IDs{}) {
			key = strconv.FormatInt(categoryID, 10) + "|" + strconv.FormatInt(ids.TMDB, 10) + ids.IMDb
		}
		defer r.Locks.Lock(key)()
	}
	if ids != (IDs{}) {
		if id := r.byID(ctx, categoryID, ids); id != 0 {
			return id, nil
		}
	}
	if strings.TrimSpace(info.Title) == "" {
		return 0, nil
	}
	if id, found := r.inLibrary(ctx, categoryID, info); found {
		return id, nil
	}
	candidates, err := r.Service.Search(ctx, info.Title, info.Year, searchLimit)
	if err != nil && len(info.AllTitles) < 2 {
		r.warn("movie lookup failed", info.Title, err)
		return 0, nil
	}
	if len(candidates) == 0 && err == nil && info.Year > 0 {
		// The provider filters by exact year; a film dated a year off by one is only found without it.
		candidates, _ = r.Service.Search(ctx, info.Title, 0, searchLimit)
	}
	for _, alternative := range info.AllTitles[min(1, len(info.AllTitles)):] {
		more, altErr := r.Service.Search(ctx, alternative, info.Year, searchLimit)
		if altErr == nil {
			candidates = append(candidates, more...)
		}
	}
	if pick, ok := pickCandidate(candidates, info); ok {
		if id := r.add(ctx, categoryID, IDs{TMDB: pick.TMDBID, IMDb: pick.IMDbID}); id != 0 {
			return id, nil
		}
	}
	return 0, identifications(candidates)
}

func (r Resolver) warn(message, title string, err error) {
	if r.Logger != nil {
		r.Logger.Warn(message, slog.String("title", title), slog.Any("error", err))
	}
}

// byID places a name that carries a TMDB or IMDb id: the movie held under it, else the one added from it.
func (r Resolver) byID(ctx context.Context, categoryID int64, ids IDs) int64 {
	if ids.TMDB > 0 {
		if id, held, err := r.Service.Series.WorkByProviderID(ctx, MediaType, ProviderTMDB, strconv.FormatInt(ids.TMDB, 10)); err == nil && held {
			return id
		}
	}
	if ids.IMDb != "" {
		if id, held, err := r.Service.Series.WorkByProviderID(ctx, MediaType, ProviderIMDb, ids.IMDb); err == nil && held {
			return id
		}
	}
	return r.add(ctx, categoryID, ids)
}

// inLibrary finds a movie the category holds under this title (or an alias of it) in this year, give or take one: a
// film dated at its festival and at its release is the same film.
func (r Resolver) inLibrary(ctx context.Context, categoryID int64, info parsemovie.Info) (int64, bool) {
	titles, err := r.Reader.TitlesOf(ctx, MediaType, categoryID)
	if err != nil {
		return 0, false
	}
	wanted := map[string]bool{}
	for _, title := range append([]string{info.Title}, info.AllTitles...) {
		wanted[parsetv.CleanTitle(title)] = true
	}
	canonical := map[int64]string{}
	for _, held := range titles {
		if held.Canonical {
			canonical[held.WorkID] = titleKey(held.Title)
		}
	}
	var found []int64
	for _, held := range titles {
		if !wanted[parsetv.CleanTitle(held.Title)] || !yearsAgree(int(held.Year), info.Year) {
			continue
		}
		// An alias that is only part of the film's own title ("Saxewtvei" for Guillermo del Toro's) names another film as well.
		if !held.Canonical && strings.Contains(canonical[held.WorkID], titleKey(held.Title)) {
			continue
		}
		if len(found) == 0 || found[len(found)-1] != held.WorkID {
			found = append(found, held.WorkID)
		}
	}
	if len(found) == 1 {
		return found[0], true
	}
	return 0, false
}

// yearsAgree says two years are the same film's: equal, one apart, or one of them unknown while the other is given.
func yearsAgree(held, named int) bool {
	if held == 0 || named == 0 {
		return named != 0 || held == 0
	}
	diff := held - named
	return diff >= -1 && diff <= 1
}

func (r Resolver) add(ctx context.Context, categoryID int64, ids IDs) int64 {
	if r.Service.IsExcluded(ctx, ids) {
		return ExcludedMovie
	}
	profile := r.DefaultProfileID
	monitor := MonitorMovieOnly
	if category, ok := r.Categories.Category(categoryID); ok {
		profile = r.Categories.ProfileFor(category, profile)
		if category.TVMonitor == settings.TVMonitorNone {
			monitor = MonitorNone
		}
	}
	result, err := r.Service.Add(ctx, AddRequest{IDs: ids, CategoryID: categoryID, ProfileID: profile, Monitor: monitor})
	if err != nil {
		r.warn("movie could not be added", strconv.FormatInt(ids.TMDB, 10)+ids.IMDb, err)
		return 0
	}
	return result.WorkID
}

// rankedWell is how far down a provider's own ranking an exact title-and-year hit may sit and still be taken: two films
// can share a title and a year, and the provider ranks the better known first, as Radarr takes it.
const rankedWell = 3

// pickCandidate chooses the first hit with the named title and a year that agrees, when it is among the provider's top
// few. With no year named it chooses the one hit of that title, if no other hit shares it; with no such hit, the
// provider's first, when its year is exactly the named one and one title is the other's beginning or end.
func pickCandidate(candidates []Candidate, info parsemovie.Info) (Candidate, bool) {
	wanted := map[string]bool{}
	for _, title := range append([]string{info.Title}, info.AllTitles...) {
		wanted[parsetv.CleanTitle(title)], wanted[titleKey(title)] = true, true
	}
	named := func(candidate Candidate) bool {
		return wanted[parsetv.CleanTitle(candidate.Title)] || wanted[titleKey(candidate.Title)]
	}
	if info.Year == 0 {
		var sole Candidate
		same := 0
		for rank, candidate := range candidates {
			if rank < rankedWell && named(candidate) {
				sole, same = candidate, same+1
			}
		}
		if same != 1 {
			return Candidate{}, false
		}
		return sole, true
	}
	// An exact year beats a year off by one, wherever each sits in the first few.
	for _, exactYear := range []bool{true, false} {
		for rank, candidate := range candidates {
			if rank >= rankedWell {
				break
			}
			sameYear := candidate.Year == info.Year
			if named(candidate) && yearsAgree(candidate.Year, info.Year) && sameYear == exactYear {
				return candidate, true
			}
		}
	}
	if len(candidates) > 0 && candidates[0].Year == info.Year && (wordsBound(info.Title, candidates[0].Title) || wordsBound(candidates[0].Title, info.Title)) {
		return candidates[0], true
	}
	return Candidate{}, false
}

// titleKey is a title's letters and digits alone, apostrophes gone ("Straight A's" is "Straight As"): CleanTitle also drops
// articles, which turns "L.A." into "l".
func titleKey(title string) string {
	var key strings.Builder
	for _, r := range strings.ToLower(strings.NewReplacer("&", "and", "'", "", "\u2019", "").Replace(title)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			key.WriteRune(r)
		}
	}
	return key.String()
}

// wordsBound says the shorter title is the longer one's first or last words and has at least two ("Borat Subsequent
// Moviefilm" and its provider title with the full Kazakh subtitle, "Almeeg of Vix 3 Dguzoli Haheix" and "Dguzoli Haheix").
func wordsBound(long, short string) bool {
	isWord := func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }
	longWords, shortWords := strings.FieldsFunc(strings.ToLower(long), isWord), strings.FieldsFunc(strings.ToLower(short), isWord)
	if len(shortWords) < 2 || len(shortWords) >= len(longWords) {
		return false
	}
	return slices.Equal(longWords[:len(shortWords)], shortWords) || slices.Equal(longWords[len(longWords)-len(shortWords):], shortWords)
}

func identifications(candidates []Candidate) []model.Identification {
	out := make([]model.Identification, 0, len(candidates))
	for rank, candidate := range candidates {
		out = append(out, model.Identification{
			Strategy:   "parse",
			ExternalID: &model.ExternalID{Provider: ProviderTMDB, ExternalID: strconv.FormatInt(candidate.TMDBID, 10), Confidence: 1 - 0.05*float64(rank)},
			Precedence: model.PrecedenceSearched,
			Confidence: max(0.1, 0.6-0.05*float64(rank)),
			Evidence:   []model.MatchEvidence{{Kind: "parse", Detail: candidate.Title}},
		})
	}
	return out
}
