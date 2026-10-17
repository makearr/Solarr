// Package movie is the movie module (stage 10): what a movie is, read from TMDB and Radarr's keyless metadata service and
// merged field by field (ADR-8). A movie is a work without parts whose one wanted item needs one file; core sees works and
// items only, so the vocabulary here stays in this package (ADR-23).
package movie

import (
	"time"

	"github.com/makearr/Solarr/internal/metadata/tmdb"
)

// MediaType is the work media type of a movie.
const MediaType = "movie"

// Provider names, as stored in work_external_id and work_field.
const (
	ProviderTMDB = tmdb.KeyMovie
	ProviderIMDb = "imdb"
	// ProviderCollection is TMDB's collection id, kept on the collection's own row.
	ProviderCollection = "tmdb:collection"
)

// Status is where a movie stands before its release.
type Status string

// The states Radarr shows.
const (
	StatusAnnounced  Status = "announced"
	StatusInCinemas  Status = "in_cinemas"
	StatusReleased   Status = "released"
	StatusIncomplete Status = ""
)

// Availability is Radarr's "minimum availability": when a movie is wanted.
type Availability string

// The three values Radarr offers.
const (
	AvailableAnnounced Availability = "announced"
	AvailableInCinemas Availability = "in_cinemas"
	AvailableReleased  Availability = "released"
)

// Valid reports whether the value is one of the three.
func (a Availability) Valid() bool {
	switch a {
	case AvailableAnnounced, AvailableInCinemas, AvailableReleased:
		return true
	}
	return false
}

// IDs are the identifiers one movie carries at the providers; zero or empty means unknown.
type IDs struct {
	TMDB int64
	IMDb string
}

// Roles a movie stores, in the credit table's vocabulary.
const (
	RoleActor           = "actor"
	RoleDirector        = "director"
	RoleWriter          = "writer"
	RoleProducer        = "producer"
	RoleComposer        = "composer"
	RoleCinematographer = "cinematographer"
)

// Credit is one person's part in a movie.
type Credit struct {
	TMDBID int64
	Name   string
	Image  string
	Role   string
	// Character is the part played; empty for crew.
	Character string
	// Order ranks cast, 0 first.
	Order int
}

// Studio is a production company with the provider's id for it.
type Studio struct {
	TMDBID  int64
	Name    string
	Country string
	Logo    string
}

// Image is one piece of artwork of a kind: poster, fanart, logo.
type Image struct {
	Kind   string `json:"kind"`
	URL    string `json:"url"`
	Source string `json:"source,omitempty"`
}

// Rating is one provider's score, on a scale of 0 to 10.
type Rating struct {
	Provider string
	Score    float64
	Votes    int64
}

// Collection is the franchise a movie belongs to.
type Collection struct {
	TMDBID    int64
	Name      string
	PosterURL string
}

// Movie is one film, merged from every source that knew it. Dates are yyyy-MM-dd or empty.
type Movie struct {
	IDs              IDs
	Title            string
	OriginalTitle    string
	Aliases          []string
	Tagline          string
	Overview         string
	Year             int
	RuntimeMin       int
	OriginalLanguage string
	Certification    string
	Genres           []string
	Keywords         []string
	// Studio is the primary production company's name; Studios lists every one.
	Studio  string
	Studios []Studio
	// The dates a movie is released on, by kind; the module picks what makes it wanted (Availability).
	Premiere, InCinemas, Digital, Physical string
	Ratings                                []Rating
	Images                                 []Image
	Credits                                []Credit
	Collection                             *Collection
	Website                                string
	TrailerID                              string
	// Sources names the providers that supplied it, in the order they were read.
	Sources []string
	// spareGenres are genres to fall back on when no provider names any: Wikidata's, too fine to lead.
	spareGenres []string
	// spareYear is the year to fall back on when no provider names one: Wikidata's earliest publication, often a festival a year or two early.
	spareYear int
}

// Released says the movie has come out by its own dates: a home release, else a theatrical date past.
func (m Movie) Released(now time.Time) bool {
	for _, day := range []string{m.Digital, m.Physical, m.InCinemas, m.Premiere} {
		if at, ok := parseDay(day); ok && !at.After(now) {
			return true
		}
	}
	return false
}

func parseDay(day string) (time.Time, bool) {
	if len(day) < 10 {
		return time.Time{}, false
	}
	at, err := time.Parse("2006-01-02", day[:10])
	return at, err == nil
}

// homeRelease is the earlier of the digital and physical dates, "" when neither is known.
func (m Movie) homeRelease() string {
	switch {
	case m.Digital == "":
		return m.Physical
	case m.Physical == "":
		return m.Digital
	}
	return min(m.Digital, m.Physical)
}

// theatricalWindow is how long after a cinema release Radarr counts a movie as released when no home date is known.
const theatricalWindow = 90 * 24 * time.Hour

// AvailableOn is the day a movie becomes wanted under a minimum availability, "" when it is wanted at once.
func (m Movie) AvailableOn(availability Availability) string {
	switch availability {
	case AvailableAnnounced:
		return ""
	case AvailableInCinemas:
		return firstDay(m.InCinemas, m.Premiere, m.homeRelease())
	}
	if home := m.homeRelease(); home != "" {
		return home
	}
	if cinemas := firstDay(m.InCinemas, m.Premiere); cinemas != "" {
		if at, ok := parseDay(cinemas); ok {
			return at.Add(theatricalWindow).Format("2006-01-02")
		}
	}
	return ""
}

func firstDay(days ...string) string {
	for _, day := range days {
		if len(day) >= 10 {
			return day[:10]
		}
	}
	return ""
}

// StatusOn says where the movie stands at now.
func (m Movie) StatusOn(now time.Time) Status {
	if home := m.homeRelease(); home != "" {
		if at, ok := parseDay(home); ok && !at.After(now) {
			return StatusReleased
		}
	}
	if at, ok := parseDay(firstDay(m.InCinemas, m.Premiere)); ok && !at.After(now) {
		if released, ok := parseDay(m.AvailableOn(AvailableReleased)); ok && !released.After(now) {
			return StatusReleased
		}
		return StatusInCinemas
	}
	return StatusAnnounced
}
