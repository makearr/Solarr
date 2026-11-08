package adult

import (
	"context"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/parseadult"
	"github.com/makearr/Solarr/internal/media/works"
	"github.com/makearr/Solarr/internal/platform/keyedlock"
)

// Excluded is what Resolve answers for a scene a person removed and asked not to see again: not a place to put the
// file, and not something to queue for review either.
const Excluded = works.Excluded

// rankedWell is how far down a provider's own ranking an exact hit may sit and still be taken.
const rankedWell = 3

// Categories is the library settings a resolver reads.
type Categories interface {
	Category(id int64) (settings.Category, bool)
	ProfileFor(category settings.Category, fallback int64) int64
}

// Resolver finds the scene a file is: a fingerprint a server knows, a title of a scene the library holds, or a single
// exact title hit at the providers, which it adds. Anything less clear is a person's decision.
type Resolver struct {
	Service          Service
	Logger           *slog.Logger
	DefaultProfileID int64
	Categories       Categories
	Locks            *keyedlock.Locks
	// Threshold is import.auto_commit_confidence; nil takes its default.
	Threshold func() float64
}

// Resolve answers the work a parsed name (and the file's oshash, when it has one) is: 0 when it could not be placed,
// in which case the candidates say what was considered, or Excluded.
func (r Resolver) Resolve(ctx context.Context, categoryID int64, info parseadult.Info, oshash string) (int64, []model.Identification) {
	var known map[string]Scene
	if oshash != "" {
		known = r.Service.ByFingerprints(ctx, categoryID, []string{oshash})
	}
	return r.ResolveKnown(ctx, categoryID, info, oshash, known)
}

// ResolveKnown is Resolve with the fingerprints already asked: known holds what the servers said of a batch's hashes.
func (r Resolver) ResolveKnown(ctx context.Context, categoryID int64, info parseadult.Info, oshash string, known map[string]Scene) (int64, []model.Identification) {
	if r.Locks != nil {
		defer r.Locks.Lock(strconv.FormatInt(categoryID, 10) + "|" + NameKey(info.Studio) + "|" + NameKey(info.Title) + "|" + info.Date + "|" + oshash)()
	}
	if scene, found := known[oshash]; found {
		if id := r.ByIDs(ctx, categoryID, scene.IDs, Hint{Title: scene.Title, Studio: scene.Studio, Date: scene.Date}); id != 0 {
			return id, nil
		}
	}
	if strings.TrimSpace(info.Title) == "" {
		return 0, nil
	}
	if id, found := r.inLibrary(ctx, categoryID, info); found {
		return id, nil
	}
	// Stops at the first provider that settles it; a film may need the numbered scenes of a later one, so a movies library asks all.
	settled := func(found []Candidate) bool {
		_, isFilm := filmOf(found, info)
		_, confidence, ok := pickCandidate(found, info)
		return !isFilm && ok && confidence == model.FolderExactMatch && model.AcceptsFolderMatch(confidence, r.Threshold)
	}
	if r.wantsFilms(categoryID) {
		settled = nil
	}
	candidates, err := r.Service.SearchUntil(ctx, categoryID, Query{Title: info.Title, Studio: info.Studio, Date: info.Date, DayOnly: info.Date != "", ScenesOnly: !r.wantsFilms(categoryID)}, searchLimit, settled)
	// The text search finds what a day cannot: a listing a day or two off, or a title the filter does not match.
	if _, _, ok := pickCandidate(candidates, info); err == nil && info.Date != "" && !ok {
		candidates, err = r.Service.SearchUntil(ctx, categoryID, Query{Title: info.Title, Studio: info.Studio, Date: info.Date, ScenesOnly: !r.wantsFilms(categoryID)}, searchLimit, settled)
	}
	if err != nil {
		r.warn("scene lookup failed", info.Title, err)
		return 0, nil
	}
	// A whole film beside its numbered scenes is a person's call, however well one scene's title fits.
	film, isFilm := filmOf(candidates, info)
	if pick, confidence, ok := pickCandidate(candidates, info); !isFilm && ok && model.AcceptsFolderMatch(confidence, r.Threshold) {
		if id := r.add(ctx, categoryID, pick.IDs, Hint{Title: pick.Title, Studio: pick.Studio, Date: pick.Date}, &pick.Listed); id != 0 {
			return id, nil
		}
	}
	if isFilm {
		return 0, append([]model.Identification{film.identification()}, r.identifications(candidates)...)
	}
	return 0, r.identifications(candidates)
}

// kindMovies is the library kind of whole films.
const kindMovies = "movies"

// wantsFilms says a library holds whole films, whose files a single scene's title can seem to name.
func (r Resolver) wantsFilms(categoryID int64) bool {
	if r.Categories == nil {
		return false
	}
	category, found := r.Categories.Category(categoryID)
	return found && category.Kind == kindMovies
}

func (r Resolver) warn(message, title string, err error) {
	if r.Logger != nil {
		r.Logger.Warn(message, slog.String("media_type", MediaType), slog.String("title", title), slog.Any("error", err))
	}
}

