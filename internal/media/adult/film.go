package adult

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/parseadult"
	"github.com/makearr/Solarr/internal/media"
)

// ProviderFilm marks a work Solarr built from a film's scenes: providers that hold a film only as "Title - Scene 1",
// "Title - Scene 2" have no record of the whole, so nothing refreshes it. Its external id in Review lists the scenes.
const ProviderFilm = "adult:film"

// sceneNumbered is a scene title that numbers a part of a film.
var sceneNumbered = regexp.MustCompile(`(?i)^(.*?)[\s\-:,.(\[]*\b(?:scene|sc)\s*\d+[)\]]?\s*$`)

// filmYearDrift is how far a scene's year may sit from a film's: scene records carry the day a site put them up. Review
// confirms the film, so a loose year costs little.
const filmYearDrift = 2

// filmTitle is the film a numbered scene title belongs to.
func filmTitle(title string) (string, bool) {
	match := sceneNumbered.FindStringSubmatch(title)
	if match == nil || strings.TrimSpace(match[1]) == "" {
		return "", false
	}
	return strings.TrimSpace(match[1]), true
}

// foundFilm is a film the search knows only by its scenes.
type foundFilm struct {
	Title, Studio string
	Scenes        []Candidate
}

// filmOf finds the scenes of the one film a whole file is: at least two numbered scenes of the file's title, of one
// studio, the largest listing when providers list it apart. A name that itself numbers a scene is that scene.
func filmOf(candidates []Candidate, info parseadult.Info) (foundFilm, bool) {
	if _, numbered := filmTitle(info.Title); numbered {
		return foundFilm{}, false
	}
	wanted := NameKey(info.Title)
	byStudio := map[string]*foundFilm{}
	var order []string
	for _, candidate := range candidates {
		title, numbered := filmTitle(candidate.Title)
		if !numbered || NameKey(title) != wanted {
			continue
		}
		if drift := candidate.Year - info.Year; info.Year != 0 && candidate.Year != 0 && (drift > filmYearDrift || drift < -filmYearDrift) {
			continue
		}
		if _, externalID := CandidateID(candidate.IDs); externalID == "" {
			continue
		}
		key := studioKey(candidate.Studio)
		if byStudio[key] == nil {
			byStudio[key] = &foundFilm{Title: title, Studio: candidate.Studio}
			order = append(order, key)
		}
		byStudio[key].Scenes = append(byStudio[key].Scenes, candidate)
	}
	var best foundFilm
	tied := false
	for _, key := range order {
		film := *byStudio[key]
		switch {
		case len(film.Scenes) > len(best.Scenes):
			best, tied = film, false
		case len(film.Scenes) == len(best.Scenes):
			tied = true
		}
	}
	return best, len(best.Scenes) >= 2 && !tied
}

// identification offers the film in Review, ahead of its scenes.
func (f foundFilm) identification() model.Identification {
	members := make([]string, 0, len(f.Scenes))
	for _, scene := range f.Scenes {
		provider, externalID := CandidateID(scene.IDs)
		members = append(members, provider+"="+externalID)
	}
	detail := fmt.Sprintf("%s: the whole film of %d scenes", f.Title, len(f.Scenes))
	if f.Studio != "" {
		detail += " (" + f.Studio + ")"
	}
	return model.Identification{
		Strategy:   "parse",
		ExternalID: &model.ExternalID{Provider: ProviderFilm, ExternalID: strings.Join(members, ","), Confidence: 1},
		Precedence: model.PrecedenceSearched,
		Confidence: 0.6,
		Evidence:   []model.MatchEvidence{{Kind: "parse", Detail: detail}},
	}
}

// filmMembers reads the scenes a film candidate lists.
func filmMembers(externalID string) ([]IDs, bool) {
	var members []IDs
	for _, member := range strings.Split(externalID, ",") {
		provider, id, found := strings.Cut(member, "=")
		ids, known := NamedIDs(provider, id)
		if !found || !known {
			return nil, false
		}
		members = append(members, ids)
	}
	return members, len(members) >= 2
}

