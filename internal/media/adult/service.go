package adult

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/stashbox"
	"github.com/makearr/Solarr/internal/metadata/theporndb"
	"github.com/makearr/Solarr/internal/metadata/whisparr"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// ErrNoSource means no provider could be asked or none knew the scene.
var ErrNoSource = errors.New("adult: no metadata source answered")

// searchLimit is how many hits one search keeps.
const searchLimit = 10

// Server is a stash-box client as the service needs it.
type Server interface {
	Configured() bool
	SearchScenes(ctx context.Context, term string, limit int) ([]stashbox.Scene, error)
	ScenesOnDate(ctx context.Context, title, date string, limit int) ([]stashbox.Scene, error)
	ScenesOnDates(ctx context.Context, queries []stashbox.DayQuery, limit int) ([][]stashbox.Scene, error)
	FindScene(ctx context.Context, id string) (stashbox.Scene, error)
	ScenesByFingerprint(ctx context.Context, files [][]stashbox.Fingerprint) ([][]stashbox.Scene, error)
	FindPerformer(ctx context.Context, id string) (stashbox.Performer, error)
	SearchPerformers(ctx context.Context, term string, limit int) ([]stashbox.Performer, error)
	FindStudio(ctx context.Context, id string) (stashbox.Studio, error)
	ScenesOf(ctx context.Context, filter stashbox.SceneFilter, page, perPage int) ([]stashbox.Scene, error)
}

// WhisparrSource is Whisparr's metadata server as the service needs it.
type WhisparrSource interface {
	SearchScenes(ctx context.Context, term string) ([]whisparr.Scene, error)
	SearchSites(ctx context.Context, term string) ([]whisparr.Site, error)
}

// MovieSource is ThePornDB's REST API as the service needs it: the films the other servers do not hold.
type MovieSource interface {
	Configured() bool
	SearchMovies(ctx context.Context, term string, limit int) ([]theporndb.Movie, error)
	Movie(ctx context.Context, id string) (theporndb.Movie, error)
}

// Service reads scenes from the providers and keeps them in the library. Any source may be nil.
type Service struct {
	Whisparr WhisparrSource
	// Movies adds ThePornDB's films to what its entry in a library's order answers; nil leaves films to the scenes' servers.
	Movies MovieSource
	// Servers are the stash-box servers by provider name (ProviderStashDB, ProviderThePornDB, ProviderFansDB).
	Servers map[string]Server
	// Sources are the providers a library asks, in its order, unless it chose its own.
	Sources []string
	// PriorityFor is a library's provider order; nil, or an empty answer, takes Sources.
	PriorityFor func(categoryID int64) []string
	Series      library.Series
	Reader      library.SeriesReader
	Desired     library.Desired
	Exclusions  library.Exclusions
	Clock       clock.Clock
	Logger      *slog.Logger
	// Memo keeps searches a while, the empty ones too; nil asks every time.
	Memo *SearchMemo
}

func (s Service) warn(source, what string, err error) {
	if s.Logger == nil {
		return
	}
	if errors.Is(err, httpx.ErrCircuitOpen) {
		s.Logger.Debug("adult metadata source paused", slog.String("source", source), slog.String("for", what))
		return
	}
	s.Logger.Warn("adult metadata source failed", slog.String("source", source), slog.String("for", what), slog.Any("error", err))
}

// order is the providers a library asks, best first, Wikidata left out: it holds no scenes.
func (s Service) order(categoryID int64) []string {
	order := s.Sources
	if s.PriorityFor != nil && categoryID != 0 {
		if own := s.PriorityFor(categoryID); len(own) > 0 {
			order = own
		}
	}
	return slices.DeleteFunc(slices.Clone(order), func(name string) bool { return name == settings.SourceWikidata })
}

// Hint is what is known of a scene besides its ids, so a provider without one of them can still be asked.
type Hint struct {
	Title, Studio, Date string
}

// FetchFor merges every source the library asks that knows the scene, the first supplying what it holds and each later
// one only filling what is missing. A failing source is skipped; only all failing is an error.
func (s Service) FetchFor(ctx context.Context, categoryID int64, ids IDs, hint Hint) (Scene, error) {
	return s.fetch(ctx, categoryID, ids, hint, false, nil)
}

