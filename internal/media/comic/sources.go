package comic

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/metadata/anilist"
	"github.com/makearr/Solarr/internal/metadata/comicvine"
	"github.com/makearr/Solarr/internal/metadata/gcd"
	"github.com/makearr/Solarr/internal/metadata/mangadex"
	"github.com/makearr/Solarr/internal/metadata/mangaupdates"
	"github.com/makearr/Solarr/internal/metadata/metron"
)

// ErrNoSource means no provider could be asked or none knew the series.
var ErrNoSource = errors.New("comic: no metadata source answered")

// maxIssuePages bounds the pages read of one series' issue list: 100 issues a page on ComicVine.
const maxIssuePages = 40

// VineSource is the part of ComicVine's client the service reads.
type VineSource interface {
	Configured() bool
	SearchSeries(ctx context.Context, query string, year int) ([]comicvine.Series, error)
	Series(ctx context.Context, id int) (comicvine.Series, error)
	Issues(ctx context.Context, seriesID, offset int) ([]comicvine.Issue, bool, error)
}

// MetronSource is the part of Metron's client the service reads.
type MetronSource interface {
	Configured() bool
	SearchSeries(ctx context.Context, query string, year int) ([]metron.Series, error)
	Series(ctx context.Context, id int) (metron.Series, error)
	Issues(ctx context.Context, seriesID, page int) ([]metron.Issue, bool, error)
}

// GCDSource is the part of the Grand Comics Database's client the service reads.
type GCDSource interface {
	SearchSeries(ctx context.Context, name string, page int) ([]gcd.Series, bool, error)
	Series(ctx context.Context, id int) (gcd.Series, error)
}

// AniListSource is the manga part of AniList's client.
type AniListSource interface {
	Manga(ctx context.Context, id int64) (anilist.Manga, error)
	SearchManga(ctx context.Context, title string, page int) ([]anilist.Manga, bool, error)
}

// MangaUpdatesSource is the part of MangaUpdates' client the service reads.
type MangaUpdatesSource interface {
	Series(ctx context.Context, id int64) (mangaupdates.Series, error)
	SearchSeries(ctx context.Context, title string, page int) ([]mangaupdates.Series, bool, error)
}

// MangaDexSource is the part of MangaDex's client the service reads.
type MangaDexSource interface {
	SearchManga(ctx context.Context, title string, page int, ratings []string) ([]mangadex.Manga, bool, error)
	Manga(ctx context.Context, id string) (mangadex.Manga, error)
	Aggregate(ctx context.Context, id, language string) ([]mangadex.Volume, error)
}

// Hint is what a caller already knows of the series, used to find it where an id is missing.
type Hint struct {
	Title string
	Year  int
}

// Sources are the providers a service asks; a nil one is not asked.
type Sources struct {
	ComicVine    VineSource
	Metron       MetronSource
	GCD          GCDSource
	AniList      AniListSource
	MangaUpdates MangaUpdatesSource
	MangaDex     MangaDexSource
	// Records answers the definition-file provider of a key, nil for one that is not (or no longer) active.
	Records func(key string) RecordProvider
	Logger  *slog.Logger
}

func (s Sources) warn(source, id string, err error) {
	if s.Logger != nil {
		s.Logger.Warn("comic metadata source failed", slog.String("source", source), slog.String("id", id), slog.Any("error", err))
	}
}

// fetch reads a series from the providers of order, the first to hold a field supplying it.
func (s Sources) fetch(ctx context.Context, kind string, order []string, ids map[string]string, hint Hint) (Series, error) {
	series := Series{IDs: map[string]string{}, Manga: isMangaKind(kind)}
	for provider, id := range ids {
		series.IDs[provider] = id
	}
	if series.Manga {
		s.fetchManga(ctx, &series, order, hint)
	} else {
		s.fetchComic(ctx, &series, order, hint)
	}
	if len(series.Sources) == 0 {
		return Series{}, ErrNoSource
	}
	if series.Status == "" {
		series.Status = "continuing"
	}
	return series, nil
}