// joinScenes is the film its scenes make: the film's title, the earliest day, everyone who appears in it, their length
// added up. Each scene's id stays on the work, so a release or a fingerprint naming a scene finds the film.
func joinScenes(title string, scenes []Scene) Scene {
	film := Scene{}
	for _, scene := range scenes {
		film.Members = append(film.Members, scene.IDs)
		duration := film.DurationSec + scene.DurationSec
		date := film.Date
		if date == "" || (scene.Date != "" && scene.Date < date) {
			date = scene.Date
		}
		credits := slices.Clone(film.Credits)
		for _, credit := range scene.Credits {
			if !slices.ContainsFunc(credits, func(held Credit) bool { return sameCredit(held, credit) }) {
				credit.Order = len(credits)
				credits = append(credits, credit)
			}
		}
		film = Merge(film, scene)
		film.DurationSec, film.Date, film.Credits = duration, date, credits
	}
	film.Title = title
	if len(film.Date) >= 4 {
		film.Year = cmp.Or(yearOf(film.Date), film.Year)
	}
	return film
}

func sameCredit(a, b Credit) bool {
	if a.ExternalID != "" || b.ExternalID != "" {
		return a.Provider == b.Provider && a.ExternalID == b.ExternalID
	}
	return NameKey(a.Name) == NameKey(b.Name)
}

// AddFilm stores a film from the scenes a person chose in Review, or answers the one already built from them.
func (s Service) AddFilm(ctx context.Context, request AddFilmRequest) (AddResult, error) {
	scenes := make([]Scene, 0, len(request.Members))
	for _, ids := range request.Members {
		if workID, held := s.WorkByIDs(ctx, ids); held {
			provider, err := s.Reader.ProviderIDs(ctx, workID)
			if err != nil {
				return AddResult{}, err
			}
			if _, film := provider[ProviderFilm]; film {
				return AddResult{WorkID: workID}, nil
			}
			return AddResult{}, fmt.Errorf("%w: one of its scenes is already in the library on its own", media.ErrInvalidAdd)
		}
		scene, err := s.FetchFor(ctx, request.CategoryID, ids, Hint{})
		if err != nil {
			return AddResult{}, err
		}
		scenes = append(scenes, scene)
	}
	if len(scenes) == 0 {
		return AddResult{}, fmt.Errorf("%w: a film needs its scenes", media.ErrInvalidAdd)
	}
	title, numbered := filmTitle(scenes[0].Title)
	if !numbered {
		return AddResult{}, fmt.Errorf("%w: %q is not a numbered scene", media.ErrInvalidAdd, scenes[0].Title)
	}
	film := joinScenes(title, scenes)
	now := s.Clock.Now()
	stored, err := s.Series.Upsert(ctx, now, ToWrite(film, now))
	if err != nil {
		return AddResult{}, err
	}
	item, err := s.Desired.EnsureAlbumItem(ctx, library.AlbumRequest{
		WorkID: stored.WorkID, CategoryID: request.CategoryID, ProfileID: request.ProfileID, Monitored: request.Monitor != MonitorNone, RequiredWithoutParts: 1,
	})
	if err != nil {
		return AddResult{}, fmt.Errorf("adult: wanted item: %w", err)
	}
	return AddResult{WorkID: stored.WorkID, ItemID: item.ID, Created: stored.Created, Scene: film}, nil
}

// AddFilmRequest names a film by its scenes.
type AddFilmRequest struct {
	Members    []IDs
	CategoryID int64
	ProfileID  int64
	Monitor    MonitorOption
}

// AddFilm places a film a person named by its scenes.
func (r Resolver) AddFilm(ctx context.Context, categoryID int64, externalID string) (int64, error) {
	members, ok := filmMembers(externalID)
	if !ok {
		return 0, fmt.Errorf("%w: %q does not list a film's scenes", media.ErrInvalidAdd, externalID)
	}
	profile, monitor := r.addChoices(categoryID)
	result, err := r.Service.AddFilm(ctx, AddFilmRequest{Members: members, CategoryID: categoryID, ProfileID: profile, Monitor: monitor})
	if errors.Is(err, ErrNoSource) {
		return 0, fmt.Errorf("%w: its scenes could not be read", media.ErrInvalidAdd)
	}
	return result.WorkID, err
}

// addChoices is the profile and monitoring a library gives what it adds by itself.
func (r Resolver) addChoices(categoryID int64) (int64, MonitorOption) {
	profile, monitor := r.DefaultProfileID, MonitorScene
	if category, ok := r.Categories.Category(categoryID); ok {
		profile = r.Categories.ProfileFor(category, profile)
		if category.TVMonitor == settings.TVMonitorNone {
			monitor = MonitorNone
		}
	}
	return profile, monitor
}