// providerPatience bounds one provider's lookup, its rate limiter's queue included: a flaky one it has slowed must not hold a scan for good.
const providerPatience = 2 * time.Minute

// fetch is FetchFor; quick stops asking once the scene holds what placing a file needs, leaving the rest to its refresh.
func (s Service) fetch(ctx context.Context, categoryID int64, ids IDs, hint Hint, quick bool, seed *Scene) (Scene, error) {
	order := s.order(categoryID)
	answers := map[string]Scene{}
	if seed != nil && len(seed.Sources) > 0 && slices.Contains(order, seed.Sources[0]) {
		answers[seed.Sources[0]] = *seed
		ids = mergeIDs(ids, seed.IDs)
		if quick && enough(*seed) {
			return finish(answers, order, ids)
		}
	}
	// A provider early in the order may lack the title a later one teaches it: it is asked again once the first round ran.
	askedWith := map[string]askedAbout{}
	type question struct {
		name  string
		ids   IDs
		known Hint
	}
	for round := 0; round < 2; round++ {
		var questions []question
		for _, name := range order {
			if _, asked := answers[name]; asked {
				continue
			}
			known := hintFrom(hint, answers, order)
			if askedWith[name] == (askedAbout{ids, known}) {
				continue
			}
			askedWith[name] = askedAbout{ids, known}
			questions = append(questions, question{name, ids, known})
		}
		scenes := make([]Scene, len(questions))
		answered := make([]bool, len(questions))
		ask := func(i int) {
			asking, cancel := context.WithTimeout(ctx, providerPatience)
			defer cancel()
			scenes[i], answered[i] = s.ask(asking, questions[i].name, questions[i].ids, questions[i].known)
		}
		if quick {
			for i, q := range questions {
				if ask(i); answered[i] {
					answers[q.name] = scenes[i]
					ids = mergeIDs(ids, scenes[i].IDs)
					if enough(mergeAnswers(answers, order)) {
						return finish(answers, order, ids)
					}
				}
			}
			continue
		}
		// The providers of a round are asked together: each answers by its own bucket.
		var asking sync.WaitGroup
		for i := range questions {
			asking.Add(1)
			go func() {
				defer asking.Done()
				ask(i)
			}()
		}
		asking.Wait()
		for i, q := range questions {
			if answered[i] {
				answers[q.name] = scenes[i]
				ids = mergeIDs(ids, scenes[i].IDs)
			}
		}
	}
	return finish(answers, order, ids)
}

// mergeAnswers is the answers so far as one scene, the first in the order supplying and the later filling.
func mergeAnswers(answers map[string]Scene, order []string) Scene {
	var scene Scene
	found := false
	for _, name := range order {
		answer, ok := answers[name]
		switch {
		case !ok:
		case !found:
			scene, found = answer, true
		default:
			scene = Merge(scene, answer)
		}
	}
	return scene
}

// enough says a scene holds what placing its file needs: its title, day, studio, a performer and a picture.
func enough(scene Scene) bool {
	return scene.Title != "" && scene.Date != "" && scene.Studio != "" && len(scene.Performers()) > 0 && len(scene.Images) > 0
}

func finish(answers map[string]Scene, order []string, ids IDs) (Scene, error) {
	scene := mergeAnswers(answers, order)
	if scene.Title == "" {
		return Scene{}, ErrNoSource
	}
	scene.IDs = mergeIDs(scene.IDs, ids)
	return scene, nil
}

// askedAbout is what a provider was asked with: the same again gets the same answer.
type askedAbout struct {
	ids  IDs
	hint Hint
}

func mergeIDs(first, second IDs) IDs {
	return IDs{StashDB: cmp.Or(first.StashDB, second.StashDB), ThePornDB: cmp.Or(first.ThePornDB, second.ThePornDB), FansDB: cmp.Or(first.FansDB, second.FansDB)}
}

// hintFrom is the hint, with what the sources asked first supplying what it lacks: a provider that finds a scene by
// its name needs one.
func hintFrom(hint Hint, answers map[string]Scene, order []string) Hint {
	for _, name := range order {
		if answer, ok := answers[name]; ok {
			hint = Hint{Title: cmp.Or(hint.Title, answer.Title), Studio: cmp.Or(hint.Studio, answer.Studio), Date: cmp.Or(hint.Date, answer.Date)}
		}
	}
	return hint
}

