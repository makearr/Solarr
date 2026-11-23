package comic

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// Service looks comic series up at the providers of a library's order, stores them and keeps them current.
type Service struct {
	Sources
	// KindOf answers a library's kind; ProvidersOf the enabled providers of its order, best first.
	KindOf      func(categoryID int64) string
	ProvidersOf func(categoryID int64) []string
	// OptionOf answers one of the library's own settings (module options), or the fallback while it has none.
	OptionOf   func(categoryID int64, key, fallback string) string
	Series     library.Series
	Exclusions library.Exclusions
	Reader     library.SeriesReader
	Desired    library.Desired
	Clock      clock.Clock
	// ProfileFor is the quality profile a library's new wanted items take; nil leaves a series without items until one exists.
	ProfileFor func(categoryID int64) int64
}

func (s Service) kind(categoryID int64) string {
	if s.KindOf == nil {
		return KindComics
	}
	return s.KindOf(categoryID)
}

func (s Service) option(categoryID int64, key, fallback string) string {
	if s.OptionOf == nil {
		return fallback
	}
	return s.OptionOf(categoryID, key, fallback)
}

func (s Service) coverDates(categoryID int64) bool {
	return s.option(categoryID, string(media.FileDateType), DateTypeStore) == DateTypeCover
}

func (s Service) order(categoryID int64) []string {
	if s.ProvidersOf != nil {
		return s.ProvidersOf(categoryID)
	}
	return ProviderKeys(s.kind(categoryID))
}

// Fetch reads a series from the library's providers.
func (s Service) Fetch(ctx context.Context, categoryID int64, ids map[string]string, hint Hint) (Series, error) {
	return s.fetch(ctx, s.kind(categoryID), s.order(categoryID), ids, hint)
}

// Candidate is one series a lookup found, with the ids every provider that named it gave.
type Candidate struct {
	// Provider and ID are what an add names it by: the first provider of the order that knows it.
	Provider, ID, Title, Publisher, Overview, Cover string
	Year, Count                                     int
	Adult                                           bool
	IDs                                             map[string]string
}

