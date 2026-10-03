package tv

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/metadata/anilist"
	"github.com/makearr/Solarr/internal/metadata/skyhook"
	"github.com/makearr/Solarr/internal/metadata/tvmaze"
)

const (
	sourceSkyhook = "skyhook"
	sourceTVmaze  = "tvmaze"
	sourceAniList = "anilist"
)

// FromSkyhook reads Sonarr's TheTVDB proxy answer into the module's own types.
func FromSkyhook(in skyhook.Series) Series {
	series := Series{
		IDs: IDs{
			TVDB: in.TVDBID, TVmaze: in.TVMazeID, TMDB: in.TMDBID, IMDb: in.IMDbID,
			AniDB: in.AniDBIDs, MAL: in.MALIDs, AniList: in.AniListIDs,
		},
		Title: in.Title, Overview: in.Overview, FirstAired: in.FirstAired, LastAired: in.LastAired,
		Year: yearOf(in.FirstAired), Status: statusOf(in.Status), RuntimeMin: in.Runtime,
		Network: in.Network, OriginalNetwork: in.OriginalNetwork, Genres: in.Genres, ContentRating: in.ContentRating,
		OriginalLanguage: in.OriginalLanguage, OriginalCountry: in.OriginalCountry, Sources: []string{sourceSkyhook},
	}
	if score, err := strconv.ParseFloat(in.Rating.Value, 64); err == nil {
		series.Rating, series.RatingVotes = score, in.Rating.Count
	}
	if in.TimeOfDay != nil {
		series.AirTime = &AirTime{Hour: in.TimeOfDay.Hours, Minute: in.TimeOfDay.Minutes}
	}
	for _, actor := range in.Actors {
		series.Cast = append(series.Cast, Person{Name: actor.Name, Character: actor.Character, Image: actor.Image})
	}
	for _, image := range in.Images {
		series.Images = append(series.Images, Image{Kind: imageKind(image.CoverType), URL: image.URL, Source: sourceSkyhook})
	}
	for _, season := range in.Seasons {
		entry := Season{Number: season.SeasonNumber}
		for _, image := range season.Images {
			switch imageKind(image.CoverType) {
			case "poster":
				entry.Poster = image.URL
			case "banner":
				entry.Banner = image.URL
			}
		}
		series.Seasons = append(series.Seasons, entry)
	}
	for _, episode := range in.Episodes {
		series.Episodes = append(series.Episodes, skyhookEpisode(episode))
	}
	return series
}

func skyhookEpisode(in skyhook.Episode) Episode {
	episode := Episode{
		Season: in.SeasonNumber, Number: in.EpisodeNumber, Title: in.Title, Overview: in.Overview,
		AirDate: in.AirDate, RuntimeMin: in.Runtime, Image: in.Image, Special: in.SeasonNumber == 0,
		IDs: map[string]int64{ProviderTVDB: in.TVDBID}, AirConfidence: ConfidenceUnknown,
	}
	if in.AbsoluteEpisodeNumber != nil {
		episode.Absolute = *in.AbsoluteEpisodeNumber
	}
	if in.AiredBeforeSeasonNumber != nil {
		episode.AiredBeforeSeason = *in.AiredBeforeSeasonNumber
	}
	if in.AiredBeforeEpisodeNumber != nil {
		episode.AiredBeforeEpisode = *in.AiredBeforeEpisodeNumber
	}
	switch stamp, err := time.Parse(time.RFC3339, in.AirDateUTC); {
	case err == nil:
		episode.AirUTC, episode.AirConfidence, episode.AirBasis = stamp, ConfidenceMedium, "tvdb air time"
	case in.AirDate != "":
		episode.AirConfidence, episode.AirBasis = ConfidenceLow, "tvdb air date"
	}
	return episode
}

// FromTVmaze reads TVmaze's answers for one show into the module's own types.
func FromTVmaze(show tvmaze.Show, episodes []tvmaze.Episode, aliases []string, cast []tvmaze.CastMember) Series {
	series := Series{
		IDs:   IDs{TVmaze: show.ID, IMDb: show.Externals.IMDb},
		Title: show.Name, Aliases: aliases, Overview: stripTags(show.Summary), FirstAired: show.Premiered, LastAired: show.Ended,
		Year: yearOf(show.Premiered), Status: statusOfTVmaze(show.Status), RuntimeMin: firstPositive(show.AverageRuntime, show.Runtime),
		Genres: show.Genres, OriginalLanguage: show.Language, Rating: show.Rating.Average, Sources: []string{sourceTVmaze},
	}
	if show.Externals.TheTVDB != nil {
		series.IDs.TVDB = *show.Externals.TheTVDB
	}
	channel := show.Network
	if channel == nil {
		channel = show.WebChannel
	}
	timezone := ""
	if channel != nil {
		series.Network = channel.Name
		if channel.Country != nil {
			series.OriginalCountry, timezone = channel.Country.Code, channel.Country.Timezone
		}
	}
	if hour, minute, ok := clockOf(show.Schedule.Time); ok {
		series.AirTime = &AirTime{Hour: hour, Minute: minute, Timezone: timezone}
	}
	if show.Image != nil {
		series.Images = append(series.Images, Image{Kind: "poster", URL: show.Image.Original, Source: sourceTVmaze})
	}
	for _, member := range cast {
		person := Person{Name: member.Person.Name, Character: member.Character.Name}
		if member.Person.Image != nil {
			person.Image = member.Person.Image.Original
		}
		series.Cast = append(series.Cast, person)
	}
	for _, episode := range episodes {
		if episode.Number == nil {
			continue
		}
		series.Episodes = append(series.Episodes, tvmazeEpisode(episode, timezone))
	}
	return series
}

