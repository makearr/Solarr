// Package knownfor is the part of a person's page every media type shares: what TMDB knows the person for beyond what
// the libraries hold, and what it adds to the person.
package knownfor

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/tmdb"
)

const providerTMDB = "tmdb"

func parseID(externalID string) (int64, bool) {
	id, err := strconv.ParseInt(externalID, 10, 64)
	return id, err == nil && id > 0
}

// maxKnownFor bounds the titles a person page lists beside the person's credits in the library.
const maxKnownFor = 60

// PersonSource is the TMDB client as a person's page needs it.
type PersonSource interface {
	Configured() bool
	Person(ctx context.Context, id int64) (tmdb.Person, error)
}

// Profiling is a media type's part of a person's page: what TMDB knows them for, and what it adds to the person.
type Profiling struct {
	People library.People
	Reader library.SeriesReader
	Source PersonSource
}

// KnownFor implements media.Profiling: TMDB's titles for the person, each marked with the library's series it holds.
func (p Profiling) KnownFor(ctx context.Context, personID, categoryID int64) ([]media.KnownFor, error) {
	if p.Source == nil || !p.Source.Configured() {
		return nil, nil
	}
	external, found, err := p.People.ExternalIDFor(ctx, personID, providerTMDB)
	if err != nil || !found {
		return nil, err
	}
	id, ok := parseID(external)
	if !ok {
		return nil, nil
	}
	remote, err := p.Source.Person(ctx, id)
	if err != nil {
		return nil, err
	}
	p.keep(ctx, personID, remote)
	return p.knownFor(ctx, personID, categoryID, remote), nil
}

// keep stores what TMDB adds to the person, filling only what is empty; a Wikidata id may merge them with an artist.
func (p Profiling) keep(ctx context.Context, personID int64, remote tmdb.Person) {
	if remote.ExternalIDs.Wikidata != "" {
		if survivor, err := p.People.LinkWikidata(ctx, personID, remote.ExternalIDs.Wikidata); err != nil || survivor != personID {
			return
		}
	}
	person, err := p.People.Person(ctx, personID)
	if err != nil {
		return
	}
	if err := p.People.BackfillDetails(ctx, personID, library.PersonIdentity{Name: person.Name, Born: remote.Birthday, Died: remote.Deathday}); err != nil {
		return
	}
	if remote.Biography != "" {
		_ = p.People.SetBiography(ctx, personID, remote.Biography)
	}
}

// knownFor lists TMDB's titles, marking the library's series by TMDB id, else by title and first-aired year: a person
// is known for series the library has even where their credit in it is missing.
func (p Profiling) knownFor(ctx context.Context, personID, categoryID int64, remote tmdb.Person) []media.KnownFor {
	heldTV, err := p.Reader.HeldTMDBOf(ctx, "tv")
	if err != nil {
		heldTV = map[int64]int64{}
	}
	heldMovies, err := p.Reader.HeldTMDBOf(ctx, "movie")
	if err != nil {
		heldMovies = map[int64]int64{}
	}
	held := heldTV
	if credits, err := p.People.Credits(ctx, personID, []int64{categoryID}); err == nil {
		for _, credit := range credits {
			ids, err := p.Reader.ProviderIDs(ctx, credit.ID)
			if err != nil {
				continue
			}
			if id, ok := parseID(ids[providerTMDB]); ok {
				if credit.MediaType == "movie" {
					heldMovies[id] = credit.ID
				} else {
					held[id] = credit.ID
				}
			}
		}
	}
	byTitle := map[string][]library.SeriesTitle{}
	if titles, err := p.Reader.Titles(ctx, categoryID); err == nil {
		for _, one := range titles {
			byTitle[titleKey(one.Title)] = append(byTitle[titleKey(one.Title)], one)
		}
	}
	seen := map[string]bool{}
	var titles []media.KnownFor
	add := func(credit tmdb.Credit, role string) {
		kind := credit.MediaType
		key := kind + ":" + strconv.FormatInt(credit.ID, 10)
		if (kind != "tv" && kind != "movie") || seen[key] {
			return
		}
		seen[key] = true
		title, date := credit.Name, credit.FirstAirDate
		if kind == "movie" {
			title, date = credit.Title, credit.ReleaseDate
		}
		entry := media.KnownFor{
			Provider: providerTMDB, ExternalID: strconv.FormatInt(credit.ID, 10), Kind: kind, Title: title,
			PosterURL: tmdb.ImageURL(credit.PosterPath), Role: role,
		}
		if len(date) >= 4 {
			entry.Year, _ = strconv.ParseInt(date[:4], 10, 64)
		}
		if kind == "movie" {
			if workID, ok := heldMovies[credit.ID]; ok {
				entry.WorkID = workID
			}
		}
		if kind == "tv" {
			if workID, ok := held[credit.ID]; ok {
				entry.WorkID = workID
			} else {
				for _, one := range byTitle[titleKey(title)] {
					if one.Year == 0 || entry.Year == 0 || max(one.Year-entry.Year, entry.Year-one.Year) <= 1 {
						entry.WorkID = one.WorkID
						break
					}
				}
			}
		}
		// What the library already holds is not something to discover.
		if entry.WorkID == 0 {
			titles = append(titles, entry)
		}
	}
	for _, credit := range remote.CombinedCredits.Cast {
		add(credit, credit.Character)
	}
	for _, credit := range remote.CombinedCredits.Crew {
		add(credit, credit.Job)
	}
	slices.SortStableFunc(titles, func(a, b media.KnownFor) int { return int(b.Year - a.Year) })
	if len(titles) > maxKnownFor {
		titles = titles[:maxKnownFor]
	}
	return titles
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
