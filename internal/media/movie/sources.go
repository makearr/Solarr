package movie

import (
	"cmp"
	"slices"
	"strings"

	"github.com/makearr/Solarr/internal/metadata/radarrmeta"
	"github.com/makearr/Solarr/internal/metadata/tmdb"
)

// Source names, as listed in Movie.Sources.
const (
	sourceTMDB  = "tmdb"
	sourceRadar = "radarr"
)

const (
	// maxCast bounds the people stored per movie: the billed-first of the cast.
	maxCast = 30
	// maxImages bounds the artwork kept per kind.
	maxImages = 12
	maxAlias  = 30
)

// crewRoles maps TMDB's and Radarr's crew jobs to the roles a movie stores.
var crewRoles = map[string]string{
	"Director": RoleDirector, "Writer": RoleWriter, "Screenplay": RoleWriter, "Story": RoleWriter,
	"Producer": RoleProducer, "Executive Producer": RoleProducer,
	"Original Music Composer": RoleComposer, "Composer": RoleComposer,
	"Director of Photography": RoleCinematographer,
}

// FromTMDB turns TMDB's answer into a movie; country picks the certification ("US" when empty).
func FromTMDB(in tmdb.Movie, country string) Movie {
	movie := Movie{
		IDs:   IDs{TMDB: in.ID, IMDb: in.IMDbID},
		Title: in.Title, OriginalTitle: in.OriginalTitle, Tagline: in.Tagline, Overview: in.Overview,
		Year: yearOf(in.ReleaseDate), RuntimeMin: in.Runtime, OriginalLanguage: in.OriginalLanguage,
		Certification: in.Certification(country), Website: in.Homepage, Sources: []string{sourceTMDB},
		Premiere: in.ReleaseOfType(tmdb.ReleasePremiere), InCinemas: in.ReleaseOfType(tmdb.ReleaseTheatricalLimit, tmdb.ReleaseTheatrical),
		Digital: in.ReleaseOfType(tmdb.ReleaseDigital), Physical: in.ReleaseOfType(tmdb.ReleasePhysical),
	}
	if movie.InCinemas == "" && movie.Digital == "" && movie.Physical == "" {
		movie.InCinemas = firstDay(in.ReleaseDate)
	}
	for _, genre := range in.Genres {
		movie.Genres = append(movie.Genres, genre.Name)
	}
	for _, keyword := range in.Keywords.Keywords {
		movie.Keywords = append(movie.Keywords, keyword.Name)
	}
	for _, company := range in.Companies {
		movie.Studios = append(movie.Studios, Studio{TMDBID: company.ID, Name: company.Name, Country: company.OriginCountry, Logo: tmdb.ImageURL(company.LogoPath)})
	}
	if len(movie.Studios) > 0 {
		movie.Studio = movie.Studios[0].Name
	}
	if in.VoteAverage > 0 {
		movie.Ratings = []Rating{{Provider: ProviderTMDB, Score: in.VoteAverage, Votes: int64(in.VoteCount)}}
	}
	if in.Collection != nil && in.Collection.ID != 0 {
		movie.Collection = &Collection{TMDBID: in.Collection.ID, Name: in.Collection.Name}
	}
	for _, title := range in.AlternativeTitles.Titles {
		movie.Aliases = append(movie.Aliases, title.Title)
	}
	movie.Aliases = aliasesOf(movie, in.OriginalTitle)
	movie.Images = tmdbImages(in)
	movie.Credits = tmdbCredits(in)
	return movie
}

func tmdbImages(in tmdb.Movie) []Image {
	var images []Image
	add := func(kind, primary string, list []tmdb.Image) {
		sorted := slices.Clone(list)
		slices.SortStableFunc(sorted, func(a, b tmdb.Image) int { return cmp.Compare(b.VoteAverage, a.VoteAverage) })
		if primary != "" {
			images = append(images, Image{Kind: kind, URL: tmdb.ImageURL(primary), Source: sourceTMDB})
		}
		kept := 0
		for _, image := range sorted {
			if kept >= maxImages {
				break
			}
			if url := tmdb.ImageURL(image.FilePath); url != "" && !hasImage(images, url) {
				images = append(images, Image{Kind: kind, URL: url, Source: sourceTMDB})
				kept++
			}
		}
	}
	add("poster", in.PosterPath, in.Images.Posters)
	add("fanart", in.BackdropPath, in.Images.Backdrops)
	add("logo", "", in.Images.Logos)
	return images
}