func (s Sources) fetchComic(ctx context.Context, series *Series, order []string, hint Hint) {
	for _, key := range order {
		switch key {
		case settings.ComicSourceComicVine:
			s.askComicVine(ctx, series, hint)
		case settings.ComicSourceMetron:
			s.askMetron(ctx, series, hint)
		case settings.ComicSourceGCD:
			s.askGCD(ctx, series, hint)
		default:
			s.askRecords(ctx, series, key, hint)
		}
	}
}

func (s Sources) askComicVine(ctx context.Context, series *Series, hint Hint) {
	if s.ComicVine == nil || !s.ComicVine.Configured() {
		return
	}
	id, _ := strconv.Atoi(series.IDs[ProviderComicVine])
	if id == 0 {
		title := firstNonEmpty(hint.Title, series.Title)
		if title == "" {
			return
		}
		found, err := s.ComicVine.SearchSeries(ctx, title, hint.Year)
		if err != nil {
			s.warn(settings.ComicSourceComicVine, title, err)
			return
		}
		for _, candidate := range found {
			if sameTitle(candidate.Name, title) && (hint.Year == 0 || candidate.StartYear == hint.Year) {
				id = candidate.ID
				break
			}
		}
		if id == 0 {
			return
		}
	}
	held, err := s.ComicVine.Series(ctx, id)
	if err != nil {
		s.warn(settings.ComicSourceComicVine, strconv.Itoa(id), err)
		series.Failed = append(series.Failed, settings.ComicSourceComicVine)
		return
	}
	series.IDs[ProviderComicVine] = strconv.Itoa(held.ID)
	series.fill(held.Name, held.Description, held.Publisher, held.CoverURL, held.StartYear, 0)
	series.Sources = append(series.Sources, settings.ComicSourceComicVine)
	var parts []Part
	for page := 0; page < maxIssuePages; page++ {
		issues, more, err := s.ComicVine.Issues(ctx, id, page*comicvine.PageSize)
		if err != nil {
			s.warn(settings.ComicSourceComicVine, strconv.Itoa(id), err)
			series.Failed = append(series.Failed, settings.ComicSourceComicVine)
			return
		}
		for _, issue := range issues {
			parts = append(parts, Part{
				Kind: partKind(issue.Number), Number: issue.Number, Title: issue.Title, CoverDate: issue.CoverDate, Store: issue.StoreDate,
				Cover: issue.CoverURL, IDs: map[string]string{ProviderComicVine: strconv.Itoa(issue.ID)},
			})
		}
		if !more {
			break
		}
	}
	series.mergeParts(parts)
	if series.Format == "" {
		series.Format = FormatSeries
	}
}

func (s Sources) askMetron(ctx context.Context, series *Series, hint Hint) {
	if s.Metron == nil || !s.Metron.Configured() {
		return
	}
	id, _ := strconv.Atoi(series.IDs[ProviderMetron])
	if id == 0 {
		title := firstNonEmpty(series.Title, hint.Title)
		if title == "" {
			return
		}
		found, err := s.Metron.SearchSeries(ctx, title, firstInt(series.Year, hint.Year))
		if err != nil {
			s.warn(settings.ComicSourceMetron, title, err)
			return
		}
		for _, candidate := range found {
			bridged := series.IDs[ProviderComicVine] != "" && strconv.Itoa(candidate.ComicVineID) == series.IDs[ProviderComicVine]
			if bridged || (series.IDs[ProviderComicVine] == "" && sameTitle(candidate.Name, title)) {
				id = candidate.ID
				break
			}
		}
		if id == 0 {
			return
		}
	}
	held, err := s.Metron.Series(ctx, id)
	if err != nil {
		s.warn(settings.ComicSourceMetron, strconv.Itoa(id), err)
		series.Failed = append(series.Failed, settings.ComicSourceMetron)
		return
	}
	series.IDs[ProviderMetron] = strconv.Itoa(held.ID)
	if held.ComicVineID > 0 && series.IDs[ProviderComicVine] == "" {
		series.IDs[ProviderComicVine] = strconv.Itoa(held.ComicVineID)
	}
	if held.GCDID > 0 && series.IDs[ProviderGCD] == "" {
		series.IDs[ProviderGCD] = strconv.Itoa(held.GCDID)
	}
	series.fill(held.Name, held.Summary, held.Publisher, "", held.YearBegan, held.YearEnded)
	if series.Format == "" {
		series.Format = metronFormat(held.Type)
	}
	if series.Status == "" {
		series.Status = strings.ToLower(held.Status)
	}
	series.Sources = append(series.Sources, settings.ComicSourceMetron)
	var parts []Part
	for page := 1; page <= maxIssuePages; page++ {
		issues, more, err := s.Metron.Issues(ctx, id, page)
		if err != nil {
			s.warn(settings.ComicSourceMetron, strconv.Itoa(id), err)
			series.Failed = append(series.Failed, settings.ComicSourceMetron)
			return
		}
		for _, issue := range issues {
			part := Part{
				Kind: partKind(issue.Number), Number: issue.Number, CoverDate: issue.CoverDate, Store: issue.StoreDate, Pages: issue.Pages,
				Rating: issue.Rating, Cover: issue.CoverURL, IDs: map[string]string{ProviderMetron: strconv.Itoa(issue.ID)},
			}
			if len(issue.Titles) > 0 {
				part.Title = issue.Titles[0]
			}
			parts = append(parts, part)
		}
		if !more {
			break
		}
	}
	series.mergeParts(parts)
}

