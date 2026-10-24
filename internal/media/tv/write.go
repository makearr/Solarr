package tv

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media/screen"
)

// MediaType is the work media type of a series.
const MediaType = "tv"

// MonitorOption is Sonarr's "Monitor" choice when a series is added: which episodes start out wanted.
type MonitorOption string

// Sonarr's nine options. Existing wants the episodes that already have a file, which an add does not know yet; the
// scan marks them when it attaches files.
const (
	MonitorAll         MonitorOption = "all"
	MonitorFuture      MonitorOption = "future"
	MonitorMissing     MonitorOption = "missing"
	MonitorExisting    MonitorOption = "existing"
	MonitorRecent      MonitorOption = "recent"
	MonitorPilot       MonitorOption = "pilot"
	MonitorFirstSeason MonitorOption = "first_season"
	MonitorLastSeason  MonitorOption = "last_season"
	MonitorNone        MonitorOption = "none"
)

// Valid reports whether the option is one of the nine.
func (o MonitorOption) Valid() bool {
	switch o {
	case MonitorAll, MonitorFuture, MonitorMissing, MonitorExisting, MonitorRecent, MonitorPilot,
		MonitorFirstSeason, MonitorLastSeason, MonitorNone:
		return true
	}
	return false
}

// recentWindow is Sonarr's "Recent": episodes aired within this long ago, and every future one.
const recentWindow = 90 * 24 * time.Hour

// MediaRef is a part's reference: season and episode, with the colon that scopes it below its season item.
func MediaRef(season, episode int) string { return fmt.Sprintf("s%02d:e%02d", season, episode) }

// SeasonRef is a season item's reference.
func SeasonRef(season int) string { return fmt.Sprintf("s%02d", season) }

func sortKey(season, episode int) string { return fmt.Sprintf("s%04de%04d", season, episode) }

// monitors decides which episodes an add starts out wanting.
type monitors struct {
	option       MonitorOption
	specials     bool
	now          time.Time
	first, last  int
	pilotSeason  int
	pilotEpisode int
}

func newMonitors(option MonitorOption, specials bool, now time.Time, episodes []Episode) monitors {
	m := monitors{option: option, specials: specials, now: now}
	for _, episode := range episodes {
		if episode.Season == 0 {
			continue
		}
		if m.first == 0 || episode.Season < m.first {
			m.first = episode.Season
		}
		m.last = max(m.last, episode.Season)
	}
	m.pilotSeason, m.pilotEpisode = m.first, 1
	return m
}

func (m monitors) wants(episode Episode) bool {
	if episode.Season == 0 || episode.Special {
		return m.specials && m.option != MonitorNone
	}
	switch m.option {
	case MonitorAll, MonitorMissing:
		return true
	case MonitorFuture:
		return m.unaired(episode)
	case MonitorRecent:
		return m.unaired(episode) || m.within(episode, recentWindow)
	case MonitorPilot:
		return episode.Season == m.pilotSeason && episode.Number == m.pilotEpisode
	case MonitorFirstSeason:
		return episode.Season == m.first
	case MonitorLastSeason:
		return episode.Season == m.last
	}
	return false
}

func (m monitors) airedAt(episode Episode) (time.Time, bool) {
	if !episode.AirUTC.IsZero() {
		return episode.AirUTC, true
	}
	if episode.AirDate == "" {
		return time.Time{}, false
	}
	day, err := time.Parse("2006-01-02", episode.AirDate)
	return day, err == nil
}

func (m monitors) unaired(episode Episode) bool {
	at, known := m.airedAt(episode)
	return !known || at.After(m.now)
}

func (m monitors) within(episode Episode, window time.Duration) bool {
	at, known := m.airedAt(episode)
	return known && !at.After(m.now) && m.now.Sub(at) <= window
}

// WriteOptions say how a series is stored: what starts out monitored.
type WriteOptions struct {
	Monitor  MonitorOption
	Specials bool
	// Now is the clock the monitor choice reads; a refresh passes its own.
	Now time.Time
	// NewPart, when set, decides the monitoring of each part this write creates instead of Monitor.
	NewPart func(Episode) bool
	// Grace is the settings' wait after an episode's time, stamped on its part; nil leaves the default of its confidence.
	Grace *library.AvailabilityGrace
}