func tmdbCredits(in tmdb.Movie) []Credit {
	var credits []Credit
	for _, cast := range in.Credits.Cast {
		if cast.Order >= maxCast || cast.ID == 0 {
			continue
		}
		credits = append(credits, Credit{TMDBID: cast.ID, Name: cast.Name, Image: tmdb.ImageURL(cast.ProfilePath), Role: RoleActor, Character: cast.Character, Order: cast.Order})
	}
	seen := map[[2]string]bool{}
	for _, crew := range in.Credits.Crew {
		role, wanted := crewRoles[crew.Job]
		key := [2]string{role, crew.Name}
		if !wanted || crew.ID == 0 || seen[key] {
			continue
		}
		seen[key] = true
		credits = append(credits, Credit{TMDBID: crew.ID, Name: crew.Name, Image: tmdb.ImageURL(crew.ProfilePath), Role: role})
	}
	return credits
}

// FromRadarr turns Radarr's metadata service's answer into a movie.
func FromRadarr(in radarrmeta.Movie, country string) Movie {
	movie := Movie{
		IDs:   IDs{TMDB: in.TMDBID, IMDb: in.IMDbID},
		Title: in.Title, OriginalTitle: in.OriginalTitle, Overview: in.Overview, Year: in.Year, RuntimeMin: in.Runtime,
		OriginalLanguage: in.OriginalLanguage, Genres: in.Genres, Keywords: in.Keywords, Studio: in.Studio, Website: in.Homepage,
		TrailerID: in.TrailerID, Sources: []string{sourceRadar},
		Premiere: firstDay(in.Premiere), InCinemas: firstDay(in.InCinemas), Digital: firstDay(in.DigitalRelease), Physical: firstDay(in.PhysicalRelease),
		Certification: radarrCertification(in, country),
	}
	if movie.Studio != "" {
		movie.Studios = []Studio{{Name: movie.Studio}}
	}
	for provider, rating := range in.MovieRatings {
		if score := radarrScore(provider, rating.Value); score > 0 {
			movie.Ratings = append(movie.Ratings, Rating{Provider: strings.ToLower(provider), Score: score, Votes: rating.Count})
		}
	}
	slices.SortFunc(movie.Ratings, func(a, b Rating) int { return cmp.Compare(a.Provider, b.Provider) })
	if in.Collection != nil && in.Collection.TMDBID != 0 {
		movie.Collection = &Collection{TMDBID: in.Collection.TMDBID, Name: in.Collection.Name}
	}
	for _, title := range in.AlternativeTitles {
		movie.Aliases = append(movie.Aliases, title.Title)
	}
	movie.Aliases = aliasesOf(movie, in.OriginalTitle)
	for _, image := range in.Images {
		movie.Images = append(movie.Images, Image{Kind: radarrImageKind(image.CoverType), URL: image.URL, Source: sourceRadar})
	}
	movie.Credits = radarrCredits(in)
	return movie
}

// radarrScore brings a rating to the 0 to 10 scale; the service sends Metacritic and Rotten Tomatoes out of 100.
func radarrScore(provider string, value float64) float64 {
	switch strings.ToLower(provider) {
	case "metacritic", "rottentomatoes":
		return value / 10
	}
	return value
}

func radarrCertification(in radarrmeta.Movie, country string) string {
	for _, wanted := range []string{country, "US"} {
		for _, entry := range in.Certifications {
			if wanted != "" && entry.Country == wanted && entry.Certification != "" {
				return entry.Certification
			}
		}
	}
	for _, entry := range in.Certifications {
		if entry.Certification != "" {
			return entry.Certification
		}
	}
	return ""
}

func radarrImageKind(coverType string) string {
	switch strings.ToLower(coverType) {
	case "poster":
		return "poster"
	case "fanart", "backdrop":
		return "fanart"
	case "clearlogo", "logo":
		return "logo"
	}
	return strings.ToLower(coverType)
}