func (s Sources) askGCD(ctx context.Context, series *Series, hint Hint) {
	if s.GCD == nil {
		return
	}
	id, _ := strconv.Atoi(series.IDs[ProviderGCD])
	if id == 0 {
		// The Grand Comics Database names a series another provider has not: it is asked by title only as a last resort.
		if len(series.Sources) > 0 {
			return
		}
		title := firstNonEmpty(series.Title, hint.Title)
		if title == "" {
			return
		}
		found, _, err := s.GCD.SearchSeries(ctx, title, 1)
		if err != nil {
			s.warn(settings.ComicSourceGCD, title, err)
			return
		}
		for _, candidate := range found {
			if sameTitle(candidate.Name, title) && (hint.Year == 0 || candidate.YearBegan == hint.Year) {
				id = candidate.ID
				break
			}
		}
		if id == 0 {
			return
		}
	}
	held, err := s.GCD.Series(ctx, id)
	if err != nil {
		s.warn(settings.ComicSourceGCD, strconv.Itoa(id), err)
		series.Failed = append(series.Failed, settings.ComicSourceGCD)
		return
	}
	series.IDs[ProviderGCD] = strconv.Itoa(held.ID)
	series.fill(held.Name, "", "", "", held.YearBegan, held.YearEnded)
	series.Sources = append(series.Sources, settings.ComicSourceGCD)
}

// mangaDexRatings are the content ratings asked for: MangaDex rates mainstream manga with nudity "erotica", so only "pornographic" is adult.
var mangaDexRatings = struct{ safe, adult []string }{
	safe: []string{"safe", "suggestive", "erotica"}, adult: []string{"safe", "suggestive", "erotica", "pornographic"},
}

func (s Sources) fetchManga(ctx context.Context, series *Series, order []string, hint Hint) {
	var aggregate []mangadex.Volume
	var lastVolume int
	var volumeCounts []int
	for _, key := range order {
		switch key {
		case settings.TVSourceAniList:
			manga, ok := s.askAniList(ctx, series, hint)
			if ok {
				volumeCounts = append(volumeCounts, manga.Volumes)
			}
		case settings.ComicSourceMangaUpdates:
			if held, ok := s.askMangaUpdates(ctx, series, hint); ok {
				volumeCounts = append(volumeCounts, held.Volumes)
			}
		case settings.ComicSourceMangaDex:
			dex, volumes, ok := s.askMangaDex(ctx, series, hint)
			if ok {
				aggregate, lastVolume = volumes, dex.LastVolume
			}
		default:
			s.askRecords(ctx, series, key, hint)
		}
	}
	if parts := mangaParts(aggregate, append(volumeCounts, lastVolume)); len(parts) > 0 || len(series.Parts) == 0 {
		series.Parts = parts
	}
	if series.Format == "" {
		series.Format = FormatSeries
	}
}

