package adult

import (
	"context"
	"log/slog"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/parseadult"
	"github.com/makearr/Solarr/internal/metadata/stashbox"
	"github.com/makearr/Solarr/internal/platform/keyedlock"
)

// Excluded is what Resolve answers for a scene a person removed and asked not to see again: not a place to put the
// file, and not something to queue for review either.
const Excluded int64 = -1

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
}

// Resolve answers the work a parsed name (and the file's oshash, when it has one) is: 0 when it could not be placed,
// in which case the candidates say what was considered, or Excluded.
func (r Resolver) Resolve(ctx context.Context, categoryID int64, info parseadult.Info, oshash string) (int64, []model.Identification) {
	if r.Locks != nil {
		defer r.Locks.Lock(strconv.FormatInt(categoryID, 10) + "|" + NameKey(info.Studio) + "|" + NameKey(info.Title) + "|" + info.Date + "|" + oshash)()
	}
	if oshash != "" {
		if scene, found := r.Service.ByFingerprint(ctx, categoryID, []stashbox.Fingerprint{{Hash: oshash, Algorithm: stashbox.AlgorithmOSHash}}); found {
			if id := r.ByIDs(ctx, categoryID, scene.IDs, Hint{Title: scene.Title, Studio: scene.Studio, Date: scene.Date}); id != 0 {
				return id, nil
			}
		}
	}
	if strings.TrimSpace(info.Title) == "" {
		return 0, nil
	}
	if id, found := r.inLibrary(ctx, categoryID, info); found {
		return id, nil
	}
	candidates, err := r.Service.Search(ctx, categoryID, Query{Title: info.Title, Studio: info.Studio, Date: info.Date}, searchLimit)
	if err != nil {
		r.warn("scene lookup failed", info.Title, err)
		return 0, nil
	}
	if pick, ok := pickCandidate(candidates, info); ok {
		if id := r.add(ctx, categoryID, pick.IDs, Hint{Title: pick.Title, Studio: pick.Studio, Date: pick.Date}); id != 0 {
			return id, nil
		}
	}
	return 0, r.identifications(candidates)
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
	return r.add(ctx, categoryID, ids, hint)
}

// inLibrary finds the one scene the library holds under this title, in a year that agrees when both are known.
func (r Resolver) inLibrary(ctx context.Context, categoryID int64, info parseadult.Info) (int64, bool) {
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

func (r Resolver) add(ctx context.Context, categoryID int64, ids IDs, hint Hint) int64 {
	if r.Service.IsExcluded(ctx, ids) {
		return Excluded
	}
	profile := r.DefaultProfileID
	monitor := MonitorScene
	if category, ok := r.Categories.Category(categoryID); ok {
		profile = r.Categories.ProfileFor(category, profile)
		if category.TVMonitor == settings.TVMonitorNone {
			monitor = MonitorNone
		}
	}
	result, err := r.Service.Add(ctx, AddRequest{IDs: ids, Hint: hint, CategoryID: categoryID, ProfileID: profile, Monitor: monitor})
	if err != nil {
		r.warn("scene could not be added", ids.StashDB+ids.ThePornDB+ids.FansDB, err)
		return 0
	}
	return result.WorkID
}

// pickCandidate chooses the one of the provider's top few that is the named scene: the same title and, where both
// know the day, the same day. Two such hits are told apart by the day, and two on one day are one scene listed twice.
func pickCandidate(candidates []Candidate, info parseadult.Info) (Candidate, bool) {
	var named []Candidate
	for rank, candidate := range candidates {
		if rank >= rankedWell {
			break
		}
		if info.Date != "" && candidate.Date != "" && info.Date != candidate.Date {
			continue
		}
		sameDay := info.Date != "" && info.Date == candidate.Date
		if titleMatches(candidate.Title, info.Title, sameDay) {
			named = append(named, candidate)
		}
	}
	if len(named) == 1 {
		return named[0], true
	}
	var onTheDay []Candidate
	for _, candidate := range named {
		if info.Date != "" && candidate.Date == info.Date {
			onTheDay = append(onTheDay, candidate)
		}
	}
	// The same title on the same day twice is one scene a network lists under two sites: the provider's best ranked is it.
	if len(onTheDay) > 0 {
		return onTheDay[0], true
	}
	return Candidate{}, false
}

// titleMatches says two titles are the same scene's: equal, or on the same day one holding the other ("Title" and
// "Title - Scene 3").
func titleMatches(found, wanted string, sameDay bool) bool {
	foundKey, wantedKey := NameKey(found), NameKey(wanted)
	if foundKey == "" || wantedKey == "" {
		return false
	}
	return foundKey == wantedKey || (sameDay && (strings.Contains(foundKey, wantedKey) || strings.Contains(wantedKey, foundKey)))
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