// ask asks one provider by the ids known, else by the name; one that knows nothing answers none.
func (s Service) ask(ctx context.Context, name string, ids IDs, hint Hint) (Scene, bool) {
	if name == sourceWhisparr {
		return s.askWhisparr(ctx, ids, hint)
	}
	if scene, ok := s.askServer(ctx, name, ids, hint); ok {
		return scene, true
	}
	if name == ProviderThePornDB {
		return s.askMovie(ctx, ids, hint)
	}
	return Scene{}, false
}

func (s Service) askServer(ctx context.Context, name string, ids IDs, hint Hint) (Scene, bool) {
	server, ok := s.Servers[name]
	if !ok || server == nil || !server.Configured() {
		return Scene{}, false
	}
	if id := idOf(ids, name); id != "" {
		found, err := server.FindScene(ctx, id)
		if err != nil {
			if !errors.Is(err, stashbox.ErrNotFound) {
				s.warn(name, id, err)
			}
			return Scene{}, false
		}
		return FromStashBox(name, found), true
	}
	if hint.Title == "" {
		return Scene{}, false
	}
	onDay, _ := s.onDate(ctx, name, server, hint.Title, hint.Date, searchLimit)
	for _, scene := range onDay {
		if sameScene(scene, hint) {
			return scene, true
		}
	}
	for _, term := range searchTerms(hint) {
		found, err := memoized(ctx, s.Memo, name, term, func() ([]stashbox.Scene, error) { return server.SearchScenes(ctx, term, searchLimit) })
		if err != nil {
			s.warn(name, term, err)
			return Scene{}, false
		}
		for _, candidate := range found {
			if scene := FromStashBox(name, candidate); sameScene(scene, hint) {
				return scene, true
			}
		}
	}
	return Scene{}, false
}

// askMovie asks ThePornDB's films, by the id a scene lookup did not know or by the name.
func (s Service) askMovie(ctx context.Context, ids IDs, hint Hint) (Scene, bool) {
	if s.Movies == nil || !s.Movies.Configured() {
		return Scene{}, false
	}
	if ids.ThePornDB != "" {
		found, err := s.Movies.Movie(ctx, ids.ThePornDB)
		if err != nil {
			if !errors.Is(err, theporndb.ErrNotFound) {
				s.warn(ProviderThePornDB, ids.ThePornDB, err)
			}
			return Scene{}, false
		}
		return FromMovie(found), true
	}
	if hint.Title == "" {
		return Scene{}, false
	}
	for _, term := range searchTerms(hint) {
		found, err := memoized(ctx, s.Memo, ProviderThePornDB+":movies", term, func() ([]theporndb.Movie, error) { return s.Movies.SearchMovies(ctx, term, searchLimit) })
		if err != nil {
			s.warn(ProviderThePornDB, term, err)
			return Scene{}, false
		}
		for _, candidate := range found {
			if scene := FromMovie(candidate); sameScene(scene, hint) {
				return scene, true
			}
		}
	}
	return Scene{}, false
}

func (s Service) askWhisparr(ctx context.Context, ids IDs, hint Hint) (Scene, bool) {
	if s.Whisparr == nil || hint.Title == "" {
		return Scene{}, false
	}
	for _, term := range searchTerms(hint) {
		found, err := memoized(ctx, s.Memo, sourceWhisparr, term, func() ([]whisparr.Scene, error) { return s.Whisparr.SearchScenes(ctx, term) })
		if err != nil {
			s.warn(sourceWhisparr, term, err)
			return Scene{}, false
		}
		for _, candidate := range found {
			scene := FromWhisparr(candidate)
			if (ids.ThePornDB != "" && ids.ThePornDB == candidate.ForeignGUID) || (ids.ThePornDB == "" && sameScene(scene, hint)) {
				s.nameWhisparrSite(ctx, &scene, candidate.SiteID, hint.Studio)
				return scene, true
			}
		}
	}
	return Scene{}, false
}

