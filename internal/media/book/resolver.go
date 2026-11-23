package book

import (
	"context"
	"log/slog"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/parsebook"
	"github.com/makearr/Solarr/internal/media/works"
	"github.com/makearr/Solarr/internal/platform/keyedlock"
)

// Excluded is what Resolve answers for a book a person removed and asked not to see again: not a place to put the
// file, and not something to queue for review either.
const Excluded = works.Excluded

// searchLimit is how many hits one search keeps.
const searchLimit = 10

// rankedWell is how far down a provider's own ranking an exact title may sit and still be taken when no author is named.
const rankedWell = 3

// Categories is the library settings a resolver reads.
type Categories interface {
	Category(id int64) (settings.Category, bool)
	ProfileFor(category settings.Category, fallback int64) int64
}

// Resolver finds the book a file or folder is: an id in its name, a title of a book the library holds, or a single
// exact title-and-author hit at the providers, which it adds. Anything less clear is a person's decision.
type Resolver struct {
	Service          Service
	Logger           *slog.Logger
	DefaultProfileID int64
	Categories       Categories
	Locks            *keyedlock.Locks
	// Threshold is import.auto_commit_confidence; nil takes its default.
	Threshold func() float64
}

// Resolve answers the work a parsed name is: 0 when it could not be placed, in which case the candidates say what was
// considered, or Excluded.
func (r Resolver) Resolve(ctx context.Context, categoryID int64, info parsebook.Info) (int64, []model.Identification) {
	ids := IDs{ISBN: info.ISBN, ASIN: info.ASIN}
	if !r.Service.Format.ByFolder {
		ids.ASIN = ""
	}
	if r.Locks != nil {
		key := strconv.FormatInt(categoryID, 10) + "|" + parsebook.CleanTitle(info.Title) + "|" + PersonKey(info.Author)
		if !ids.Empty() {
			key = strconv.FormatInt(categoryID, 10) + "|" + ids.ISBN + ids.ASIN
		}
		defer r.Locks.Lock(key)()
	}
	if !ids.Empty() {
		if id := r.ByIDs(ctx, categoryID, ids); id != 0 {
			return id, nil
		}
	}
	if strings.TrimSpace(info.Title) == "" {
		return 0, nil
	}
	if id, found := r.inLibrary(ctx, categoryID, info); found {
		return id, nil
	}
	found := r.find(ctx, categoryID, info)
	if found.failed {
		return 0, nil
	}
	if found.ok && model.AcceptsFolderMatch(r.confidenceOf(found, ids), r.Threshold) {
		if id := r.add(ctx, categoryID, found.picked.IDs, Hint{Title: found.picked.Title, Author: found.picked.Author}); id != 0 {
			return id, nil
		}
	}
	return 0, r.identifications(found.candidates, found.reading)
}

// capBelowThreshold keeps a title that only the file's own metadata gave out of an automatic placement: such files exist mis-tagged.
const capBelowThreshold = 0.01

// confidenceOf is the pick's confidence, held below the threshold when its title came from the file's metadata alone
// and no id of the file's own named it.
func (r Resolver) confidenceOf(f found, ids IDs) float64 {
	if f.unbacked && !f.picked.namesID(ids) {
		return min(f.confidence, r.floor()-capBelowThreshold)
	}
	return f.confidence
}

func (r Resolver) floor() float64 {
	if r.Threshold != nil {
		return r.Threshold()
	}
	return model.FolderLooseMatch
}

// namesID is true when the candidate carries one of the ids the file itself gave.
func (c Candidate) namesID(ids IDs) bool {
	return (ids.ISBN != "" && c.IDs.ISBN == ids.ISBN) || (ids.ASIN != "" && c.IDs.ASIN == ids.ASIN)
}

// found is what the search for a parsed name came to.
type found struct {
	candidates []Candidate
	picked     Candidate
	confidence float64
	ok, failed bool
	// unbacked: the title is the file's own metadata and no reading of its name says the same.
	unbacked bool
	// reading names a reading other than the name's own that found the book; empty for the usual one.
	reading string
}