// ToWrite turns a merged series into what the library stores.
func ToWrite(series Series, options WriteOptions) library.SeriesWrite {
	runtime := int64(series.RuntimeMin) * 60_000
	var runtimePtr, yearPtr *int64
	if series.RuntimeMin > 0 {
		runtimePtr = &runtime
	}
	if series.Year > 0 {
		year := int64(series.Year)
		yearPtr = &year
	}
	source := ""
	if len(series.Sources) > 0 {
		source = series.Sources[0]
	}
	write := library.SeriesWrite{
		Work:             library.WorkIdentity{MediaType: MediaType, CanonicalTitle: series.Title, SortTitle: SortTitle(series.Title)},
		Year:             yearPtr,
		OriginalLanguage: series.OriginalLanguage,
		RuntimeMs:        runtimePtr,
		Description:      series.Overview,
		IDs:              idWrites(series.IDs),
		Aliases:          series.Aliases,
		AliasSource:      "series-merge",
		Genres:           series.Genres,
		GenreSource:      source,
		GenreTree:        screen.GenreTree.Key,
		Network:          series.Network,
		NetworkCountry:   series.OriginalCountry,
		Certification:    series.ContentRating,
		CertificationAge: screen.AgeOrUnrated(series.ContentRating),
		Fields:           fieldWrites(series, source),
	}
	if series.Rating > 0 {
		write.Rating = &library.SeriesRatingWrite{Provider: source, Score: series.Rating, Votes: int64(series.RatingVotes)}
	}
	if series.FirstAired != "" {
		write.Dates = append(write.Dates, library.SeriesDateWrite{Kind: "first_aired", Date: series.FirstAired, Source: source})
	}
	if series.LastAired != "" {
		write.Dates = append(write.Dates, library.SeriesDateWrite{Kind: "last_aired", Date: series.LastAired, Source: source})
	}

	write.Keywords = series.Keywords
	for _, network := range series.Networks {
		write.MoreNetworks = append(write.MoreNetworks, library.SeriesNetworkWrite{
			Name: network.Name, Country: network.Country, Provider: ProviderTMDB, ExternalID: strconv.FormatInt(network.TMDBID, 10), LogoURL: network.Logo,
		})
	}
	for _, credit := range series.Credits {
		if credit.TMDBID == 0 || credit.Name == "" {
			continue
		}
		write.Credits = append(write.Credits, creditWrite(credit))
	}

	monitoring := newMonitors(options.Monitor, options.Specials, options.Now, series.Episodes)
	wants := monitoring.wants
	if options.NewPart != nil {
		wants = options.NewPart
	}
	for _, episode := range series.Episodes {
		write.Parts = append(write.Parts, partWrite(episode, wants(episode), options.Grace))
	}
	return write
}

func creditWrite(credit Credit) library.SeriesCreditWrite {
	write := library.SeriesCreditWrite{
		Provider: ProviderTMDB, ExternalID: strconv.FormatInt(credit.TMDBID, 10), Name: credit.Name, ImageURL: credit.Image,
		Role: credit.Role, Character: credit.Character, Ord: credit.Order,
	}
	if credit.Season != 0 || credit.Episode != 0 {
		write.PartRef = MediaRef(credit.Season, credit.Episode)
	}
	return write
}

func partWrite(episode Episode, monitored bool, grace *library.AvailabilityGrace) library.SeriesPartWrite {
	part := library.SeriesPartWrite{
		MediaRef: MediaRef(episode.Season, episode.Number), SortKey: sortKey(episode.Season, episode.Number),
		Title: episode.Title, Date: episode.AirDate, MonitoredIfNew: monitored,
		AvailableConfidence: string(episode.AirConfidence), AvailableBasis: episode.AirBasis,
	}
	if episode.RuntimeMin > 0 {
		runtime := int64(episode.RuntimeMin) * 60_000
		part.RuntimeMs = &runtime
	}
	if !episode.AirUTC.IsZero() {
		at := episode.AirUTC.Unix()
		part.AvailableAt = &at
	} else if episode.AirDate == "" {
		// Listed with no date: coming, and not owed until it has one.
		part.AvailableBasis = library.AvailabilityUnannounced
	}
	if grace != nil && (part.AvailableAt != nil || episode.AirDate != "") {
		wait := grace.For(string(episode.AirConfidence))
		part.AvailableGrace = &wait
	}
	if episode.Absolute > 0 {
		part.AltNumbers = append(part.AltNumbers, library.AltNumberWrite{Scheme: "absolute", Provider: ProviderTVDB, Ref: strconv.Itoa(episode.Absolute)})
	}
	if episode.AirDate != "" {
		part.AltNumbers = append(part.AltNumbers, library.AltNumberWrite{Scheme: "date", Provider: "air", Ref: episode.AirDate})
	}
	for provider, id := range episode.IDs {
		part.ExternalIDs = append(part.ExternalIDs, library.ExternalIDWrite{Provider: provider + ":episode", ExternalID: strconv.FormatInt(id, 10)})
	}
	return part
}