// ByIDs places a scene named by its ids: the one held under any of them, else the one added from them.
func (r Resolver) ByIDs(ctx context.Context, categoryID int64, ids IDs, hint Hint) int64 {
	if id, held := r.Service.WorkByIDs(ctx, ids); held {
		return id
	}
	return r.add(ctx, categoryID, ids, hint, nil)
}

// inLibrary finds the one scene the library holds under this title, in a year that agrees when both are known. A file
// with a day is left to the providers: a studio's scenes of one title come day after day, which the titles cannot tell apart.
func (r Resolver) inLibrary(ctx context.Context, categoryID int64, info parseadult.Info) (int64, bool) {
	if info.Date != "" {
		return 0, false
	}
	titles, err := r.Service.Reader.TitlesOf(ctx, MediaType, categoryID)
	if err != nil {
		return 0, false
	}
	wanted := NameKey(info.Title)
	var found []int64
	for _, held := range titles {
		if NameKey(held.Title) != wanted {
			continue
		}
		if info.Year != 0 && held.Year != 0 && int(held.Year) != info.Year {
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

func (r Resolver) add(ctx context.Context, categoryID int64, ids IDs, hint Hint, seed *Scene) int64 {
	if r.Service.IsExcluded(ctx, ids) {
		return Excluded
	}
	profile, monitor := r.addChoices(categoryID)
	result, err := r.Service.Add(ctx, AddRequest{IDs: ids, Hint: hint, CategoryID: categoryID, ProfileID: profile, Monitor: monitor, Quick: true, Seed: seed})
	if err != nil {
		r.warn("scene could not be added", ids.StashDB+ids.ThePornDB+ids.FansDB, err)
		return 0
	}
	return result.WorkID
}

// pickCandidate chooses the one of the provider's top few that is the named scene: the same title and, where both
// know the day, the same day. Two such hits are told apart by the day, and two on one day are one scene listed twice.
func pickCandidate(candidates []Candidate, info parseadult.Info) (Candidate, float64, bool) {
	// The title on the day wherever the provider ranks it: a studio's numbered scenes push the right day past the top.
	// A title that only continues the file's ("Title, Scene #01") is a listing of the same scene, but the plain title is it.
	var onTheDay, plain []Candidate
	for _, candidate := range candidates {
		if info.Date == "" || candidate.Date != info.Date || !titleMatches(candidate.Title, info.Title, true) {
			continue
		}
		onTheDay = append(onTheDay, candidate)
		if titleMatches(candidate.Title, info.Title, false) {
			plain = append(plain, candidate)
		}
	}
	if len(plain) > 0 {
		return ofStudio(plain, info), model.FolderExactMatch, true
	}
	if len(onTheDay) > 0 {
		return ofStudio(onTheDay, info), model.FolderLooseMatch, true
	}
	var named []Candidate
	offByDays := false
	siblingDays := siblingDays(candidates, info)
	for _, candidate := range candidates {
		if candidate.Rank >= rankedWell {
			continue
		}
		near := false
		if info.Date != "" && candidate.Date != "" && info.Date != candidate.Date {
			// A site's date and a file's differ by a day or two across time zones; only an identical title may bridge it,
			// and not when the studio has the same title on other days: the file's own day is then missing, not off.
			if siblingDays || !withinDays(info.Date, candidate.Date, dateToleranceDays) || !titleMatches(candidate.Title, info.Title, false) {
				continue
			}
			near = true
		}
		sameDay := info.Date != "" && info.Date == candidate.Date
		if titleMatches(candidate.Title, info.Title, sameDay) {
			named = append(named, candidate)
			offByDays = offByDays || near
		}
	}
	if len(named) == 1 {
		if offByDays {
			return named[0], model.FolderLooseMatch, true
		}
		return named[0], model.FolderExactMatch, true
	}
	// With no day, the same title in the same year from the same studio is the same listing again.
	for _, candidate := range named {
		if info.Year != 0 && candidate.Year == info.Year && info.Studio != "" && studioKey(candidate.Studio) == studioKey(info.Studio) {
			return candidate, model.FolderLooseMatch, true
		}
	}
	return Candidate{}, 0, false
}

// ofStudio is the one of the same title and day that the file most plainly is: the studio it names, then the performers
// its name ends in, then the length it has, else the provider's best ranked (one scene listed under two sites).
func ofStudio(candidates []Candidate, info parseadult.Info) Candidate {
	best, bestScore := candidates[0], -1
	for _, candidate := range candidates {
		score := 0
		if info.Studio != "" && studioKey(candidate.Studio) == studioKey(info.Studio) {
			score += 1000
		}
		score += 10 * namedIn(candidate.Performers, info.Performer)
		if lengthAgrees(candidate.DurationSec, info.DurationSec) {
			score++
		}
		if score > bestScore {
			best, bestScore = candidate, score
		}
	}
	return best
}

// namedIn counts the performers whose names a file's name holds; a file lists them as one run of words.
func namedIn(performers []string, named string) int {
	words := " " + NameKey(named) + " "
	count := 0
	for _, performer := range performers {
		if key := NameKey(performer); key != "" && strings.Contains(words, " "+key+" ") {
			count++
		}
	}
	return count
}

// lengthAgrees says two lengths in seconds are one scene's: both known and within a few seconds or two percent.
func lengthAgrees(provider, file int) bool {
	if provider <= 0 || file <= 0 {
		return false
	}
	return max(provider-file, file-provider) <= max(3, file/50)
}

// siblingDays says the providers list the file's title on more than one other day, as a studio numbering its scenes alike does.
func siblingDays(candidates []Candidate, info parseadult.Info) bool {
	days := map[string]bool{}
	for _, candidate := range candidates {
		if candidate.Date != "" && titleMatches(candidate.Title, info.Title, false) {
			days[candidate.Date] = true
		}
	}
	return len(days) > 1
}

// studioKey is a studio's name without the apostrophes sites spell differently ("Devil's Film", "Devils Film").
func studioKey(name string) string {
	return NameKey(strings.NewReplacer("'", "", "\u2019", "").Replace(name))
}

// titleMatches says two titles are the same scene's: equal, or on the same day one holding the other ("Title" and
// "Title - Scene 3").
func titleMatches(found, wanted string, sameDay bool) bool {
	foundKey, wantedKey := NameKey(extraPrefix.ReplaceAllString(found, "")), NameKey(extraPrefix.ReplaceAllString(wanted, ""))
	if foundKey == "" || wantedKey == "" {
		return false
	}
	if sameDay && cutShort(wanted) && len(wantedKey) >= minCutTitleKey && strings.HasPrefix(foundKey, wantedKey) {
		return true
	}
	return foundKey == wantedKey || (sameDay && (strings.Contains(foundKey, wantedKey) || strings.Contains(wantedKey, foundKey)))
}

// minCutTitleKey is how much of a cut-short title must be left to name a scene.
const minCutTitleKey = 12

// cutShort says a file's title ends in the ellipsis a file system's name limit leaves.
func cutShort(title string) bool {
	trimmed := strings.TrimSpace(title)
	return strings.HasSuffix(trimmed, "...") || strings.HasSuffix(trimmed, "\u2026")
}

// identifications are the hits a person picks from in Review, the provider's own ranking kept.
func (r Resolver) identifications(candidates []Candidate) []model.Identification {
	out := make([]model.Identification, 0, len(candidates))
	for rank, candidate := range candidates {
		provider, externalID := CandidateID(candidate.IDs)
		if externalID == "" {
			continue
		}
		detail := candidate.Title
		if candidate.Studio != "" {
			detail += " (" + candidate.Studio + ")"
		}
		out = append(out, model.Identification{
			Strategy:   "parse",
			ExternalID: &model.ExternalID{Provider: provider, ExternalID: externalID, Confidence: 1 - 0.05*float64(rank)},
			Precedence: model.PrecedenceSearched,
			Confidence: max(0.1, 0.6-0.05*float64(rank)),
			Evidence:   []model.MatchEvidence{{Kind: "parse", Detail: detail}},
		})
	}
	return out
}

// CandidateID is the one id a found scene is named by to a person and back: StashDB's, else ThePornDB's, else FansDB's.
func CandidateID(ids IDs) (provider, externalID string) {
	switch {
	case ids.StashDB != "":
		return ProviderStashDB, ids.StashDB
	case ids.ThePornDB != "":
		return ProviderThePornDB, ids.ThePornDB
	case ids.FansDB != "":
		return ProviderFansDB, ids.FansDB
	}
	return "", ""
}

// NamedIDs reads a scene a person named by provider and id.
func NamedIDs(provider, externalID string) (IDs, bool) {
	externalID = strings.TrimSpace(externalID)
	switch provider {
	case ProviderStashDB:
		return IDs{StashDB: externalID}, externalID != ""
	case ProviderThePornDB:
		return IDs{ThePornDB: externalID}, externalID != ""
	case ProviderFansDB:
		return IDs{FansDB: externalID}, externalID != ""
	}
	return IDs{}, false
}

// dateToleranceDays is how far a file's day may sit from the provider's when the title is otherwise identical.
const dateToleranceDays = 2

// extraPrefix is the "BONUS-" or "BTS-" a network puts before the title of an extra on its site.
var extraPrefix = regexp.MustCompile(`(?i)^\s*(?:bonus|bts)\s*[-:]\s*`)

// withinDays says two yyyy-mm-dd dates are at most n days apart; unreadable dates are not.
func withinDays(a, b string, n int) bool {
	first, firstErr := time.Parse(time.DateOnly, a)
	second, secondErr := time.Parse(time.DateOnly, b)
	if firstErr != nil || secondErr != nil {
		return false
	}
	return first.Sub(second).Abs() <= time.Duration(n)*24*time.Hour
}