func (s Sources) askAniList(ctx context.Context, series *Series, hint Hint) (anilist.Manga, bool) {
	if s.AniList == nil {
		return anilist.Manga{}, false
	}
	id, _ := strconv.ParseInt(series.IDs[ProviderAniListManga], 10, 64)
	if id == 0 {
		title := firstNonEmpty(hint.Title, series.Title)
		if title == "" {
			return anilist.Manga{}, false
		}
		found, _, err := s.AniList.SearchManga(ctx, title, 1)
		if err != nil {
			s.warn(settings.TVSourceAniList, title, err)
			return anilist.Manga{}, false
		}
		for _, candidate := range found {
			if slices.ContainsFunc(candidate.Titles(), func(name string) bool { return sameTitle(name, title) }) {
				id = candidate.ID
				break
			}
		}
		if id == 0 {
			return anilist.Manga{}, false
		}
	}
	manga, err := s.AniList.Manga(ctx, id)
	if err != nil {
		s.warn(settings.TVSourceAniList, strconv.FormatInt(id, 10), err)
		series.Failed = append(series.Failed, settings.TVSourceAniList)
		return anilist.Manga{}, false
	}
	series.IDs[ProviderAniListManga] = strconv.FormatInt(manga.ID, 10)
	if manga.IDMal > 0 && series.IDs[ProviderMAL] == "" {
		series.IDs[ProviderMAL] = strconv.FormatInt(manga.IDMal, 10)
	}
	title := firstNonEmpty(manga.Title.English, manga.Title.Romaji, manga.Title.Native)
	series.fill(title, stripMarkup(manga.Description), "", manga.CoverImage.ExtraLarge, manga.StartDate.Year, 0)
	series.addAliases(manga.Titles()...)
	series.addGenres(manga.Genres...)
	for index, person := range manga.People() {
		series.Credits = append(series.Credits, Credit{Name: person.Name, Role: mangaRole(person.Role), Order: index})
	}
	if series.Format == "" && manga.Format == "ONE_SHOT" {
		series.Format = FormatOneShot
	}
	if series.Status == "" {
		series.Status = aniListStatus(manga.Status)
	}
	if series.OriginalLanguage == "" && manga.CountryOfOrigin == "JP" {
		series.OriginalLanguage = "ja"
	}
	series.Adult = series.Adult || manga.IsAdult
	series.Sources = append(series.Sources, settings.TVSourceAniList)
	return manga, true
}

func (s Sources) askMangaUpdates(ctx context.Context, series *Series, hint Hint) (mangaupdates.Series, bool) {
	if s.MangaUpdates == nil {
		return mangaupdates.Series{}, false
	}
	id, _ := strconv.ParseInt(series.IDs[ProviderMangaUpdates], 10, 64)
	if id == 0 {
		title := firstNonEmpty(series.Title, hint.Title)
		if title == "" {
			return mangaupdates.Series{}, false
		}
		found, _, err := s.MangaUpdates.SearchSeries(ctx, title, 1)
		if err != nil {
			s.warn(settings.ComicSourceMangaUpdates, title, err)
			return mangaupdates.Series{}, false
		}
		for _, candidate := range found {
			if sameTitle(candidate.Title, title) || slices.ContainsFunc(candidate.Associated, func(name string) bool { return sameTitle(name, title) }) {
				id = candidate.ID
				break
			}
		}
		if id == 0 {
			return mangaupdates.Series{}, false
		}
	}
	held, err := s.MangaUpdates.Series(ctx, id)
	if err != nil {
		s.warn(settings.ComicSourceMangaUpdates, strconv.FormatInt(id, 10), err)
		series.Failed = append(series.Failed, settings.ComicSourceMangaUpdates)
		return mangaupdates.Series{}, false
	}
	series.IDs[ProviderMangaUpdates] = strconv.FormatInt(held.ID, 10)
	publisher := ""
	if len(held.Publishers) > 0 {
		publisher = held.Publishers[0].Name
	}
	series.fill(held.Title, stripMarkup(held.Description), publisher, held.CoverURL, held.Year, 0)
	series.addAliases(held.Associated...)
	series.addGenres(held.Genres...)
	if len(series.Credits) == 0 {
		for index, author := range held.Authors {
			series.Credits = append(series.Credits, Credit{Name: author.Name, Role: mangaRole(author.Type), Order: index})
		}
	}
	if series.Status == "" && held.Completed {
		series.Status = "ended"
	}
	series.Sources = append(series.Sources, settings.ComicSourceMangaUpdates)
	return held, true
}