func tvmazeEpisode(in tvmaze.Episode, timezone string) Episode {
	episode := Episode{
		Season: in.Season, Number: *in.Number, Title: in.Name, Overview: stripTags(in.Summary), AirDate: in.Airdate,
		RuntimeMin: in.Runtime, Special: in.Season == 0 || in.Type != "regular", IDs: map[string]int64{ProviderTVmaze: in.ID},
		AirConfidence: ConfidenceUnknown,
	}
	if in.Image != nil {
		episode.Image = in.Image.Original
	}
	if stamp, err := time.Parse(time.RFC3339, in.Airstamp); err == nil && in.Airtime != "" {
		episode.AirUTC, episode.AirConfidence = stamp, ConfidenceHigh
		episode.AirBasis = "tvmaze airstamp"
		if timezone != "" {
			episode.AirBasis += " tz=" + timezone
		}
	} else if in.Airdate != "" {
		episode.AirConfidence, episode.AirBasis = ConfidenceLow, "tvmaze air date"
	}
	return episode
}

// FromAniList reads the entries AniList lists for one anime series into aliases and a banner: the romaji, English and
// native titles and the synonyms are what release groups name anime by.
func FromAniList(entries []anilist.Media) Series {
	series := Series{Sources: []string{sourceAniList}}
	for _, entry := range entries {
		for _, title := range entry.Titles() {
			series.Aliases = appendDistinct(series.Aliases, title)
		}
		if entry.BannerImage != "" && !hasImage(series.Images, entry.BannerImage) {
			series.Images = append(series.Images, Image{Kind: "banner", URL: entry.BannerImage, Source: sourceAniList})
		}
	}
	return series
}

func hasImage(images []Image, url string) bool {
	for _, image := range images {
		if image.URL == url {
			return true
		}
	}
	return false
}

// Merge combines what several sources know about one series, the first source winning field by field (ADR-8): a later
// source fills what the earlier left empty, adds aliases, and supplies a more trustworthy air time.
func Merge(sources ...Series) Series {
	var merged Series
	for index, source := range sources {
		if index == 0 {
			merged = source
			merged.Sources = append([]string(nil), source.Sources...)
			continue
		}
		mergeInto(&merged, source)
	}
	return merged
}

func mergeInto(into *Series, other Series) {
	into.Sources = append(into.Sources, other.Sources...)
	fillString(&into.Title, other.Title)
	fillString(&into.Overview, other.Overview)
	fillString(&into.FirstAired, other.FirstAired)
	fillString(&into.LastAired, other.LastAired)
	fillString(&into.Network, other.Network)
	fillString(&into.OriginalNetwork, other.OriginalNetwork)
	fillString(&into.ContentRating, other.ContentRating)
	fillString(&into.OriginalLanguage, other.OriginalLanguage)
	fillString(&into.OriginalCountry, other.OriginalCountry)
	if into.Year == 0 {
		into.Year = other.Year
	}
	if into.Status == "" {
		into.Status = other.Status
	}
	if into.RuntimeMin == 0 {
		into.RuntimeMin = other.RuntimeMin
	}
	if into.Rating == 0 {
		into.Rating, into.RatingVotes = other.Rating, other.RatingVotes
	}
	if len(into.Genres) == 0 {
		into.Genres = other.Genres
	}
	if len(into.Cast) == 0 {
		into.Cast = other.Cast
	}
	if len(into.Credits) == 0 {
		into.Credits = other.Credits
	}
	if len(into.Networks) == 0 {
		into.Networks = other.Networks
	}
	if len(into.Keywords) == 0 {
		into.Keywords = other.Keywords
	}
	if len(into.Seasons) == 0 {
		into.Seasons = other.Seasons
	}
	into.Images = append(into.Images, other.Images...)
	into.Aliases = appendDistinct(into.Aliases, other.Aliases...)
	mergeIDs(&into.IDs, other.IDs)
	mergeAirTime(into, other)
	mergeEpisodes(into, other)
}