func radarrCredits(in radarrmeta.Movie) []Credit {
	var credits []Credit
	for _, cast := range in.Credits.Cast {
		if cast.Order >= maxCast || cast.TMDBID == 0 {
			continue
		}
		credits = append(credits, Credit{TMDBID: cast.TMDBID, Name: cast.Name, Image: headshot(cast.Images), Role: RoleActor, Character: cast.Character, Order: cast.Order})
	}
	seen := map[[2]string]bool{}
	for _, crew := range in.Credits.Crew {
		role, wanted := crewRoles[crew.Job]
		key := [2]string{role, crew.Name}
		if !wanted || crew.TMDBID == 0 || seen[key] {
			continue
		}
		seen[key] = true
		credits = append(credits, Credit{TMDBID: crew.TMDBID, Name: crew.Name, Image: headshot(crew.Images), Role: role})
	}
	return credits
}

func headshot(images []radarrmeta.Image) string {
	for _, image := range images {
		if strings.EqualFold(image.CoverType, "headshot") {
			return image.URL
		}
	}
	return ""
}

// Merge fills what the first movie lacks from the second: the first is the preferred source (TMDB), so a value it holds
// is never replaced; lists the first has stay its own, except ratings and images, which are the union.
func Merge(first, second Movie) Movie {
	merged := first
	merged.IDs = IDs{TMDB: cmp.Or(first.IDs.TMDB, second.IDs.TMDB), IMDb: cmp.Or(first.IDs.IMDb, second.IDs.IMDb)}
	merged.Title = cmp.Or(first.Title, second.Title)
	merged.OriginalTitle = cmp.Or(first.OriginalTitle, second.OriginalTitle)
	merged.Tagline = cmp.Or(first.Tagline, second.Tagline)
	merged.Overview = cmp.Or(first.Overview, second.Overview)
	merged.Year = cmp.Or(first.Year, second.Year)
	merged.RuntimeMin = cmp.Or(first.RuntimeMin, second.RuntimeMin)
	merged.OriginalLanguage = cmp.Or(first.OriginalLanguage, second.OriginalLanguage)
	merged.Certification = cmp.Or(first.Certification, second.Certification)
	merged.Studio = cmp.Or(first.Studio, second.Studio)
	merged.Premiere, merged.InCinemas = cmp.Or(first.Premiere, second.Premiere), cmp.Or(first.InCinemas, second.InCinemas)
	merged.Digital, merged.Physical = cmp.Or(first.Digital, second.Digital), cmp.Or(first.Physical, second.Physical)
	merged.Website, merged.TrailerID = cmp.Or(first.Website, second.Website), cmp.Or(first.TrailerID, second.TrailerID)
	if len(merged.Genres) == 0 {
		merged.Genres = second.Genres
	}
	if len(merged.Keywords) == 0 {
		merged.Keywords = second.Keywords
	}
	if len(merged.Studios) == 0 {
		merged.Studios = second.Studios
	}
	if len(merged.Credits) == 0 {
		merged.Credits = second.Credits
	}
	if merged.Collection == nil {
		merged.Collection = second.Collection
	}
	for _, rating := range second.Ratings {
		if !slices.ContainsFunc(merged.Ratings, func(have Rating) bool { return have.Provider == rating.Provider }) {
			merged.Ratings = append(slices.Clone(merged.Ratings), rating)
		}
	}
	for _, image := range second.Images {
		if !hasImage(merged.Images, image.URL) {
			merged.Images = append(slices.Clone(merged.Images), image)
		}
	}
	merged.Aliases = aliasesOf(Movie{Title: merged.Title, Aliases: append(slices.Clone(first.Aliases), second.Aliases...)}, merged.OriginalTitle)
	merged.Sources = append(slices.Clone(first.Sources), second.Sources...)
	return merged
}

// aliasesOf is the movie's other names: the alternative titles and the original title, each once, never the title itself.
func aliasesOf(movie Movie, original string) []string {
	var aliases []string
	seen := map[string]bool{strings.ToLower(movie.Title): true}
	for _, title := range append(slices.Clone(movie.Aliases), original) {
		title = strings.TrimSpace(title)
		key := strings.ToLower(title)
		if title == "" || seen[key] || len(aliases) >= maxAlias {
			continue
		}
		seen[key] = true
		aliases = append(aliases, title)
	}
	return aliases
}

func hasImage(images []Image, url string) bool {
	return slices.ContainsFunc(images, func(image Image) bool { return image.URL == url })
}

func yearOf(day string) int {
	if len(day) < 4 {
		return 0
	}
	year := 0
	for _, digit := range day[:4] {
		if digit < '0' || digit > '9' {
			return 0
		}
		year = year*10 + int(digit-'0')
	}
	return year
}
