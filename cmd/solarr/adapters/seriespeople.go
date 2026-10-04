package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/metadata/tmdb"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/paginate"
)

// maxElsewhere bounds the titles listed beside a person's credits in the library.
const maxElsewhere = 60

// PersonSource is the TMDB client as the person page needs it.
type PersonSource interface {
	Configured() bool
	Person(ctx context.Context, id int64) (tmdb.Person, error)
}

// SeriesPeople satisfies apiserver.SeriesPeopleCatalog over the library's series reader and TMDB.
type SeriesPeople struct {
	Reader        library.SeriesReader
	Persons       library.People
	Subscriptions library.Subscriptions
	TMDB          PersonSource
	// Preview asks what a follow would add; nil answers an empty preview.
	Preview func(ctx context.Context, subscription library.Subscription) (tv.FollowPreview, error)
	// EnqueueFollow asks the follow sweep to look at one subscription now; nil leaves it to the schedule.
	EnqueueFollow func(ctx context.Context, subscriptionID int64) error
	Clock         clock.Clock
}

// People maps a page of the people credited in a TV library.
func (c SeriesPeople) People(ctx context.Context, categoryID int64, roles []string, prefix string, monitored *bool, after paginate.TextCursor, limit int) ([]gen.SeriesPerson, error) {
	rows, err := c.Reader.People(ctx, library.TVPersonFilter{CategoryID: categoryID, Roles: roles, NamePrefix: prefix, Monitored: monitored},
		library.PersonCursor{SortName: after.Sort, ID: after.ID}, limit)
	if err != nil {
		return nil, err
	}
	views := make([]gen.SeriesPerson, len(rows))
	for i, row := range rows {
		views[i] = gen.SeriesPerson{Id: row.ID, Name: row.Name, Roles: row.Roles, SeriesCount: row.SeriesCount, Monitored: row.Monitored}
		setString(&views[i].ImageUrl, row.ImageURL)
		setString(&views[i].Character, row.Character)
	}
	return views, nil
}

// Person maps one person's page: credits here, then biography and other titles from TMDB when a key is stored.
func (c SeriesPeople) Person(ctx context.Context, categoryID, personID, userID int64) (gen.SeriesPersonDetail, error) {
	detail, err := c.Reader.Person(ctx, categoryID, personID)
	if err != nil {
		return gen.SeriesPersonDetail{}, translatePeopleError(err)
	}
	remote, haveRemote := c.remote(ctx, detail)
	if haveRemote {
		if survivor := c.keep(ctx, &detail, remote); survivor != detail.ID {
			if detail, err = c.Reader.Person(ctx, categoryID, survivor); err != nil {
				return gen.SeriesPersonDetail{}, translatePeopleError(err)
			}
		}
	}
	view := gen.SeriesPersonDetail{Id: detail.ID, Name: detail.Name, Monitored: detail.Monitored, Ids: detail.IDs}
	_, hasTMDB := tv.ParseID(detail.IDs[tv.ProviderTMDB])
	view.Followable = &hasTMDB
	view.Follow = c.followOf(ctx, "person", detail.ID, userID)
	setString(&view.ImageUrl, detail.ImageURL)
	setString(&view.Biography, detail.Biography)
	setString(&view.Born, detail.Born)
	setString(&view.Died, detail.Died)
	if haveRemote {
		setString(&view.Birthplace, remote.PlaceOfBirth)
	}

	workIDs := make([]int64, 0, len(detail.Credits))
	for _, credit := range detail.Credits {
		if !slices.Contains(workIDs, credit.WorkID) {
			workIDs = append(workIDs, credit.WorkID)
		}
	}
	summaries, err := c.Reader.SummariesByIDs(ctx, categoryID, workIDs, c.Clock.Now())
	if err != nil {
		return gen.SeriesPersonDetail{}, err
	}
	posters := make(map[int64]string, len(summaries))
	for _, summary := range summaries {
		posters[summary.WorkID] = posterOf(summary.Fields["images"])
	}
	view.Credits = make([]gen.SeriesPersonCredit, len(detail.Credits))
	for i, credit := range detail.Credits {
		entry := gen.SeriesPersonCredit{SeriesId: credit.WorkID, SeriesTitle: credit.Title, Role: credit.Role}
		if credit.Year > 0 {
			entry.Year = &credit.Year
		}
		setString(&entry.PosterUrl, posters[credit.WorkID])
		setString(&entry.Character, credit.Character)
		if len(credit.Episodes) > 0 {
			episodes := make([]string, len(credit.Episodes))
			for j, ref := range credit.Episodes {
				episodes[j] = episodeLabel(ref)
			}
			entry.Episodes = &episodes
		}
		view.Credits[i] = entry
	}
	others, err := c.Reader.OtherWorks(ctx, detail.ID)
	if err != nil {
		return gen.SeriesPersonDetail{}, err
	}
	if len(others) > 0 {
		views := make([]gen.SeriesPersonOtherWork, len(others))
		for i, other := range others {
			views[i] = gen.SeriesPersonOtherWork{WorkId: other.WorkID, MediaType: other.MediaType, Title: other.Title, Role: other.Role}
			if other.Year > 0 {
				views[i].Year = &other.Year
			}
		}
		view.OtherWorks = &views
	}
	if haveRemote {
		elsewhere := c.elsewhere(ctx, categoryID, detail, remote)
		view.Elsewhere = &elsewhere
	}
	return view, nil
}