// PreferRicher lets the richer source win prose, genres, certification and artwork; identity stays with the numbering
// basis, so a library named after TheTVDB keeps its names.
func PreferRicher(into *Series, richer Series) {
	if strings.TrimSpace(richer.Overview) != "" {
		into.Overview = richer.Overview
	}
	if len(richer.Genres) > 0 {
		into.Genres = richer.Genres
	}
	if richer.ContentRating != "" {
		into.ContentRating = richer.ContentRating
	}
	into.Images = append(append([]Image(nil), richer.Images...), into.Images...)
}

func mergeAirTime(into *Series, other Series) {
	switch {
	case into.AirTime == nil:
		into.AirTime = other.AirTime
	case into.AirTime.Timezone == "" && other.AirTime != nil && other.AirTime.Timezone != "":
		// A clock time without its zone is the weaker half of the same fact.
		into.AirTime = other.AirTime
	}
}

func mergeEpisodes(into *Series, other Series) {
	if len(into.Episodes) == 0 {
		into.Episodes = other.Episodes
		return
	}
	byKey := make(map[Key]Episode, len(other.Episodes))
	for _, episode := range other.Episodes {
		byKey[Key{episode.Season, episode.Number}] = episode
	}
	for index := range into.Episodes {
		have := &into.Episodes[index]
		match, found := byKey[Key{have.Season, have.Number}]
		if !found {
			continue
		}
		for provider, id := range match.IDs {
			if have.IDs == nil {
				have.IDs = map[string]int64{}
			}
			have.IDs[provider] = id
		}
		fillString(&have.Title, match.Title)
		fillString(&have.Overview, match.Overview)
		fillString(&have.Image, match.Image)
		fillString(&have.AirDate, match.AirDate)
		if have.RuntimeMin == 0 {
			have.RuntimeMin = match.RuntimeMin
		}
		if rank(match.AirConfidence) > rank(have.AirConfidence) {
			have.AirUTC, have.AirConfidence, have.AirBasis = match.AirUTC, match.AirConfidence, match.AirBasis
		}
	}
}

func rank(confidence Confidence) int {
	switch confidence {
	case ConfidenceHigh:
		return 3
	case ConfidenceMedium:
		return 2
	case ConfidenceLow:
		return 1
	default:
		return 0
	}
}

func mergeIDs(into *IDs, other IDs) {
	if into.TVDB == 0 {
		into.TVDB = other.TVDB
	}
	if into.TVmaze == 0 {
		into.TVmaze = other.TVmaze
	}
	if into.TMDB == 0 {
		into.TMDB = other.TMDB
	}
	fillString(&into.IMDb, other.IMDb)
	if len(into.AniDB) == 0 {
		into.AniDB = other.AniDB
	}
	if len(into.MAL) == 0 {
		into.MAL = other.MAL
	}
	if len(into.AniList) == 0 {
		into.AniList = other.AniList
	}
}

func fillString(into *string, other string) {
	if strings.TrimSpace(*into) == "" {
		*into = other
	}
}

func appendDistinct(list []string, more ...string) []string {
	for _, candidate := range more {
		found := false
		for _, have := range list {
			if strings.EqualFold(have, candidate) {
				found = true
				break
			}
		}
		if !found && strings.TrimSpace(candidate) != "" {
			list = append(list, candidate)
		}
	}
	return list
}

func yearOf(date string) int {
	if len(date) < 4 {
		return 0
	}
	year, err := strconv.Atoi(date[:4])
	if err != nil {
		return 0
	}
	return year
}

func clockOf(text string) (hour, minute int, ok bool) {
	var h, m int
	if _, err := fmt.Sscanf(text, "%d:%d", &h, &m); err != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func statusOf(skyhookStatus string) Status {
	switch strings.ToLower(skyhookStatus) {
	case "continuing":
		return StatusContinuing
	case "ended":
		return StatusEnded
	case "upcoming":
		return StatusUpcoming
	}
	return ""
}

func statusOfTVmaze(status string) Status {
	switch strings.ToLower(status) {
	case "running":
		return StatusContinuing
	case "ended":
		return StatusEnded
	case "to be determined", "in development":
		return StatusUpcoming
	}
	return ""
}

func imageKind(coverType string) string {
	switch strings.ToLower(coverType) {
	case "poster":
		return "poster"
	case "banner":
		return "banner"
	case "fanart":
		return "fanart"
	case "logo":
		return "logo"
	case "clearart":
		return "clearart"
	}
	return strings.ToLower(coverType)
}

// stripTags turns TVmaze's HTML summaries into plain text.
func stripTags(html string) string {
	var out strings.Builder
	inTag := false
	for _, r := range html {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			out.WriteRune(r)
		}
	}
	return strings.TrimSpace(out.String())
}