// readingExchanged says the name was read with its author and title the other way round.
const readingExchanged = "author and title exchanged"

// find searches the providers for the book a name is: its title and author first, then the two exchanged (a name
// the other way round finds nothing), then each other reading of the title.
func (r Resolver) find(ctx context.Context, categoryID int64, info parsebook.Info) found {
	out := found{unbacked: info.TitleUnbacked}
	placed := func(hits []Candidate) bool { _, _, ok := pickCandidate(hits, info); return ok }
	candidates, err := r.Service.SearchUntil(ctx, categoryID, info.Title, info.Author, searchLimit, placed)
	if err != nil {
		r.warn("book lookup failed", info.Title, err)
		out.failed = true
		return out
	}
	if len(candidates) == 0 && info.Author != "" {
		// A folder above a book is often its series, not its author.
		candidates, _ = r.Service.Search(ctx, categoryID, info.Title, "", searchLimit)
	}
	out.candidates = candidates
	out.picked, out.confidence, out.ok = pickCandidate(candidates, info)
	if !out.ok && info.Author != "" && info.Title != "" {
		swapped := parsebook.Info{Author: info.Title, Title: info.Author}
		if hits, err := r.Service.Search(ctx, categoryID, swapped.Title, swapped.Author, searchLimit); err == nil {
			if pick, confidence, ok := pickCandidate(hits, swapped); ok {
				out.picked, out.confidence, out.ok, out.reading = pick, confidence, true, readingExchanged
			}
			if len(out.candidates) == 0 {
				out.candidates = hits
				out.reading = readingExchanged
			}
		}
	}
	for _, reading := range otherReadings(info) {
		if out.ok {
			break
		}
		// The name's best reading may be only its subtitle ("A Novel"): each other reading is searched on its own.
		hits, err := r.Service.Search(ctx, categoryID, reading, info.Author, searchLimit)
		if err == nil {
			out.picked, out.confidence, out.ok = pickCandidate(hits, info)
		}
	}
	return out
}

// maxOtherReadings bounds the extra searches one unplaced name costs.
const maxOtherReadings = 2

// otherReadings are the name's titles other than the one already searched, in the order read.
func otherReadings(info parsebook.Info) []string {
	var others []string
	for _, title := range info.Titles {
		if title != info.Title && !slices.Contains(others, title) {
			others = append(others, title)
		}
	}
	return others[:min(len(others), maxOtherReadings)]
}

func (r Resolver) warn(message, title string, err error) {
	if r.Logger != nil {
		r.Logger.Warn(message, slog.String("media_type", r.Service.Format.MediaType), slog.String("title", title), slog.Any("error", err))
	}
}

// ByIDs places a book named by its ids: the one held under any of them, else the one added from them.
func (r Resolver) ByIDs(ctx context.Context, categoryID int64, ids IDs) int64 {
	if id, held := r.Service.WorkByIDs(ctx, ids); held {
		return id
	}
	return r.add(ctx, categoryID, ids, Hint{})
}

