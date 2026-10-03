package tv

import (
	"strings"

	"github.com/makearr/Solarr/internal/metadata/tmdb"
)

const sourceTMDB = "tmdb"

// Caps keep a long-running series' credit list from drowning the screens; TMDB lists hundreds of crew.
const (
	maxSeriesCast = 60
	maxGuestStars = 12
	maxCrewPerJob = 40
)

// FromTMDB reads TMDB's answers into the module's types; it carries no episode list, since TMDB's order is not the
// numbering basis, only credits scoped to an episode with the air date that guards them.
func FromTMDB(in tmdb.Series, seasons []tmdb.Season, country string) Series {
	series := Series{
		IDs:   IDs{TMDB: in.ID, TVDB: in.ExternalIDs.TVDB, IMDb: in.ExternalIDs.IMDb},
		Title: in.Name, Overview: in.Overview, FirstAired: in.FirstAirDate, LastAired: in.LastAirDate,
		Year: yearOf(in.FirstAirDate), Status: statusOfTMDB(in.Status), RuntimeMin: firstPositive(in.EpisodeRunTime...),
		OriginalLanguage: in.OriginalLanguage, Rating: in.VoteAverage, RatingVotes: in.VoteCount,
		ContentRating: in.ContentRating(country), Sources: []string{sourceTMDB},
	}
	if len(in.OriginCountry) > 0 {
		series.OriginalCountry = strings.ToLower(in.OriginCountry[0])
	}
	for _, genre := range in.Genres {
		series.Genres = append(series.Genres, genre.Name)
	}
	for _, keyword := range in.Keywords.Results {
		series.Keywords = append(series.Keywords, keyword.Name)
	}
	if len(in.Networks) > 0 {
		series.Network = in.Networks[0].Name
	}
	for _, network := range in.Networks {
		series.Networks = append(series.Networks, NetworkRef{TMDBID: network.ID, Name: network.Name, Country: network.OriginCountry, Logo: tmdb.ImageURL(network.LogoPath)})
	}
	if in.OriginalName != "" && in.OriginalName != in.Name {
		series.Aliases = append(series.Aliases, in.OriginalName)
	}
	for _, title := range in.AlternativeTitles.Results {
		series.Aliases = appendDistinct(series.Aliases, title.Title)
	}
	series.Images = tmdbImages(in)
	for _, season := range in.Seasons {
		series.Seasons = append(series.Seasons, Season{Number: season.SeasonNumber, Poster: tmdb.ImageURL(season.PosterPath)})
	}
	series.Credits = append(series.Credits, tmdbSeriesCredits(in)...)
	for _, season := range seasons {
		for _, episode := range season.Episodes {
			series.Credits = append(series.Credits, tmdbEpisodeCredits(episode)...)
		}
	}
	return series
}

func tmdbImages(in tmdb.Series) []Image {
	var images []Image
	add := func(kind, path string) {
		if path != "" {
			images = append(images, Image{Kind: kind, URL: tmdb.ImageURL(path), Source: sourceTMDB})
		}
	}
	add("poster", in.PosterPath)
	add("fanart", in.BackdropPath)
	for _, logo := range in.Images.Logos {
		if logo.Language == "en" {
			add("logo", logo.FilePath)
			break
		}
	}
	return images
}

func tmdbSeriesCredits(in tmdb.Series) []Credit {
	var credits []Credit
	for _, creator := range in.CreatedBy {
		credits = append(credits, Credit{TMDBID: creator.ID, Name: creator.Name, Image: tmdb.ImageURL(creator.ProfilePath), Role: RoleCreator})
	}
	for index, member := range in.AggregateCredits.Cast {
		if index == maxSeriesCast {
			break
		}
		character := ""
		if len(member.Roles) > 0 {
			character = member.Roles[0].Character
		}
		credits = append(credits, Credit{
			TMDBID: member.ID, Name: member.Name, Image: tmdb.ImageURL(member.ProfilePath), Role: RoleActor,
			Character: character, Order: member.Order, Episodes: member.TotalEpisodeCount,
		})
	}
	perRole := map[string]int{}
	for _, member := range in.AggregateCredits.Crew {
		for _, job := range member.Jobs {
			role := crewRole(job.Job)
			if role == "" || perRole[role] >= maxCrewPerJob {
				continue
			}
			perRole[role]++
			credits = append(credits, Credit{
				TMDBID: member.ID, Name: member.Name, Image: tmdb.ImageURL(member.ProfilePath), Role: role, Episodes: job.EpisodeCount,
			})
		}
	}
	return credits
}

func tmdbEpisodeCredits(episode tmdb.Episode) []Credit {
	var credits []Credit
	for index, guest := range episode.GuestStars {
		if index == maxGuestStars {
			break
		}
		credits = append(credits, Credit{
			TMDBID: guest.ID, Name: guest.Name, Image: tmdb.ImageURL(guest.ProfilePath), Role: RoleGuestStar, Character: guest.Character,
			Order: guest.Order, Season: episode.SeasonNumber, Episode: episode.EpisodeNumber, EpisodeAirDate: episode.AirDate,
		})
	}
	for _, member := range episode.Crew {
		role := crewRole(member.Job)
		if role != RoleDirector && role != RoleWriter {
			continue
		}
		credits = append(credits, Credit{
			TMDBID: member.ID, Name: member.Name, Image: tmdb.ImageURL(member.ProfilePath), Role: role,
			Season: episode.SeasonNumber, Episode: episode.EpisodeNumber, EpisodeAirDate: episode.AirDate,
		})
	}
	return credits
}

// crewRole maps a TMDB job to the credit vocabulary; jobs outside it (gaffer, colourist) are not stored.
func crewRole(job string) string {
	switch strings.ToLower(job) {
	case "director":
		return RoleDirector
	case "writer", "screenplay", "teleplay", "story":
		return RoleWriter
	case "executive producer":
		return RoleExecutiveProducer
	case "producer":
		return RoleProducer
	case "showrunner":
		return RoleShowrunner
	case "original music composer", "composer", "music":
		return RoleComposer
	}
	return ""
}

func statusOfTMDB(status string) Status {
	switch strings.ToLower(status) {
	case "returning series":
		return StatusContinuing
	case "ended", "canceled":
		return StatusEnded
	case "in production", "planned", "pilot":
		return StatusUpcoming
	}
	return ""
}
