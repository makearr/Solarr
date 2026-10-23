package movie

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media/screen"
	"github.com/makearr/Solarr/internal/metadata/tmdb"
)

// MonitorOption is Radarr's "Monitor" choice when a movie is added.
type MonitorOption string

// Radarr's three: the movie alone, the movie and its whole collection, or nothing.
const (
	MonitorMovieOnly          MonitorOption = "movie_only"
	MonitorMovieAndCollection MonitorOption = "movie_and_collection"
	MonitorNone               MonitorOption = "none"
)

// Valid reports whether the option is one of the three.
func (o MonitorOption) Valid() bool {
	return o == MonitorMovieOnly || o == MonitorMovieAndCollection || o == MonitorNone
}

// Fields of a movie a person may change by hand, kept apart from what providers say.
const (
	// FieldMinimumAvailability is when the movie is wanted (Availability); a manual value wins over the library default.
	FieldMinimumAvailability = "minimum_availability"
)

// DefaultAvailability is Radarr's own default.
const DefaultAvailability = AvailableReleased

// releaseKind is the work_date kind the planner, the calendar and "Not out yet" read; one date per movie.
const releaseKind = "release"

// WriteOptions say how a movie is stored: what makes it wanted.
type WriteOptions struct {
	// Availability decides the day the movie becomes wanted; empty takes Radarr's "released".
	Availability Availability
	Now          time.Time
	// Country picks the certification and nothing else.
	Country string
}

// ToWrite turns a merged movie into what the library stores. The movie's one wanted day is its only work date; the
// other dates are fields, because every work date counts as a release for the planner and the calendar.
func ToWrite(movie Movie, options WriteOptions) library.SeriesWrite {
	runtime := int64(movie.RuntimeMin) * 60_000
	var runtimePtr, yearPtr *int64
	if movie.RuntimeMin > 0 {
		runtimePtr = &runtime
	}
	if movie.Year > 0 {
		year := int64(movie.Year)
		yearPtr = &year
	}
	source := ""
	if len(movie.Sources) > 0 {
		source = movie.Sources[0]
	}
	availability := options.Availability
	if !availability.Valid() {
		availability = DefaultAvailability
	}
	write := library.SeriesWrite{
		Work:                library.WorkIdentity{MediaType: MediaType, CanonicalTitle: movie.Title, SortTitle: SortTitle(movie.Title)},
		Year:                yearPtr,
		OriginalLanguage:    movie.OriginalLanguage,
		RuntimeMs:           runtimePtr,
		Description:         movie.Overview,
		IDs:                 idWrites(movie.IDs),
		Aliases:             movie.Aliases,
		AliasSource:         "movie-merge",
		Genres:              movie.Genres,
		GenreSource:         source,
		GenreTaxonomy:       screen.Taxonomy.Key,
		Network:             movie.Studio,
		OrganisationKind:    "studio",
		Certification:       movie.Certification,
		CertificationSystem: "movie",
		Keywords:            movie.Keywords,
		Fields:              fieldWrites(movie, source, options.Now),
	}
	for _, studio := range movie.Studios {
		if studio.Name == "" || studio.Name == movie.Studio {
			continue
		}
		write.MoreNetworks = append(write.MoreNetworks, library.SeriesNetworkWrite{
			Name: studio.Name, Country: studio.Country, Provider: tmdb.KeyCompany, ExternalID: idString(studio.TMDBID), LogoURL: studio.Logo,
		})
	}
	for i, rating := range movie.Ratings {
		entry := library.SeriesRatingWrite{Provider: rating.Provider, Score: rating.Score, Votes: rating.Votes}
		if i == 0 {
			write.Rating = &entry
		} else {
			write.MoreRatings = append(write.MoreRatings, entry)
		}
	}
	if day := movie.AvailableOn(availability); day != "" {
		write.Dates = append(write.Dates, library.SeriesDateWrite{Kind: releaseKind, Date: day, Source: source})
	} else if first := firstDay(movie.InCinemas, movie.Premiere, movie.homeRelease()); first != "" {
		// Announced: wanted at once, but the work still shows its earliest known release in the calendar.
		write.Dates = append(write.Dates, library.SeriesDateWrite{Kind: releaseKind, Date: first, Source: source})
	}
	for _, credit := range movie.Credits {
		if credit.TMDBID == 0 || credit.Name == "" {
			continue
		}
		write.Credits = append(write.Credits, library.SeriesCreditWrite{
			Provider: tmdb.KeyPerson, ExternalID: strconv.FormatInt(credit.TMDBID, 10), Name: credit.Name, ImageURL: credit.Image,
			Role: credit.Role, Character: credit.Character, Ord: credit.Order,
		})
	}
	write.LeadRole = RoleDirector
	if movie.Collection != nil && movie.Collection.TMDBID != 0 {
		write.Franchise = &library.SeriesFranchiseWrite{Provider: ProviderCollection, ExternalID: idString(movie.Collection.TMDBID), Name: movie.Collection.Name, PosterURL: movie.Collection.PosterURL}
	}
	return write
}

func idString(id int64) string {
	if id <= 0 {
		return ""
	}
	return strconv.FormatInt(id, 10)
}

func idWrites(ids IDs) []library.ExternalIDWrite {
	var writes []library.ExternalIDWrite
	if ids.TMDB > 0 {
		writes = append(writes, library.ExternalIDWrite{Provider: ProviderTMDB, ExternalID: strconv.FormatInt(ids.TMDB, 10)})
	}
	if ids.IMDb != "" {
		writes = append(writes, library.ExternalIDWrite{Provider: ProviderIMDb, ExternalID: ids.IMDb})
	}
	return writes
}

func fieldWrites(movie Movie, source string, now time.Time) []library.SeriesFieldWrite {
	var fields []library.SeriesFieldWrite
	add := func(field, value string) {
		if strings.TrimSpace(value) != "" {
			fields = append(fields, library.SeriesFieldWrite{Field: field, Provider: source, Value: value})
		}
	}
	add("status", string(movie.StatusOn(now)))
	add("tagline", movie.Tagline)
	add("original_title", movie.OriginalTitle)
	add("studio", movie.Studio)
	add("website", movie.Website)
	add("trailer", movie.TrailerID)
	add("premiere", movie.Premiere)
	add("in_cinemas", movie.InCinemas)
	add("digital_release", movie.Digital)
	add("physical_release", movie.Physical)
	if len(movie.Images) > 0 {
		if encoded, err := json.Marshal(movie.Images); err == nil {
			add("images", string(encoded))
		}
	}
	return fields
}

// SortTitle lowercases a title, collapses whitespace and drops a leading article, so "The Example Film" sorts under M.
func SortTitle(title string) string {
	lowered := strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(title))), " ")
	for _, article := range []string{"the ", "a ", "an "} {
		if rest, ok := strings.CutPrefix(lowered, article); ok {
			return rest
		}
	}
	return lowered
}