// remote asks TMDB for the person, when a key is stored and the person came from TMDB.
func (c SeriesPeople) remote(ctx context.Context, detail library.TVPersonDetail) (tmdb.Person, bool) {
	if c.TMDB == nil || !c.TMDB.Configured() {
		return tmdb.Person{}, false
	}
	id, ok := tv.ParseID(detail.IDs[tv.ProviderTMDB])
	if !ok {
		return tmdb.Person{}, false
	}
	person, err := c.TMDB.Person(ctx, id)
	if err != nil {
		return tmdb.Person{}, false
	}
	return person, true
}

// keep stores what the TMDB page adds to the person, filling only what is empty, and shows it on this very page.
func (c SeriesPeople) keep(ctx context.Context, detail *library.TVPersonDetail, remote tmdb.Person) int64 {
	survivor := detail.ID
	if remote.ExternalIDs.Wikidata != "" {
		if linked, err := c.Persons.LinkWikidata(ctx, detail.ID, remote.ExternalIDs.Wikidata); err == nil {
			survivor = linked
		}
	}
	identity := library.PersonIdentity{Name: detail.Name, Born: remote.Birthday, Died: remote.Deathday}
	if survivor != detail.ID {
		return survivor
	}
	if err := c.Persons.BackfillDetails(ctx, detail.ID, identity); err != nil {
		return survivor
	}
	if detail.Biography == "" && remote.Biography != "" {
		if err := c.Persons.SetBiography(ctx, detail.ID, remote.Biography); err == nil {
			detail.Biography = remote.Biography
		}
	}
	if detail.Born == "" {
		detail.Born = remote.Birthday
	}
	if detail.Died == "" {
		detail.Died = remote.Deathday
	}
	return survivor
}

// elsewhere lists TMDB's titles for the person, newest first, each marked with the library's series when the library
// already holds it (by TMDB id, else by title and first-aired year): a person is known for series the library has
// even where their credit in it is missing.
func (c SeriesPeople) elsewhere(ctx context.Context, categoryID int64, detail library.TVPersonDetail, remote tmdb.Person) []gen.SeriesPersonElsewhere {
	held, err := c.Reader.HeldTMDB(ctx, categoryID)
	if err != nil {
		held = map[int64]int64{}
	}
	for _, credit := range detail.Credits {
		ids, err := c.Reader.ProviderIDs(ctx, credit.WorkID)
		if err != nil {
			continue
		}
		if id, ok := tv.ParseID(ids[tv.ProviderTMDB]); ok {
			held[id] = credit.WorkID
		}
	}
	byTitle := map[string][]library.SeriesTitle{}
	if titles, err := c.Reader.Titles(ctx, categoryID); err == nil {
		for _, one := range titles {
			byTitle[titleKey(one.Title)] = append(byTitle[titleKey(one.Title)], one)
		}
	}
	type key struct {
		kind string
		id   int64
	}
	seen := map[key]bool{}
	var titles []gen.SeriesPersonElsewhere
	add := func(credit tmdb.Credit, role string) {
		kind := credit.MediaType
		if (kind != "tv" && kind != "movie") || seen[key{kind, credit.ID}] {
			return
		}
		seen[key{kind, credit.ID}] = true
		title, date := credit.Name, credit.FirstAirDate
		if kind == "movie" {
			title, date = credit.Title, credit.ReleaseDate
		}
		entry := gen.SeriesPersonElsewhere{TmdbId: credit.ID, MediaType: gen.SeriesPersonElsewhereMediaType(kind), Title: title}
		if len(date) >= 4 {
			if year, err := strconv.ParseInt(date[:4], 10, 64); err == nil {
				entry.Year = &year
			}
		}
		setString(&entry.Role, role)
		setString(&entry.PosterUrl, tmdb.ImageURL(credit.PosterPath))
		if kind == "tv" {
			if workID, ok := held[credit.ID]; ok {
				entry.LibrarySeriesId = &workID
			} else {
				for _, one := range byTitle[titleKey(title)] {
					if one.Year == 0 || entry.Year == nil || abs64(one.Year-*entry.Year) <= 1 {
						workID := one.WorkID
						entry.LibrarySeriesId = &workID
						break
					}
				}
			}
		}
		titles = append(titles, entry)
	}
	for _, credit := range remote.CombinedCredits.Cast {
		add(credit, credit.Character)
	}
	for _, credit := range remote.CombinedCredits.Crew {
		add(credit, credit.Job)
	}
	slices.SortStableFunc(titles, func(a, b gen.SeriesPersonElsewhere) int {
		return int(derefInt64(b.Year) - derefInt64(a.Year))
	})
	if len(titles) > maxElsewhere {
		titles = titles[:maxElsewhere]
	}
	return titles
}