// Search looks a title up at every provider of the library's order, one candidate per series: a provider that names
// another's id (Metron's ComicVine id, MangaDex's AniList id) joins that candidate. An adult result is left out of a
// kind that is not adult.
func (s Service) Search(ctx context.Context, categoryID int64, query string, limit int) ([]Candidate, error) {
	kind := s.kind(categoryID)
	var found []Candidate
	add := func(candidate Candidate) {
		for i := range found {
			if joins(found[i].IDs, candidate.IDs) {
				for provider, id := range candidate.IDs {
					if found[i].IDs[provider] == "" {
						found[i].IDs[provider] = id
					}
				}
				found[i].Overview = firstNonEmpty(found[i].Overview, candidate.Overview)
				found[i].Cover = firstNonEmpty(found[i].Cover, candidate.Cover)
				found[i].Adult = found[i].Adult || candidate.Adult
				return
			}
		}
		found = append(found, candidate)
	}
	for _, key := range s.order(categoryID) {
		for _, candidate := range s.searchOne(ctx, key, kind, query) {
			add(candidate)
		}
	}
	var out []Candidate
	for _, candidate := range found {
		if candidate.Adult && !isAdultKind(kind) {
			continue
		}
		candidate.Provider, candidate.ID = firstID(candidate.IDs, s.order(categoryID))
		if candidate.ID == "" {
			continue
		}
		out = append(out, candidate)
	}
	if len(out) == 0 && len(found) == 0 && !s.anyAnswered(ctx, categoryID) {
		return nil, ErrNoSource
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// anyAnswered says a provider of the order is switched on and wired, so an empty search is "nothing found", not "no source".
func (s Service) anyAnswered(_ context.Context, categoryID int64) bool {
	for _, key := range s.order(categoryID) {
		switch {
		case key == settings.ComicSourceComicVine && s.ComicVine != nil && s.ComicVine.Configured(),
			key == settings.ComicSourceMetron && s.Metron != nil && s.Metron.Configured(),
			key == settings.ComicSourceGCD && s.GCD != nil,
			key == settings.TVSourceAniList && s.AniList != nil,
			key == settings.ComicSourceMangaUpdates && s.MangaUpdates != nil,
			key == settings.ComicSourceMangaDex && s.MangaDex != nil,
			s.recordProvider(key) != nil:
			return true
		}
	}
	return false
}

func joins(held, other map[string]string) bool {
	for provider, id := range other {
		if id != "" && held[provider] == id {
			return true
		}
	}
	return false
}

func firstID(ids map[string]string, order []string) (provider, id string) {
	keys := map[string]string{
		settings.ComicSourceComicVine: ProviderComicVine, settings.ComicSourceMetron: ProviderMetron, settings.ComicSourceGCD: ProviderGCD,
		settings.TVSourceAniList: ProviderAniListManga, settings.ComicSourceMangaUpdates: ProviderMangaUpdates, settings.ComicSourceMangaDex: ProviderMangaDex,
	}
	for _, key := range order {
		name, compiled := keys[key]
		if !compiled {
			name = key
		}
		if ids[name] != "" {
			return name, ids[name]
		}
	}
	return "", ""
}

func (s Service) searchOne(ctx context.Context, key, kind, query string) []Candidate {
	var out []Candidate
	switch key {
	case settings.ComicSourceComicVine:
		if s.ComicVine == nil || !s.ComicVine.Configured() || isMangaKind(kind) {
			return nil
		}
		found, err := s.ComicVine.SearchSeries(ctx, query, 0)
		if err != nil {
			s.warn(key, query, err)
		}
		for _, one := range found {
			out = append(out, Candidate{Title: one.Name, Publisher: one.Publisher, Overview: stripMarkup(one.Description), Cover: one.CoverURL, Year: one.StartYear, Count: one.IssueCount,
				IDs: map[string]string{ProviderComicVine: strconv.Itoa(one.ID)}})
		}
	case settings.ComicSourceMetron:
		if s.Metron == nil || !s.Metron.Configured() || isMangaKind(kind) {
			return nil
		}
		found, err := s.Metron.SearchSeries(ctx, query, 0)
		if err != nil {
			s.warn(key, query, err)
		}
		for _, one := range found {
			ids := map[string]string{ProviderMetron: strconv.Itoa(one.ID)}
			if one.ComicVineID > 0 {
				ids[ProviderComicVine] = strconv.Itoa(one.ComicVineID)
			}
			if one.GCDID > 0 {
				ids[ProviderGCD] = strconv.Itoa(one.GCDID)
			}
			out = append(out, Candidate{Title: one.Name, Publisher: one.Publisher, Overview: one.Summary, Year: one.YearBegan, Count: one.IssueCount, IDs: ids})
		}
	case settings.ComicSourceGCD:
		if s.GCD == nil || isMangaKind(kind) {
			return nil
		}
		found, _, err := s.GCD.SearchSeries(ctx, query, 1)
		if err != nil {
			s.warn(key, query, err)
		}
		for _, one := range found {
			out = append(out, Candidate{Title: one.Name, Year: one.YearBegan, IDs: map[string]string{ProviderGCD: strconv.Itoa(one.ID)}})
		}
	case settings.TVSourceAniList:
		if s.AniList == nil || !isMangaKind(kind) {
			return nil
		}
		found, _, err := s.AniList.SearchManga(ctx, query, 1)
		if err != nil {
			s.warn(key, query, err)
		}
		for _, one := range found {
			ids := map[string]string{ProviderAniListManga: strconv.FormatInt(one.ID, 10)}
			if one.IDMal > 0 {
				ids[ProviderMAL] = strconv.FormatInt(one.IDMal, 10)
			}
			out = append(out, Candidate{Title: firstNonEmpty(one.Title.English, one.Title.Romaji, one.Title.Native), Overview: stripMarkup(one.Description),
				Cover: one.CoverImage.ExtraLarge, Year: one.StartDate.Year, Count: one.Volumes, Adult: one.IsAdult, IDs: ids})
		}
	case settings.ComicSourceMangaUpdates:
		if s.MangaUpdates == nil || !isMangaKind(kind) {
			return nil
		}
		found, _, err := s.MangaUpdates.SearchSeries(ctx, query, 1)
		if err != nil {
			s.warn(key, query, err)
		}
		for _, one := range found {
			out = append(out, Candidate{Title: one.Title, Overview: stripMarkup(one.Description), Cover: one.CoverURL, Year: one.Year, Count: one.Volumes,
				IDs: map[string]string{ProviderMangaUpdates: strconv.FormatInt(one.ID, 10)}})
		}
	case settings.ComicSourceMangaDex:
		if s.MangaDex == nil || !isMangaKind(kind) {
			return nil
		}
		ratings := mangaDexRatings.safe
		if isAdultKind(kind) {
			ratings = mangaDexRatings.adult
		}
		found, _, err := s.MangaDex.SearchManga(ctx, query, 1, ratings)
		if err != nil {
			s.warn(key, query, err)
		}
		for _, one := range found {
			ids := map[string]string{ProviderMangaDex: one.ID}
			if one.Links["al"] != "" {
				ids[ProviderAniListManga] = one.Links["al"]
			}
			if one.Links["mal"] != "" {
				ids[ProviderMAL] = one.Links["mal"]
			}
			out = append(out, Candidate{Title: one.Title, Overview: stripMarkup(one.Description), Year: one.Year, Count: one.LastVolume,
				Adult: one.ContentRating == "pornographic", IDs: ids})
		}
	default:
		out = s.searchRecords(ctx, key, query)
	}
	return out
}

// AddRequest is one add.
type AddRequest struct {
	IDs        map[string]string
	Hint       Hint
	CategoryID int64
	ProfileID  int64
	Monitor    MonitorOption
	AddedBy    *int64
	// Fallback is stored when no provider knows the series; nil refuses the add then.
	Fallback *Series
}

// AddResult is what an add stored.
type AddResult struct {
	WorkID  int64
	Created bool
	Series  Series
}

// Add stores a series, its parts and its one wanted item. Adding what the library already holds is not an error: the
// series is read again and its item left as it is (Invariant 71).
func (s Service) Add(ctx context.Context, request AddRequest) (AddResult, error) {
	monitor := request.Monitor
	if monitor == "" {
		monitor = MonitorAll
	}
	if !monitor.Valid() {
		return AddResult{}, fmt.Errorf("comic: unknown monitor option %q", monitor)
	}
	series, err := s.Fetch(ctx, request.CategoryID, request.IDs, request.Hint)
	if errors.Is(err, ErrNoSource) && request.Fallback != nil {
		series, err = *request.Fallback, nil
	}
	if err != nil {
		return AddResult{}, err
	}
	if series.Adult && !isAdultKind(s.kind(request.CategoryID)) {
		return AddResult{}, fmt.Errorf("%w: %q is adult content, which only an adult kind of library takes", media.ErrInvalidAdd, series.Title)
	}
	now := s.Clock.Now()
	stored, err := s.Series.Upsert(ctx, now, ToWrite(series, WriteOptions{Monitor: monitor, Now: now, CoverDate: s.coverDates(request.CategoryID)}))
	if err != nil {
		return AddResult{}, err
	}
	if err := s.ensureItems(ctx, stored.WorkID, request.CategoryID, request.ProfileID, request.AddedBy, series.primaryKind(), series.Parts, Monitored(series.Parts, monitor, now)); err != nil {
		return AddResult{}, err
	}
	return AddResult{WorkID: stored.WorkID, Created: stored.Created, Series: series}, nil
}

// ensureItems gives the series one wanted item for each kind of part it holds (issues, annuals, volumes, loose chapters),
// as a TV series has one per season: a file covers parts of the item its kind names. An item that exists keeps its
// own monitoring (Invariant 71).
func (s Service) ensureItems(ctx context.Context, workID, categoryID, profileID int64, addedBy *int64, primary string, parts []Part, wanted map[string]bool) error {
	// The series' main kind of part always has an item, so a series whose parts no provider lists yet is still in its library.
	monitored := map[string]bool{}
	if primary != "" {
		monitored[primary] = false
	}
	for _, part := range parts {
		monitored[part.Kind] = monitored[part.Kind] || wanted[part.Ref()]
	}
	if profileID == 0 && len(monitored) > 0 {
		return nil
	}
	for kind, any := range monitored {
		if _, err := s.Desired.EnsureSeasonItem(ctx, library.SeasonRequest{
			WorkID: workID, MediaRef: kind, SortKey: SortKey(kind, "0"), CategoryID: categoryID, ProfileID: profileID, Monitored: any, AddedBy: addedBy,
		}); err != nil {
			return fmt.Errorf("comic: wanted item: %w", err)
		}
	}
	return nil
}

// profileOf is the profile the series' items already follow, else the library's.
func (s Service) profileOf(ctx context.Context, workID, categoryID int64) int64 {
	if items, err := s.Desired.AlbumItemsForWork(ctx, workID); err == nil && len(items) > 0 {
		return items[0].ProfileID
	}
	if s.ProfileFor != nil {
		return s.ProfileFor(categoryID)
	}
	return 0
}

// Refresh reads a held series again. A part it creates is monitored when the series is; monitoring of the parts it
// already holds and the person's own fields are untouched.
func (s Service) Refresh(ctx context.Context, categoryID, workID int64) (Series, error) {
	provider, err := s.Reader.ProviderIDs(ctx, workID)
	if err != nil {
		return Series{}, err
	}
	basics, err := s.Reader.Basics(ctx, workID)
	if err != nil {
		return Series{}, err
	}
	series, err := s.Fetch(ctx, categoryID, provider, Hint{Title: basics.Title, Year: int(basics.Year)})
	if err != nil {
		return Series{}, err
	}
	for _, failed := range series.Failed {
		if holdsID(provider, failed) {
			return Series{}, fmt.Errorf("comic %d: refresh skipped, %s did not answer: %w", workID, failed, media.ErrSourceUnavailable)
		}
	}
	monitored, err := s.monitoredNow(ctx, workID)
	if err != nil {
		return Series{}, err
	}
	now := s.Clock.Now()
	if _, err := s.Series.Upsert(ctx, now, ToWrite(series, WriteOptions{Now: now, CoverDate: s.coverDates(categoryID), NewPart: func(part Part) bool { return monitored && part.Kind != PartChapter }})); err != nil {
		return Series{}, err
	}
	// A kind of part the series gains (its first annual) gets an item beside the others, under the same profile.
	kept := map[string]bool{}
	for _, part := range series.Parts {
		kept[part.Ref()] = monitored && part.Kind != PartChapter
	}
	if err := s.ensureItems(ctx, workID, categoryID, s.profileOf(ctx, workID, categoryID), nil, series.primaryKind(), series.Parts, kept); err != nil {
		return Series{}, err
	}
	return series, nil
}

func holdsID(ids map[string]string, source string) bool {
	switch source {
	case settings.ComicSourceComicVine:
		return ids[ProviderComicVine] != ""
	case settings.ComicSourceMetron:
		return ids[ProviderMetron] != ""
	case settings.ComicSourceGCD:
		return ids[ProviderGCD] != ""
	case settings.TVSourceAniList:
		return ids[ProviderAniListManga] != ""
	case settings.ComicSourceMangaUpdates:
		return ids[ProviderMangaUpdates] != ""
	case settings.ComicSourceMangaDex:
		return ids[ProviderMangaDex] != ""
	}
	return false
}

func (s Service) monitoredNow(ctx context.Context, workID int64) (bool, error) {
	parts, err := s.Desired.AllParts(ctx, workID)
	if err != nil {
		return false, err
	}
	for _, part := range parts {
		if part.Monitored != 0 {
			return true, nil
		}
	}
	return false, nil
}

// ApplyMonitor re-applies a monitor option to a series the library holds.
func (s Service) ApplyMonitor(ctx context.Context, workID int64, option MonitorOption) error {
	if !option.Valid() {
		return fmt.Errorf("comic: unknown monitor option %q", option)
	}
	parts, err := s.Desired.AllParts(ctx, workID)
	if err != nil {
		return err
	}
	held := make([]Part, len(parts))
	for i, part := range parts {
		kind, number, _ := strings.Cut(part.MediaRef, ":")
		held[i] = Part{Kind: kind, Number: number}
		if part.Date.Valid {
			held[i].Store = part.Date.String
		}
	}
	wants := Monitored(held, option, s.Clock.Now())
	byPart := make(map[int64]bool, len(parts))
	for i, part := range parts {
		byPart[part.ID] = wants[held[i].Ref()]
	}
	return s.Desired.ApplyPartMonitoring(ctx, workID, byPart)
}

// WorkByIDs finds the held series any of the provider ids names.
func (s Service) WorkByIDs(ctx context.Context, ids map[string]string) (int64, bool) {
	for _, provider := range []string{ProviderComicVine, ProviderMetron, ProviderGCD, ProviderAniListManga, ProviderMangaUpdates, ProviderMangaDex, ProviderMAL} {
		if ids[provider] == "" {
			continue
		}
		if id, found, err := s.Series.WorkByProviderID(ctx, MediaType, provider, ids[provider]); err == nil && found {
			return id, true
		}
	}
	return 0, false
}

// IsExcluded says a person removed a series by one of these ids and asked not to see it again.
func (s Service) IsExcluded(ctx context.Context, ids map[string]string) bool {
	excluded, err := s.Exclusions.Excluded(ctx, idWrites(ids))
	if err != nil {
		s.warn("exclusions", "", err)
		return false
	}
	return excluded
}

// EnsureParts makes the parts a manga's files name that no provider listed: a volume of a series whose count is unknown, a
// chapter no volume holds. Parts appear as files or providers reveal them.
func (s Service) EnsureParts(ctx context.Context, categoryID, workID int64, keys []library.PartKey) {
	var missing []Part
	for _, key := range keys {
		kind, number, _ := strings.Cut(key.MediaRef, ":")
		if kind != PartChapter && kind != PartVolume {
			continue
		}
		if _, absent, _ := s.Series.ResolveParts(ctx, workID, []library.PartKey{key}); absent > 0 {
			missing = append(missing, Part{Kind: kind, Number: number})
		}
	}
	if len(missing) == 0 {
		return
	}
	basics, err := s.Reader.Basics(ctx, workID)
	if err != nil {
		return
	}
	ids, err := s.Reader.ProviderIDs(ctx, workID)
	if err != nil {
		return
	}
	write := library.SeriesWrite{Work: library.WorkIdentity{MediaType: MediaType, CanonicalTitle: basics.Title, SortTitle: library.SortTitle(basics.Title)}, IDs: idWrites(ids), CertificationAge: -1}
	for _, part := range missing {
		write.Parts = append(write.Parts, partWrite(part, false, s.coverDates(categoryID)))
	}
	if _, err := s.Series.Upsert(ctx, s.Clock.Now(), write); err != nil {
		s.warn("parts", strconv.FormatInt(workID, 10), err)
		return
	}
	profile := s.profileOf(ctx, workID, categoryID)
	if err := s.ensureItems(ctx, workID, categoryID, profile, nil, "", missing, map[string]bool{}); err != nil {
		s.warn("part items", strconv.FormatInt(workID, 10), err)
	}
}