// nameWhisparrSite gives a scene Whisparr's server answered the studio of its site, which that server names only by number:
// the site found under the studio's name that holds that number. One lookup serves every scene of the studio.
func (s Service) nameWhisparrSite(ctx context.Context, scene *Scene, siteID int, studio string) {
	if siteID == 0 || studio == "" || scene.Studio != "" {
		return
	}
	sites, err := memoized(ctx, s.Memo, sourceWhisparr+":sites", studio, func() ([]whisparr.Site, error) { return s.Whisparr.SearchSites(ctx, studio) })
	if err != nil {
		s.warn(sourceWhisparr, studio, err)
		return
	}
	for _, site := range sites {
		if site.ForeignID == siteID {
			scene.Studio, scene.Network = strings.TrimSpace(site.Title), strings.TrimSpace(site.Network)
			return
		}
	}
}

func idOf(ids IDs, provider string) string {
	switch provider {
	case ProviderStashDB:
		return ids.StashDB
	case ProviderThePornDB:
		return ids.ThePornDB
	case ProviderFansDB:
		return ids.FansDB
	}
	return ""
}

// searchTerms are the words a name is searched by, the studio and title first and the title alone after.
func searchTerms(hint Hint) []string {
	if hint.Studio == "" {
		return []string{hint.Title}
	}
	return []string{hint.Studio + " " + hint.Title, hint.Title}
}

// sameScene says a found scene is the one the hint names: the same title, or one holding the other on the same day.
func sameScene(scene Scene, hint Hint) bool {
	if hint.Date != "" && scene.Date != "" && hint.Date != scene.Date {
		return false
	}
	found, wanted := NameKey(scene.Title), NameKey(hint.Title)
	if found == "" || wanted == "" {
		return false
	}
	if found == wanted {
		return true
	}
	return hint.Date != "" && hint.Date == scene.Date && (strings.Contains(found, wanted) || strings.Contains(wanted, found))
}

// Candidate is one scene a search found.
type Candidate struct {
	IDs        IDs
	Title      string
	Studio     string
	Date       string
	Year       int
	Performers []string
	// DurationSec is the scene's length as the provider gives it, 0 when it gives none.
	DurationSec int
	Overview    string
	Cover       string
	Traits      media.Traits
	// Rank is the candidate's place in its own provider's answer, from 0.
	Rank int
	// Listed is the scene as its provider listed it, which adding the candidate need not ask for again.
	Listed Scene
}

// Query is what a search is for: the words and, when known, the studio and day.
type Query struct {
	Title, Studio, Date string
	// DayOnly asks a server that can list the scenes of a day only for that, skipping its slower, flakier text search.
	DayOnly bool
	// ScenesOnly leaves out ThePornDB's films, which only a library of whole films wants.
	ScenesOnly bool
}

// lookup is one provider's share of a search: the words it is asked, which may carry the studio, and the title and day alone.
type lookup struct {
	term, title, date   string
	dayOnly, scenesOnly bool
}

// Search finds scenes by their words at every provider of the library's order, each in its own ranking and at most limit of
// them, one scene found at
// several providers once with the ids of all. A provider that fails is skipped; only all failing is an error.
func (s Service) Search(ctx context.Context, categoryID int64, query Query, limit int) ([]Candidate, error) {
	return s.SearchUntil(ctx, categoryID, query, limit, nil)
}

// SearchUntil is Search that stops asking once enough says what has been found settles it: a scan placing a file has no
// use for the other providers' lists, and asks each of them again for the scene's fields when it adds it.
func (s Service) SearchUntil(ctx context.Context, categoryID int64, query Query, limit int, enough func([]Candidate) bool) ([]Candidate, error) {
	title := strings.TrimSpace(query.Title)
	if title == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = searchLimit
	}
	var failure error
	asked := 0
	var found []Candidate
	for _, name := range s.order(categoryID) {
		patient, cancel := context.WithTimeout(ctx, providerPatience)
		question := lookup{term: strings.TrimSpace(strings.Join([]string{query.Studio, title}, " ")), title: title, date: query.Date, dayOnly: query.DayOnly, scenesOnly: query.ScenesOnly}
		scenes, err := s.searchOne(patient, name, question, limit)
		if err == nil && len(scenes) == 0 && query.Studio != "" {
			question.term = title
			scenes, err = s.searchOne(patient, name, question, limit)
		}
		cancel()
		if err != nil {
			failure = err
			s.warn(name, title, err)
			continue
		}
		asked++
		found = mergeCandidates(found, candidatesOf(scenes, limit))
		if enough != nil && enough(found) {
			break
		}
	}
	if asked == 0 && failure != nil {
		return nil, fmt.Errorf("adult: search %q: %w", title, failure)
	}
	return found, nil
}