func derefInt64(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

// episodeLabel turns "s03:e04" into "S03E04".
func episodeLabel(ref string) string {
	return strings.ToUpper(strings.ReplaceAll(ref, ":", ""))
}

// Networks maps a library's networks.
func (c SeriesPeople) Networks(ctx context.Context, categoryID int64) ([]gen.SeriesNetwork, error) {
	rows, err := c.Reader.Networks(ctx, categoryID)
	if err != nil {
		return nil, err
	}
	views := make([]gen.SeriesNetwork, len(rows))
	for i, row := range rows {
		views[i] = networkView(row)
	}
	return views, nil
}

// Network maps one network with the library's series on it.
func (c SeriesPeople) Network(ctx context.Context, categoryID, networkID, userID int64) (gen.SeriesNetworkDetail, error) {
	network, ids, err := c.Reader.NetworkSeries(ctx, categoryID, networkID)
	if err != nil {
		return gen.SeriesNetworkDetail{}, translatePeopleError(err)
	}
	summaries, err := c.Reader.SummariesByIDs(ctx, categoryID, ids, c.Clock.Now())
	if err != nil {
		return gen.SeriesNetworkDetail{}, err
	}
	series := make([]gen.SeriesSummary, len(summaries))
	for i, summary := range summaries {
		series[i] = summaryView(summary)
	}
	view := gen.SeriesNetworkDetail{Network: networkView(network), Series: series}
	_, followable, err := c.Subscriptions.OrganisationExternalID(ctx, networkID, tv.ProviderTMDB)
	if err != nil {
		return gen.SeriesNetworkDetail{}, err
	}
	view.Followable = &followable
	view.Follow = c.followOf(ctx, "organisation", networkID, userID)
	return view, nil
}

// followOf maps the user's follow of a subject, nil when they have none.
func (c SeriesPeople) followOf(ctx context.Context, subject string, subjectID, userID int64) *gen.SeriesFollow {
	subscription, found, err := c.Subscriptions.FollowOf(ctx, subject, subjectID, userID)
	if err != nil || !found || !slices.Contains(subscription.MediaTypes, tv.MediaType) {
		return nil
	}
	var filters tv.FollowFilters
	_ = json.Unmarshal([]byte(subscription.FiltersJSON), &filters)
	if filters.Monitor == "" {
		filters.Monitor = string(tv.MonitorFuture)
	}
	roles := subscription.Roles
	if roles == nil {
		roles = []string{}
	}
	return &gen.SeriesFollow{Roles: roles, Monitor: filters.Monitor, BackCatalogue: subscription.MonitorBackCatalogue}
}

// Follow subscribes the user to a person or a network that TMDB knows, and asks the sweep to look at it at once.
func (c SeriesPeople) Follow(ctx context.Context, userID, categoryID int64, subject string, subjectID int64, request gen.SeriesFollowRequest) (gen.SeriesFollow, error) {
	if err := c.knownSubject(ctx, categoryID, subject, subjectID); err != nil {
		return gen.SeriesFollow{}, err
	}
	monitor := tv.MonitorFuture
	if request.Monitor != nil {
		monitor = tv.MonitorOption(*request.Monitor)
	}
	if !monitor.Valid() {
		return gen.SeriesFollow{}, fmt.Errorf("%w: unknown monitor option %q", apiserver.ErrInvalidChoice, monitor)
	}
	filters, err := json.Marshal(tv.FollowFilters{Monitor: string(monitor)})
	if err != nil {
		return gen.SeriesFollow{}, err
	}
	var roles []string
	if request.Roles != nil && subject == "person" {
		roles = *request.Roles
	}
	back := request.BackCatalogue != nil && *request.BackCatalogue
	id, err := c.Subscriptions.Follow(ctx, c.Clock.Now(), subject, subjectID, userID, tv.MediaType, library.FollowRequest{
		CategoryID: categoryID, Roles: roles, FiltersJSON: string(filters), BackCatalogue: back,
	})
	if errors.Is(err, library.ErrFollowConflict) {
		return gen.SeriesFollow{}, fmt.Errorf("%w: %w", apiserver.ErrFollowUnavailable, err)
	}
	if err != nil {
		return gen.SeriesFollow{}, err
	}
	if c.EnqueueFollow != nil {
		_ = c.EnqueueFollow(ctx, id)
	}
	if roles == nil {
		roles = []string{}
	}
	return gen.SeriesFollow{Roles: roles, Monitor: string(monitor), BackCatalogue: back}, nil
}

// knownSubject checks the subject is in the library and has the TMDB id a follow works by.
func (c SeriesPeople) knownSubject(ctx context.Context, categoryID int64, subject string, subjectID int64) error {
	switch subject {
	case "person":
		detail, err := c.Reader.Person(ctx, categoryID, subjectID)
		if err != nil {
			return translatePeopleError(err)
		}
		if _, ok := tv.ParseID(detail.IDs[tv.ProviderTMDB]); !ok {
			return fmt.Errorf("%w: this person has no TMDB id", apiserver.ErrFollowUnavailable)
		}
	case "organisation":
		if _, _, err := c.Reader.NetworkSeries(ctx, categoryID, subjectID); err != nil {
			return translatePeopleError(err)
		}
		if _, found, err := c.Subscriptions.OrganisationExternalID(ctx, subjectID, tv.ProviderTMDB); err != nil || !found {
			return fmt.Errorf("%w: this network has no TMDB id", apiserver.ErrFollowUnavailable)
		}
	}
	return nil
}

// PreviewFollow lists what a follow of the subject would add now, as the follow job would.
func (c SeriesPeople) PreviewFollow(ctx context.Context, categoryID int64, subject string, subjectID int64, roles []string, backCatalogue bool) (gen.SeriesFollowPreview, error) {
	if err := c.knownSubject(ctx, categoryID, subject, subjectID); err != nil {
		return gen.SeriesFollowPreview{}, err
	}
	view := gen.SeriesFollowPreview{WouldAdd: []gen.SeriesFollowCandidate{}}
	if c.Preview == nil {
		return view, nil
	}
	preview, err := c.Preview(ctx, library.Subscription{
		SubjectType: subject, SubjectID: subjectID, CategoryID: categoryID, Roles: roles,
		MonitorBackCatalogue: backCatalogue, CreatedAt: c.Clock.Now().Unix(),
	})
	if err != nil {
		return gen.SeriesFollowPreview{}, err
	}
	view.More, view.Held, view.Limit = preview.More, preview.Held, preview.Limit
	for _, candidate := range preview.WouldAdd {
		entry := gen.SeriesFollowCandidate{TmdbId: candidate.TMDBID, Title: candidate.Title}
		if candidate.Year > 0 {
			entry.Year = &candidate.Year
		}
		setString(&entry.PosterUrl, candidate.PosterURL)
		setString(&entry.Role, candidate.Role)
		view.WouldAdd = append(view.WouldAdd, entry)
	}
	return view, nil
}

// Unfollow removes the user's follow.
func (c SeriesPeople) Unfollow(ctx context.Context, userID int64, subject string, subjectID int64) error {
	return c.Subscriptions.Unfollow(ctx, subject, subjectID, userID)
}

func networkView(row library.TVNetworkRow) gen.SeriesNetwork {
	view := gen.SeriesNetwork{Id: row.ID, Name: row.Name, SeriesCount: row.SeriesCount}
	setString(&view.Country, row.Country)
	setString(&view.LogoUrl, row.LogoURL)
	return view
}

func translatePeopleError(err error) error {
	if errors.Is(err, library.ErrPersonNotFound) || errors.Is(err, library.ErrNetworkNotFound) {
		return fmt.Errorf("%w: %w", apiserver.ErrSeriesNotFound, err)
	}
	return err
}

// titleKey folds a title to its letters and digits, so "JEHES" and "Fubar!" are one.
func titleKey(title string) string {
	var out strings.Builder
	for _, r := range strings.ToLower(title) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func abs64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}
