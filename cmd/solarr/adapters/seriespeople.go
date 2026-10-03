package adapters

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

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
	Reader  library.SeriesReader
	Persons library.People
	TMDB    PersonSource
	Clock   clock.Clock
}

// People maps a page of the people credited in a TV library.
func (c SeriesPeople) People(ctx context.Context, categoryID int64, roles []string, prefix string, after paginate.TextCursor, limit int) ([]gen.SeriesPerson, error) {
	rows, err := c.Reader.People(ctx, library.TVPersonFilter{CategoryID: categoryID, Roles: roles, NamePrefix: prefix},
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
func (c SeriesPeople) Person(ctx context.Context, categoryID, personID int64) (gen.SeriesPersonDetail, error) {
	detail, err := c.Reader.Person(ctx, categoryID, personID)
	if err != nil {
		return gen.SeriesPersonDetail{}, translatePeopleError(err)
	}
	remote, haveRemote := c.remote(ctx, detail)
	if haveRemote {
		c.keep(ctx, &detail, remote)
	}
	view := gen.SeriesPersonDetail{Id: detail.ID, Name: detail.Name, Monitored: detail.Monitored, Ids: detail.IDs}
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
	if haveRemote {
		elsewhere := c.elsewhere(ctx, detail, remote)
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
func (c SeriesPeople) keep(ctx context.Context, detail *library.TVPersonDetail, remote tmdb.Person) {
	identity := library.PersonIdentity{Name: detail.Name, Born: remote.Birthday, Died: remote.Deathday}
	if err := c.Persons.BackfillDetails(ctx, detail.ID, identity); err != nil {
		return
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
}

// elsewhere lists TMDB's titles for the person that the library does not hold, newest first.
func (c SeriesPeople) elsewhere(ctx context.Context, detail library.TVPersonDetail, remote tmdb.Person) []gen.SeriesPersonElsewhere {
	held := map[int64]bool{}
	for _, credit := range detail.Credits {
		ids, err := c.Reader.ProviderIDs(ctx, credit.WorkID)
		if err != nil {
			continue
		}
		if id, ok := tv.ParseID(ids[tv.ProviderTMDB]); ok {
			held[id] = true
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
		if (kind != "tv" && kind != "movie") || seen[key{kind, credit.ID}] || (kind == "tv" && held[credit.ID]) {
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
func (c SeriesPeople) Network(ctx context.Context, categoryID, networkID int64) (gen.SeriesNetworkDetail, error) {
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
	return gen.SeriesNetworkDetail{Network: networkView(network), Series: series}, nil
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