func (s Sources) askMangaDex(ctx context.Context, series *Series, hint Hint) (mangadex.Manga, []mangadex.Volume, bool) {
	if s.MangaDex == nil {
		return mangadex.Manga{}, nil, false
	}
	id := series.IDs[ProviderMangaDex]
	if id == "" {
		title := firstNonEmpty(series.Title, hint.Title)
		if title == "" {
			return mangadex.Manga{}, nil, false
		}
		ratings := mangaDexRatings.safe
		if series.Adult {
			ratings = mangaDexRatings.adult
		}
		found, _, err := s.MangaDex.SearchManga(ctx, title, 1, ratings)
		if err != nil {
			s.warn(settings.ComicSourceMangaDex, title, err)
			return mangadex.Manga{}, nil, false
		}
		for _, candidate := range found {
			bridged := series.IDs[ProviderAniListManga] != "" && candidate.Links["al"] == series.IDs[ProviderAniListManga]
			if bridged || (series.IDs[ProviderAniListManga] == "" && sameTitle(candidate.Title, title)) {
				id = candidate.ID
				break
			}
		}
		if id == "" {
			return mangadex.Manga{}, nil, false
		}
	}
	dex, err := s.MangaDex.Manga(ctx, id)
	if err != nil {
		s.warn(settings.ComicSourceMangaDex, id, err)
		series.Failed = append(series.Failed, settings.ComicSourceMangaDex)
		return mangadex.Manga{}, nil, false
	}
	series.IDs[ProviderMangaDex] = dex.ID
	for key, provider := range map[string]string{"al": ProviderAniListManga, "mu": "", "mal": ProviderMAL} {
		if provider != "" && series.IDs[provider] == "" && dex.Links[key] != "" {
			series.IDs[provider] = dex.Links[key]
		}
	}
	if code := dex.Links["mu"]; code != "" && series.IDs[ProviderMangaUpdates] == "" {
		if numeric, ok := mangaupdates.IDFromCode(code); ok {
			series.IDs[ProviderMangaUpdates] = strconv.FormatInt(numeric, 10)
		}
	}
	series.fill(dex.Title, stripMarkup(dex.Description), "", "", dex.Year, 0)
	series.addAliases(dex.AltTitles...)
	series.addGenres(dex.Tags...)
	if series.OriginalLanguage == "" {
		series.OriginalLanguage = dex.OriginalLanguage
	}
	series.Adult = series.Adult || dex.ContentRating == "pornographic"
	if series.Status == "" {
		series.Status = dex.Status
	}
	series.Sources = append(series.Sources, settings.ComicSourceMangaDex)
	volumes, err := s.MangaDex.Aggregate(ctx, dex.ID, "en")
	if err != nil {
		s.warn(settings.ComicSourceMangaDex, dex.ID, fmt.Errorf("aggregate: %w", err))
		return dex, nil, true
	}
	return dex, volumes, true
}

// mangaParts lists the volumes of a manga: the aggregate's when MangaDex gave one, else 1..N from the largest count any
// provider states. Chapters the aggregate leaves in no volume become loose chapter parts.
func mangaParts(aggregate []mangadex.Volume, counts []int) []Part {
	var parts []Part
	seen := map[string]bool{}
	for _, volume := range aggregate {
		if volume.Number == mangadex.UnnumberedVolume {
			for _, chapter := range volume.Chapters {
				parts = append(parts, Part{Kind: PartChapter, Number: chapter})
			}
			continue
		}
		parts = append(parts, Part{Kind: PartVolume, Number: volume.Number, Chapters: volume.Chapters})
		seen[volume.Number] = true
	}
	most := 0
	for _, count := range counts {
		most = max(most, count)
	}
	if len(aggregate) == 0 || most > 0 {
		for number := 1; number <= most; number++ {
			if key := strconv.Itoa(number); !seen[key] {
				parts = append(parts, Part{Kind: PartVolume, Number: key})
			}
		}
	}
	slices.SortStableFunc(parts, func(a, b Part) int { return strings.Compare(SortKey(a.Kind, a.Number), SortKey(b.Kind, b.Number)) })
	return parts
}