// inLibrary finds the one book the library holds under this title or an alias of it, in a year that agrees when both
// are known.
func (r Resolver) inLibrary(ctx context.Context, categoryID int64, info parsebook.Info) (int64, bool) {
	wanted := map[string]bool{}
	for _, title := range append([]string{info.Title}, info.Titles...) {
		wanted[parsebook.CleanTitle(title)] = true
	}
	titles, err := r.Service.Reader.TitlesMatching(ctx, r.Service.Format.MediaType, categoryID, parsebook.CleanTitle, slices.Collect(maps.Keys(wanted)))
	if err != nil {
		return 0, false
	}
	var found []int64
	for _, held := range titles {
		if !wanted[parsebook.CleanTitle(held.Title)] {
			continue
		}
		if !yearsAgree(info.Year, int(held.Year), r.Service.Format.ByFolder) {
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

// yearsAgree compares an ebook's year within one year (a reprint at the turn of a year); an audiobook's year is its
// reading's, the work's is its first printing, so years say nothing for it.
func yearsAgree(wanted, held int, audiobook bool) bool {
	if audiobook || wanted == 0 || held == 0 {
		return true
	}
	return wanted-held <= 1 && held-wanted <= 1
}

func (r Resolver) add(ctx context.Context, categoryID int64, ids IDs, hint Hint) int64 {
	if r.Service.IsExcluded(ctx, ids) {
		return Excluded
	}
	profile := r.DefaultProfileID
	monitor := MonitorBook
	if category, ok := r.Categories.Category(categoryID); ok {
		profile = r.Categories.ProfileFor(category, profile)
		if category.TVMonitor == settings.TVMonitorNone {
			monitor = MonitorNone
		}
	}
	result, err := r.Service.Add(ctx, AddRequest{IDs: ids, Hint: hint, CategoryID: categoryID, ProfileID: profile, Monitor: monitor})
	if err != nil {
		r.warn("book could not be added", ids.OpenLibrary+ids.ASIN+ids.ISBN, err)
		return 0
	}
	r.Service.Reader.ForgetTitles(r.Service.Format.MediaType, categoryID)
	return result.WorkID
}

// pickCandidate chooses the first of the provider's top few with exactly the named title and, when an author is named,
// that author; with no author named, the one such hit of that title.
func pickCandidate(candidates []Candidate, info parsebook.Info) (Candidate, float64, bool) {
	wanted := map[string]bool{}
	for _, title := range append([]string{info.Title}, info.Titles...) {
		wanted[parsebook.CleanTitle(title)] = true
	}
	var named []Candidate
	for rank, candidate := range candidates {
		// With an author named, an exact title by that author needs no good rank: Audible lists book 1 eighth.
		if rank >= rankedWell && info.Author == "" {
			break
		}
		if wanted[parsebook.CleanTitle(candidate.Title)] || wanted[parsebook.CleanTitle(trailingEdition.ReplaceAllString(candidate.Title, ""))] {
			named = append(named, candidate)
		}
	}
	if len(named) == 0 && info.Author != "" {
		// Only a lone hit by the named author whose title continues as a subtitle ("Title: Subtitle") is the book.
		for rank, candidate := range candidates {
			if rank < rankedWell && wanted[parsebook.CleanTitle(subtitleHead.Split(candidate.Title, 2)[0])] &&
				candidate.namesAuthor(info.Author) {
				named = append(named, candidate)
			}
		}
		if len(named) == 1 {
			return named[0], model.FolderLooseMatch, true
		}
		named = nil
		// "Series and the Title": the provider's title is the folder's last words, by the author the folder names.
		var seriesPrefixed []Candidate
		for rank, candidate := range candidates {
			if rank < rankedWell && candidate.namesAuthor(info.Author) && endsTheTitle(candidate.Title, info.Title) {
				seriesPrefixed = append(seriesPrefixed, candidate)
			}
		}
		if len(seriesPrefixed) == 1 {
			return seriesPrefixed[0], model.FolderLooseMatch, true
		}
	}
	if info.Author == "" {
		if len(named) == 1 {
			return named[0], model.FolderLooseMatch, true
		}
		return Candidate{}, 0, false
	}
	for _, candidate := range named {
		if candidate.namesAuthor(info.Author) {
			return candidate, model.FolderExactMatch, true
		}
	}
	// A name in another script (Liu Cixin, 刘慈欣) cannot be compared with the folder's, so the title alone speaks.
	var unreadable []Candidate
	for _, candidate := range named {
		if candidate.Author != "" && !hasLatinLetter(candidate.Author) {
			unreadable = append(unreadable, candidate)
		}
	}
	if len(unreadable) > 0 && len(unreadable) == len(named) {
		return unreadable[0], model.FolderLooseMatch, true
	}
	return Candidate{}, 0, false
}

// namesAuthor says the folder's author is one of the book's authors: a book by two is found under either.
func (c Candidate) namesAuthor(wanted string) bool {
	spaced := " " + parsebook.CleanTitle(wanted) + " "
	for _, name := range append([]string{c.Author}, c.CoAuthors...) {
		if parsebook.NamesAuthor(spaced, name) || PersonKey(name) == PersonKey(wanted) {
			return true
		}
	}
	return false
}

func othersThanFirst(names []string) []string {
	if len(names) < 2 {
		return nil
	}
	return names[1:]
}

// trailingEdition is a provider's "(Korean Edition)" or "(The Three-Body Problem Series Book 2)" after a title.
var subtitleHead = regexp.MustCompile(`:\s|\s\(|\s-\s`)

var trailingEdition = regexp.MustCompile(`\s*\([^)]*\)\s*$`)

func hasLatinLetter(name string) bool {
	return strings.ContainsFunc(name, func(r rune) bool { return unicode.Is(unicode.Latin, r) })
}

// identifications are the hits a person picks from in Review, the provider's own ranking kept.
func (r Resolver) identifications(candidates []Candidate, reading string) []model.Identification {
	out := make([]model.Identification, 0, len(candidates))
	for rank, candidate := range candidates {
		provider, externalID := r.Service.Format.CandidateID(candidate.IDs)
		if externalID == "" {
			continue
		}
		detail := candidate.Title
		if candidate.Author != "" {
			detail += " by " + candidate.Author
		}
		if reading != "" {
			detail += " (" + reading + ")"
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

// CandidateID is the one id a found book is named by to a person and back: Audible's for a reading, else Open
// Library's, else Google's.
func (f Format) CandidateID(ids IDs) (provider, externalID string) {
	switch {
	case ids.ASIN != "" && f.ByFolder:
		return ProviderAudible, ids.ASIN
	case ids.OpenLibrary != "":
		return f.Provider(ProviderOpenLibrary), ids.OpenLibrary
	case ids.GoogleBooks != "":
		return f.Provider(ProviderGoogleBooks), ids.GoogleBooks
	case ids.ISBN != "":
		return f.Provider(ProviderISBN), ids.ISBN
	case ids.Hardcover != "":
		return f.Provider(ProviderHardcover), ids.Hardcover
	case ids.ITunes != "":
		return f.Provider(ProviderITunes), ids.ITunes
	case ids.FantLab != "":
		return f.Provider(ProviderFantLab), ids.FantLab
	case ids.BookBrainz != "":
		return f.Provider(ProviderBookBrainz), ids.BookBrainz
	}
	return "", ""
}

// NamedIDs reads a book a person named by provider and id; the format's own keys and the plain ones are both taken.
func (f Format) NamedIDs(provider, externalID string) (IDs, bool) {
	externalID = strings.TrimSpace(externalID)
	base := strings.TrimSuffix(provider, ":"+f.MediaType)
	switch base {
	case ProviderAudible:
		return IDs{ASIN: strings.ToUpper(externalID)}, externalID != "" && f.ByFolder
	case ProviderOpenLibrary:
		return IDs{OpenLibrary: externalID}, strings.HasPrefix(externalID, "OL") && strings.HasSuffix(externalID, "W")
	case ProviderGoogleBooks:
		return IDs{GoogleBooks: externalID}, externalID != ""
	case ProviderHardcover:
		return IDs{Hardcover: externalID}, externalID != ""
	case ProviderITunes:
		return IDs{ITunes: externalID}, externalID != "" && f.ByFolder
	case ProviderFantLab:
		return IDs{FantLab: externalID}, externalID != ""
	case ProviderBookBrainz:
		return IDs{BookBrainz: externalID}, externalID != ""
	case ProviderISBN:
		isbn := strings.ReplaceAll(externalID, "-", "")
		return IDs{ISBN: isbn}, len(isbn) == 13
	}
	return IDs{}, false
}

// endsTheTitle says the candidate's title, of at least two words, is the last words of the longer title a folder gives.
func endsTheTitle(candidate, folder string) bool {
	short, long := strings.Fields(parsebook.CleanTitle(candidate)), strings.Fields(parsebook.CleanTitle(folder))
	return len(short) >= 2 && len(short) < len(long) && slices.Equal(long[len(long)-len(short):], short)
}