func idWrites(ids IDs) []library.ExternalIDWrite {
	var writes []library.ExternalIDWrite
	add := func(provider string, id int64) {
		if id > 0 {
			writes = append(writes, library.ExternalIDWrite{Provider: provider, ExternalID: strconv.FormatInt(id, 10)})
		}
	}
	add(ProviderTVDB, ids.TVDB)
	add(ProviderTVmaze, ids.TVmaze)
	add(ProviderTMDB, ids.TMDB)
	if ids.IMDb != "" {
		writes = append(writes, library.ExternalIDWrite{Provider: ProviderIMDb, ExternalID: ids.IMDb})
	}
	for _, id := range ids.AniDB {
		add(ProviderAniDB, id)
	}
	for _, id := range ids.MAL {
		add(ProviderMAL, id)
	}
	for _, id := range ids.AniList {
		add(ProviderAniList, id)
	}
	return writes
}

func fieldWrites(series Series, source string) []library.SeriesFieldWrite {
	var fields []library.SeriesFieldWrite
	add := func(field, value string) {
		if strings.TrimSpace(value) != "" {
			fields = append(fields, library.SeriesFieldWrite{Field: field, Provider: source, Value: value})
		}
	}
	add("status", string(series.Status))
	add("network", series.Network)
	add("original_network", series.OriginalNetwork)
	add("original_country", series.OriginalCountry)
	add("numbering_basis", orDefault(series.Basis, ProviderTVDB+":aired"))
	if series.Type != "" {
		fields = append(fields, library.SeriesFieldWrite{Field: "series_type", Provider: "detected", Value: string(series.Type)})
	}
	if series.AirTime != nil {
		guess := ""
		if series.AirTime.Guessed {
			guess = " (guess)"
		}
		add("air_time", fmt.Sprintf("%02d:%02d %s%s", series.AirTime.Hour, series.AirTime.Minute, series.AirTime.Timezone, guess))
	}
	if len(series.Cast) > 0 {
		if encoded, err := json.Marshal(series.Cast); err == nil {
			add("cast", string(encoded))
		}
	}
	if len(series.Images) > 0 {
		if encoded, err := json.Marshal(series.Images); err == nil {
			add("images", string(encoded))
		}
	}
	if len(series.Seasons) > 0 {
		if encoded, err := json.Marshal(series.Seasons); err == nil {
			add("season_images", string(encoded))
		}
	}
	if stills := episodeStills(series.Episodes); len(stills) > 0 {
		if encoded, err := json.Marshal(stills); err == nil {
			add("episode_images", string(encoded))
		}
	}
	return fields
}

// episodeStills maps "season:episode" to the picture a provider holds of that episode.
func episodeStills(episodes []Episode) map[string]string {
	stills := map[string]string{}
	for _, episode := range episodes {
		if episode.Image != "" {
			stills[stillKey(episode.Season, episode.Number)] = episode.Image
		}
	}
	return stills
}

func stillKey(season, number int) string { return fmt.Sprintf("%d:%d", season, number) }

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// SortTitle lowercases a title, collapses whitespace and drops a leading article, so "The Show" sorts under S.
func SortTitle(title string) string {
	lowered := strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(title))), " ")
	for _, article := range []string{"the ", "a ", "an "} {
		if rest, ok := strings.CutPrefix(lowered, article); ok {
			return rest
		}
	}
	return lowered
}
