package comic

import (
	"context"
	"log/slog"
	"strconv"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/comicinfo"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/parsecomic"
	"github.com/makearr/Solarr/internal/platform/keyedlock"
)

// Evidence is what a file, its folders and its embedded metadata say about its series.
type Evidence struct {
	// IDs are the series ids the file's metadata names, by provider key.
	IDs map[string]string
	// Series and Year are the series the evidence names; Publisher narrows a title shared by several.
	Series, Publisher string
	Year              int
}

// EvidenceOf draws the series evidence from the strongest source a file has: its embedded ids, its embedded series name,
// then the names of its file, its folder and its parent folder (Kapowarr's order).
func EvidenceOf(info *comicinfo.Info, claims ...parsecomic.Claim) Evidence {
	ev := Evidence{IDs: map[string]string{}}
	if info != nil {
		for _, id := range info.IDs {
			switch id.Source {
			case comicinfo.IDComicVineSeries:
				ev.IDs[ProviderComicVine] = id.Value
			case comicinfo.IDMetronSeries:
				ev.IDs[ProviderMetron] = id.Value
			}
		}
		ev.Series, ev.Publisher, ev.Year = info.Series, info.Publisher, firstInt(info.Volume, info.Year)
		if ev.Year < 1900 {
			ev.Year = info.Year
		}
	}
	for _, claim := range claims {
		if ev.Series == "" {
			ev.Series = claim.Series
		}
		if ev.Year == 0 {
			ev.Year = claim.Year
		}
		if ev.Publisher == "" {
			ev.Publisher = claim.Publisher
		}
	}
	return ev
}

// Categories is what a resolver needs of the libraries.
type Categories interface {
	Category(id int64) (settings.Category, bool)
	ProfileFor(category settings.Category, fallback int64) int64
}

// Resolver finds the series a file belongs to: a held one by id or by title, else the one a provider names with
// enough confidence to add; anything less becomes candidates for a person to choose from.
type Resolver struct {
	Service          Service
	Logger           *slog.Logger
	Categories       Categories
	DefaultProfileID int64
	Locks            *keyedlock.Locks
	// Threshold is import.auto_commit_confidence; nil takes its default.
	Threshold func() float64
}

// Confidence of a provider's single title match: the title alone is the threshold's floor, a year or publisher that agrees
// lifts it, and a title that two series share lowers it below.
const (
	titleOnlyConfidence = 0.90
	yearAgreesBonus     = 0.06
	publisherBonus      = 0.03
	ambiguousPenalty    = 0.25
)

// Resolve answers the series of the evidence: 0 when it could not be placed, in which case the candidates say what was
// considered.
func (r Resolver) Resolve(ctx context.Context, categoryID int64, ev Evidence) (int64, []model.Identification) {
	if r.Locks != nil {
		defer r.Locks.Lock(strconv.FormatInt(categoryID, 10) + "|" + titleKey(ev.Series) + "|" + ev.IDs[ProviderComicVine] + ev.IDs[ProviderMetron])()
	}
	if id, held := r.Service.WorkByIDs(ctx, ev.IDs); held {
		return id, nil
	}
	if len(ev.IDs) > 0 {
		if id := r.add(ctx, categoryID, ev.IDs, Hint{Title: ev.Series, Year: ev.Year}); id != 0 {
			return id, nil
		}
	}
	if titleKey(ev.Series) == "" {
		return 0, nil
	}
	if id, held := r.inLibrary(ctx, categoryID, ev); held {
		return id, nil
	}
	found, err := r.Service.Search(ctx, categoryID, ev.Series, 8)
	if err != nil {
		r.warn("search", ev.Series, err)
		return 0, nil
	}
	picked, confidence, ok := pick(found, ev)
	if ok && model.AcceptsFolderMatch(confidence, r.Threshold) {
		if id := r.add(ctx, categoryID, picked.IDs, Hint{Title: picked.Title, Year: picked.Year}); id != 0 {
			return id, nil
		}
	}
	return 0, identifications(found)
}

// inLibrary finds the held series whose title or alias is the evidence's, with a year that agrees; two held series of one
// title and no year to tell them apart are not guessed between.
func (r Resolver) inLibrary(ctx context.Context, categoryID int64, ev Evidence) (int64, bool) {
	titles, err := r.Service.Reader.TitlesOf(ctx, MediaType, categoryID)
	if err != nil {
		r.warn("titles", ev.Series, err)
		return 0, false
	}
	var matches []int64
	for _, held := range titles {
		if sameTitle(held.Title, ev.Series) && yearsAgree(ev.Year, int(held.Year)) && !containsID(matches, held.WorkID) {
			matches = append(matches, held.WorkID)
		}
	}
	if len(matches) == 1 {
		return matches[0], true
	}
	return 0, false
}

func containsID(ids []int64, id int64) bool {
	for _, held := range ids {
		if held == id {
			return true
		}
	}
	return false
}

// yearsAgree: a series that began a year either side of the named year is the same run (cover and store dates differ).
func yearsAgree(wanted, held int) bool {
	return wanted == 0 || held == 0 || wanted-held <= 1 && held-wanted <= 1
}

// pick chooses the one candidate the evidence names and says how sure that is.
func pick(found []Candidate, ev Evidence) (Candidate, float64, bool) {
	var same []Candidate
	for _, candidate := range found {
		if sameTitle(candidate.Title, ev.Series) && yearsAgree(ev.Year, candidate.Year) {
			same = append(same, candidate)
		}
	}
	if len(same) == 0 {
		return Candidate{}, 0, false
	}
	best := same[0]
	confidence := titleOnlyConfidence
	if ev.Year != 0 && best.Year != 0 {
		confidence += yearAgreesBonus
	}
	if ev.Publisher != "" && sameTitle(best.Publisher, ev.Publisher) {
		confidence += publisherBonus
	}
	if len(same) > 1 {
		confidence -= ambiguousPenalty
	}
	return best, confidence, true
}

func identifications(found []Candidate) []model.Identification {
	out := make([]model.Identification, 0, len(found))
	for rank, candidate := range found {
		detail := candidate.Title
		if candidate.Year > 0 {
			detail += " (" + strconv.Itoa(candidate.Year) + ")"
		}
		if candidate.Publisher != "" {
			detail += ", " + candidate.Publisher
		}
		out = append(out, model.Identification{
			Strategy:   "parse",
			ExternalID: &model.ExternalID{Provider: candidate.Provider, ExternalID: candidate.ID, Confidence: 1 - 0.05*float64(rank)},
			Precedence: model.PrecedenceSearched,
			Confidence: max(0.1, 0.6-0.05*float64(rank)),
			Evidence:   []model.MatchEvidence{{Kind: "parse", Detail: detail}},
		})
	}
	return out
}

// add stores the series the ids name in the library the file is in, unmonitored: its files, not the provider's whole
// back catalogue, are what the person has asked to keep.
func (r Resolver) add(ctx context.Context, categoryID int64, ids map[string]string, hint Hint) int64 {
	category, found := r.Categories.Category(categoryID)
	if !found {
		return 0
	}
	result, err := r.Service.Add(ctx, AddRequest{
		IDs: ids, Hint: hint, CategoryID: categoryID, ProfileID: r.Categories.ProfileFor(category, r.DefaultProfileID), Monitor: MonitorNone,
	})
	if err != nil {
		r.warn("add", hint.Title, err)
		return 0
	}
	return result.WorkID
}

func (r Resolver) warn(what, title string, err error) {
	if r.Logger != nil {
		r.Logger.Warn("comic resolve failed", slog.String("step", what), slog.String("title", title), slog.Any("error", err))
	}
}