// mergeCandidates appends what one provider found: a scene already listed, of the same title and day, only adds its ids.
func mergeCandidates(listed, more []Candidate) []Candidate {
	for _, candidate := range more {
		at := slices.IndexFunc(listed, func(seen Candidate) bool {
			return seen.Date != "" && seen.Date == candidate.Date && NameKey(seen.Title) == NameKey(candidate.Title)
		})
		if at >= 0 {
			listed[at].IDs = mergeIDs(listed[at].IDs, candidate.IDs)
			continue
		}
		listed = append(listed, candidate)
	}
	return listed
}

func (s Service) searchOne(ctx context.Context, name string, asked lookup, limit int) ([]Scene, error) {
	term, title := asked.term, asked.title
	if name == sourceWhisparr {
		if s.Whisparr == nil {
			return nil, nil
		}
		found, err := memoized(ctx, s.Memo, sourceWhisparr, term, func() ([]whisparr.Scene, error) { return s.Whisparr.SearchScenes(ctx, term) })
		scenes := make([]Scene, 0, len(found))
		for _, scene := range found {
			scenes = append(scenes, FromWhisparr(scene))
		}
		return scenes, err
	}
	server, ok := s.Servers[name]
	if !ok || server == nil || !server.Configured() {
		return nil, nil
	}
	scenes, listed := s.onDate(ctx, name, server, title, asked.date, limit)
	if listed && (asked.dayOnly || slices.ContainsFunc(scenes, func(scene Scene) bool { return NameKey(scene.Title) == NameKey(title) })) {
		return scenes, nil
	}
	found, err := memoized(ctx, s.Memo, name, term, func() ([]stashbox.Scene, error) { return server.SearchScenes(ctx, term, limit) })
	for _, scene := range found {
		scenes = append(scenes, FromStashBox(name, scene))
	}
	if name == ProviderThePornDB && !asked.scenesOnly && s.Movies != nil && s.Movies.Configured() {
		films, filmErr := memoized(ctx, s.Memo, ProviderThePornDB+":movies", term, func() ([]theporndb.Movie, error) { return s.Movies.SearchMovies(ctx, term, limit) })
		for _, film := range films {
			scenes = append(scenes, FromMovie(film))
		}
		err = errors.Join(err, filmErr)
	}
	return scenes, err
}

// daysPerRequest is how many titles of a day one request asks a server for.
const daysPerRequest = 20

// PrefetchDays asks the servers of the library's order for the scenes of each title's day, a request to a server for every
// twenty, and remembers the answers: the lookups of the files that follow then cost the servers nothing.
func (s Service) PrefetchDays(ctx context.Context, categoryID int64, days []Hint) {
	for _, name := range s.order(categoryID) {
		server, ok := s.Servers[name]
		if !ok || server == nil || !server.Configured() {
			continue
		}
		var missing []stashbox.DayQuery
		for _, day := range days {
			if day.Date != "" && !memoHolds(s.Memo, name+":"+day.Date, day.Title) && !slices.Contains(missing, stashbox.DayQuery{Title: day.Title, Date: day.Date}) {
				missing = append(missing, stashbox.DayQuery{Title: day.Title, Date: day.Date})
			}
		}
		for chunk := range slices.Chunk(missing, daysPerRequest) {
			patient, cancel := context.WithTimeout(ctx, providerPatience)
			lists, err := server.ScenesOnDates(patient, chunk, searchLimit)
			cancel()
			if err != nil {
				if !errors.Is(err, stashbox.ErrUnsupported) {
					s.warn(name, "scenes of a batch's days", err)
				}
				break
			}
			for i, asked := range chunk {
				memoStore(s.Memo, name+":"+asked.Date, asked.Title, lists[i])
			}
		}
	}
}

// onDate lists the scenes the server holds of that day under the words, first in the ranking: a studio's numbered
// scenes ("Title #04" on twenty days) fill a text search's top ten with the wrong days. listed is false for a server
// without the filter, one that failed, or no day: it is then asked by text alone.
func (s Service) onDate(ctx context.Context, name string, server Server, title, date string, limit int) (scenes []Scene, listed bool) {
	if date == "" {
		return nil, false
	}
	found, err := memoized(ctx, s.Memo, name+":"+date, title, func() ([]stashbox.Scene, error) { return server.ScenesOnDate(ctx, title, date, limit) })
	if err != nil {
		if !errors.Is(err, stashbox.ErrUnsupported) {
			s.warn(name, title, err)
		}
		return nil, false
	}
	scenes = make([]Scene, 0, len(found))
	for _, scene := range found {
		scenes = append(scenes, FromStashBox(name, scene))
	}
	return scenes, true
}