// fill sets the series' empty fields from one provider's answer: the first provider holding a field supplies it.
func (s *Series) fill(title, overview, publisher, cover string, year, endYear int) {
	if s.Title == "" {
		s.Title = strings.TrimSpace(title)
	}
	if s.Overview == "" {
		s.Overview = strings.TrimSpace(overview)
	}
	if s.Publisher == "" {
		s.Publisher = strings.TrimSpace(publisher)
	}
	if s.Cover == "" {
		s.Cover = cover
	}
	if s.Year == 0 {
		s.Year = year
	}
	if s.EndYear == 0 {
		s.EndYear = endYear
	}
}

func (s *Series) addAliases(names ...string) {
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" && !sameTitle(name, s.Title) && !slices.ContainsFunc(s.Aliases, func(held string) bool { return sameTitle(held, name) }) {
			s.Aliases = append(s.Aliases, name)
		}
	}
}

func (s *Series) addGenres(names ...string) {
	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" && !slices.Contains(s.Genres, name) {
			s.Genres = append(s.Genres, name)
		}
	}
}

// mergeParts takes the first provider's issue list as the numbering; a later provider only fills what a matching issue lacks.
func (s *Series) mergeParts(parts []Part) {
	if len(s.Parts) == 0 {
		s.Parts = parts
		return
	}
	for _, later := range parts {
		for i := range s.Parts {
			held := &s.Parts[i]
			if held.Ref() != later.Ref() {
				continue
			}
			held.Title = firstNonEmpty(held.Title, later.Title)
			held.CoverDate = firstNonEmpty(held.CoverDate, later.CoverDate)
			held.Store = firstNonEmpty(held.Store, later.Store)
			held.Cover = firstNonEmpty(held.Cover, later.Cover)
			held.Rating = firstNonEmpty(held.Rating, later.Rating)
			held.Pages = firstInt(held.Pages, later.Pages)
			for provider, id := range later.IDs {
				if held.IDs == nil {
					held.IDs = map[string]string{}
				}
				held.IDs[provider] = id
			}
		}
	}
}

// partKind says an annual or special by how the number is printed.
func partKind(number string) string {
	lower := strings.ToLower(number)
	switch {
	case strings.Contains(lower, "annual"):
		return PartAnnual
	case strings.Contains(lower, "special"):
		return PartSpecial
	}
	return PartIssue
}

func metronFormat(kind string) string {
	switch strings.ToLower(strings.ReplaceAll(kind, " ", "_")) {
	case "limited_series", "mini_series":
		return FormatLimited
	case "one-shot", "one_shot":
		return FormatOneShot
	case "annual_series", "annual":
		return FormatAnnual
	case "trade_paperback":
		return FormatTradePaperbck
	case "hardcover":
		return FormatHardcover
	case "graphic_novel":
		return FormatGraphicNovel
	case "omnibus":
		return FormatOmnibus
	}
	return FormatSeries
}

func aniListStatus(status string) string {
	switch status {
	case "FINISHED":
		return "ended"
	case "CANCELLED":
		return "cancelled"
	case "HIATUS":
		return "hiatus"
	}
	return "continuing"
}

func mangaRole(role string) string {
	lower := strings.ToLower(role)
	switch {
	case strings.Contains(lower, "story") || strings.Contains(lower, "author"):
		return RoleWriter
	case strings.Contains(lower, "art"):
		return RolePenciller
	}
	return RoleWriter
}

var markup = regexp.MustCompile(`<[^>]*>`)

func stripMarkup(text string) string { return strings.TrimSpace(markup.ReplaceAllString(text, " ")) }

// sameTitle compares two titles ignoring case, punctuation and spacing.
func sameTitle(a, b string) bool { return titleKey(a) != "" && titleKey(a) == titleKey(b) }

func titleKey(title string) string {
	var out strings.Builder
	for _, r := range strings.ToLower(title) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r > 127 {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstInt(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}
