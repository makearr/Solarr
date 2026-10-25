package book

import (
	"context"
	"log/slog"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/parsebook"
	"github.com/makearr/Solarr/internal/platform/keyedlock"
)

// Excluded is what Resolve answers for a book a person removed and asked not to see again: not a place to put the
// file, and not something to queue for review either.
const Excluded int64 = -1

// searchLimit is how many hits one search keeps.
const searchLimit = 10

// rankedWell is how far down a provider's own ranking an exact title-and-author hit may sit and still be taken.
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
	candidates, err := r.Service.Search(ctx, categoryID, info.Title, info.Author, searchLimit)
	if err != nil {
		r.warn("book lookup failed", info.Title, err)
		return 0, nil
	}
	if len(candidates) == 0 && info.Author != "" {
		// A folder above a book is often its series, not its author.
		candidates, _ = r.Service.Search(ctx, categoryID, info.Title, "", searchLimit)
	}
	if pick, ok := pickCandidate(candidates, info); ok {
		if id := r.add(ctx, categoryID, pick.IDs, Hint{Title: pick.Title, Author: pick.Author}); id != 0 {
			return id, nil
		}
	}
	return 0, r.identifications(candidates)
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
	titles, err := r.Service.Reader.TitlesOf(ctx, r.Service.Format.MediaType, categoryID)
	if err != nil {
		return 0, false
	}
	wanted := map[string]bool{}
	for _, title := range append([]string{info.Title}, info.Titles...) {
		wanted[parsebook.CleanTitle(title)] = true
	}
	var found []int64
	for _, held := range titles {
		if !wanted[parsebook.CleanTitle(held.Title)] {
			continue
		}
		if info.Year != 0 && held.Year != 0 && int(held.Year) != info.Year && r.Service.Format.ByFolder {
			// An audiobook's year is its reading's, the work's year is its first printing: years say nothing for it.
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
	return result.WorkID
}

// pickCandidate chooses the first of the provider's top few with exactly the named title and, when an author is named,
// that author; with no author named, the one such hit of that title.
func pickCandidate(candidates []Candidate, info parsebook.Info) (Candidate, bool) {
	wanted := map[string]bool{}
	for _, title := range append([]string{info.Title}, info.Titles...) {
		wanted[parsebook.CleanTitle(title)] = true
	}
	var named []Candidate
	for rank, candidate := range candidates {
		if rank >= rankedWell {
			break
		}
		if wanted[parsebook.CleanTitle(candidate.Title)] {
			named = append(named, candidate)
		}
	}
	if info.Author == "" {
		if len(named) == 1 {
			return named[0], true
		}
		return Candidate{}, false
	}
	for _, candidate := range named {
		if parsebook.NamesAuthor(" "+parsebook.CleanTitle(info.Author)+" ", candidate.Author) || PersonKey(candidate.Author) == PersonKey(info.Author) {
			return candidate, true
		}
	}
	return Candidate{}, false
}

// identifications are the hits a person picks from in Review, the provider's own ranking kept.
func (r Resolver) identifications(candidates []Candidate) []model.Identification {
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
	case ProviderISBN:
		isbn := strings.ReplaceAll(externalID, "-", "")
		return IDs{ISBN: isbn}, len(isbn) == 13
	}
	return IDs{}, false
}