func candidatesOf(scenes []Scene, limit int) []Candidate {
	if len(scenes) > limit {
		scenes = scenes[:limit]
	}
	out := make([]Candidate, 0, len(scenes))
	for rank, scene := range scenes {
		candidate := Candidate{Rank: rank, Listed: scene, IDs: scene.IDs, Title: scene.Title, Studio: scene.Studio, Date: scene.Date, Year: scene.Year,
			Performers: scene.Performers(), DurationSec: scene.DurationSec, Overview: scene.Overview, Traits: media.Traits{Genres: scene.Tags}}
		if len(scene.Images) > 0 {
			candidate.Cover = scene.Images[0].URL
		}
		out = append(out, candidate)
	}
	return out
}

// fingerprintsPerQuery is the most files one stash-box query names; Stash itself sends 40 at a time.
const fingerprintsPerQuery = 40

// ByFingerprints finds the scene of each oshash, one query per server and 40 files, asking a later server only about what
// the earlier ones did not know. A hash that no server knows, or that names more than one scene, is absent.
func (s Service) ByFingerprints(ctx context.Context, categoryID int64, oshashes []string) map[string]Scene {
	found := map[string]Scene{}
	left := slices.Compact(slices.Sorted(slices.Values(oshashes)))
	left = slices.DeleteFunc(left, func(hash string) bool { return hash == "" })
	for _, name := range s.order(categoryID) {
		server, ok := s.Servers[name]
		if !ok || server == nil || !server.Configured() || len(left) == 0 {
			continue
		}
		for chunk := range slices.Chunk(left, fingerprintsPerQuery) {
			files := make([][]stashbox.Fingerprint, len(chunk))
			for i, hash := range chunk {
				files[i] = []stashbox.Fingerprint{{Hash: hash, Algorithm: stashbox.AlgorithmOSHash}}
			}
			answers, err := server.ScenesByFingerprint(ctx, files)
			if err != nil {
				s.warn(name, "fingerprint", err)
				continue
			}
			for i, answer := range answers {
				if i < len(chunk) && len(answer) == 1 {
					found[chunk[i]] = FromStashBox(name, answer[0])
				}
			}
		}
		left = slices.DeleteFunc(left, func(hash string) bool { _, known := found[hash]; return known })
	}
	return found
}

// MonitorOption is Whisparr's "Monitor" choice when a scene is added.
type MonitorOption string

// The scene, or nothing.
const (
	MonitorScene MonitorOption = "scene_only"
	MonitorNone  MonitorOption = "none"
)

// Valid reports whether the option is one of the two.
func (o MonitorOption) Valid() bool { return o == MonitorScene || o == MonitorNone }

// AddRequest is one scene a person asked to hold.
type AddRequest struct {
	IDs        IDs
	Hint       Hint
	CategoryID int64
	ProfileID  int64
	Monitor    MonitorOption
	AddedBy    *int64
	// Fallback is stored when no metadata source knows the scene; nil refuses the add then.
	Fallback *Scene
	// Quick adds what placing a file needs; the scene's refresh asks the remaining providers.
	Quick bool
	// Seed is the scene as a provider already listed it, taken for that provider's answer.
	Seed *Scene
}

// AddResult is what an add stored.
type AddResult struct {
	WorkID  int64
	ItemID  int64
	Created bool
	Scene   Scene
}

// Add stores a scene and its one wanted item. Adding what the library already holds is not an error: the scene is
// refreshed and its item left as it is (Invariant 71).
func (s Service) Add(ctx context.Context, request AddRequest) (AddResult, error) {
	monitor := request.Monitor
	if monitor == "" {
		monitor = MonitorScene
	}
	if !monitor.Valid() {
		return AddResult{}, fmt.Errorf("adult: unknown monitor option %q", monitor)
	}
	scene, err := s.fetch(ctx, request.CategoryID, request.IDs, request.Hint, request.Quick, request.Seed)
	if errors.Is(err, ErrNoSource) && request.Fallback != nil {
		scene, err = *request.Fallback, nil
	}
	if err != nil {
		return AddResult{}, err
	}
	now := s.Clock.Now()
	stored, err := s.Series.Upsert(ctx, now, ToWrite(scene, now))
	if err != nil {
		return AddResult{}, err
	}
	item, err := s.Desired.EnsureAlbumItem(ctx, library.AlbumRequest{
		WorkID: stored.WorkID, CategoryID: request.CategoryID, ProfileID: request.ProfileID,
		Monitored: monitor != MonitorNone, AddedBy: request.AddedBy, RequiredWithoutParts: 1,
	})
	if err != nil {
		return AddResult{}, fmt.Errorf("adult: wanted item: %w", err)
	}
	return AddResult{WorkID: stored.WorkID, ItemID: item.ID, Created: stored.Created, Scene: scene}, nil
}

// Refresh reads a scene the library holds again. Monitoring and the person's own fields are untouched.
func (s Service) Refresh(ctx context.Context, categoryID, workID int64) (Scene, error) {
	provider, err := s.Reader.ProviderIDs(ctx, workID)
	if err != nil {
		return Scene{}, err
	}
	// No provider holds the whole of a film built from its scenes: asked, it would answer "Scene 1".
	if _, film := provider[ProviderFilm]; film {
		return Scene{}, nil
	}
	basics, err := s.Reader.Basics(ctx, workID)
	if err != nil {
		return Scene{}, err
	}
	scene, err := s.FetchFor(ctx, categoryID, idsOf(provider), Hint{Title: basics.Title})
	if err != nil {
		return Scene{}, err
	}
	now := s.Clock.Now()
	if _, err := s.Series.Upsert(ctx, now, ToWrite(scene, now)); err != nil {
		return Scene{}, err
	}
	return scene, nil
}

// ApplyMonitor sets a scene's monitoring as Whisparr's Monitor choice says.
func (s Service) ApplyMonitor(ctx context.Context, workID int64, option MonitorOption) error {
	if !option.Valid() {
		return fmt.Errorf("adult: unknown monitor option %q", option)
	}
	items, err := s.Desired.AlbumItemsForWork(ctx, workID)
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := s.Desired.SetItemMonitored(ctx, item.ID, option != MonitorNone); err != nil {
			return err
		}
	}
	return nil
}

// IsExcluded reports whether a person removed this scene and asked not to see it again.
func (s Service) IsExcluded(ctx context.Context, ids IDs) bool {
	excluded, err := s.Exclusions.Excluded(ctx, idWrites(ids))
	if err != nil {
		s.warn("exclusion check", ids.StashDB+ids.ThePornDB+ids.FansDB, err)
		return false
	}
	return excluded
}

// WorkByIDs is the scene the library holds under any of the ids.
func (s Service) WorkByIDs(ctx context.Context, ids IDs) (int64, bool) {
	for _, write := range idWrites(ids) {
		if id, held, err := s.Series.WorkByProviderID(ctx, MediaType, write.Provider, write.ExternalID); err == nil && held {
			return id, true
		}
	}
	return 0, false
}

// PerformerHit is a performer a search found, with the server that knows them.
type PerformerHit struct {
	Server    string
	Performer stashbox.Performer
}

// SearchPerformers finds performers by name or alias at the first server of the library's order that has any.
func (s Service) SearchPerformers(ctx context.Context, categoryID int64, term string, limit int) ([]PerformerHit, error) {
	term = strings.TrimSpace(term)
	if term == "" {
		return nil, nil
	}
	var failure error
	for _, name := range s.order(categoryID) {
		server, ok := s.Servers[name]
		if !ok || server == nil || !server.Configured() {
			continue
		}
		found, err := server.SearchPerformers(ctx, term, limit)
		if err != nil {
			failure = err
			s.warn(name, term, err)
			continue
		}
		hits := make([]PerformerHit, 0, len(found))
		for _, performer := range found {
			if performer.MergedIntoID == "" {
				hits = append(hits, PerformerHit{Server: name, Performer: performer})
			}
		}
		if len(hits) > 0 {
			return hits, nil
		}
	}
	if failure != nil {
		return nil, fmt.Errorf("adult: search performers %q: %w", term, failure)
	}
	return nil, nil
}
