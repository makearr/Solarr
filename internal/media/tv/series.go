// Package tv is the TV module (stage 8): what a series and its episodes are, read from providers and merged
// field by field (ADR-8). Core sees works and parts only; the vocabulary here stays in this package (ADR-23).
package tv

import "time"

// Status is where a series stands in its run.
type Status string

// The three states Sonarr shows.
const (
	StatusContinuing Status = "continuing"
	StatusEnded      Status = "ended"
	StatusUpcoming   Status = "upcoming"
)

// Provider names, as stored in work_external_id and work_field.
const (
	ProviderTVDB   = "tvdb"
	ProviderTVmaze = "tvmaze"
	ProviderTMDB   = "tmdb"
	ProviderIMDb   = "imdb"
	ProviderAniDB  = "anidb"
	ProviderMAL    = "mal"
	// ProviderAniList is the AniList media id.
	ProviderAniList = "anilist"
)

// IDs are the identifiers one series carries at the providers; zero or empty means unknown.
type IDs struct {
	TVDB, TVmaze, TMDB int64
	IMDb               string
	AniDB, MAL         []int64
	AniList            []int64
}

// AirTime is the series' usual local air time and where it is read.
type AirTime struct {
	Hour, Minute int
	// Timezone is an IANA name; empty when only the clock time is known.
	Timezone string
}

// Confidence says how far an availability time can be trusted (section 19.2).
type Confidence string

// The four levels work_part.available_confidence stores.
const (
	ConfidenceHigh    Confidence = "high"
	ConfidenceMedium  Confidence = "medium"
	ConfidenceLow     Confidence = "low"
	ConfidenceUnknown Confidence = "unknown"
)

// Episode is one episode or special under the provider's own numbering.
type Episode struct {
	Season int
	Number int
	// Absolute is the running count across seasons, 0 when the provider has none.
	Absolute int
	Title    string
	Overview string
	// AirDate is the local date, yyyy-MM-dd; empty when unscheduled.
	AirDate string
	// AirUTC is the instant it airs, zero when only a date is known.
	AirUTC        time.Time
	AirConfidence Confidence
	AirBasis      string
	RuntimeMin    int
	Image         string
	// AiredBeforeSeason and AiredBeforeEpisode place a special between regular episodes; zero when unplaced.
	AiredBeforeSeason, AiredBeforeEpisode int
	// Special is true for season 0 and for extras a provider flags as specials.
	Special bool
	// IDs maps provider to that provider's episode id.
	IDs map[string]int64
}

// Key is the (season, number) pair an episode is found by.
type Key struct{ Season, Number int }

// Season carries a season's artwork.
type Season struct {
	Number int    `json:"number"`
	Poster string `json:"poster,omitempty"`
	Banner string `json:"banner,omitempty"`
}

// Person is a cast member and the character played.
type Person struct {
	Name      string `json:"name"`
	Character string `json:"character,omitempty"`
	Image     string `json:"image,omitempty"`
}

// Credit roles a series stores, in the credit table's vocabulary.
const (
	RoleActor             = "actor"
	RoleGuestStar         = "guest_star"
	RoleCreator           = "creator"
	RoleShowrunner        = "showrunner"
	RoleDirector          = "director"
	RoleWriter            = "writer"
	RoleExecutiveProducer = "executive_producer"
	RoleProducer          = "producer"
	RoleComposer          = "composer"
)

// Credit is one person's part in a series: the whole run, or one episode when Season and Episode are set.
type Credit struct {
	TMDBID int64
	Name   string
	Image  string
	Role   string
	// Character is the part played; empty for crew.
	Character string
	// Order ranks cast within the series or episode, 0 first.
	Order int
	// Season and Episode scope a credit to one episode (guest stars, directors, writers); zero means the series.
	Season, Episode int
	// EpisodeAirDate guards the scope: the credit is dropped when the merged episode aired on another date.
	EpisodeAirDate string
	// Episodes is how many episodes the person appears in, 0 when unknown.
	Episodes int
}

// NetworkRef is a broadcaster or streamer with the provider's id for it.
type NetworkRef struct {
	TMDBID  int64
	Name    string
	Country string
	Logo    string
}

// Image is one piece of series artwork of a kind: poster, banner, fanart, logo, clearart.
type Image struct {
	Kind   string `json:"kind"`
	URL    string `json:"url"`
	Source string `json:"source,omitempty"`
}

// Series is one show, merged from every source that knew it.
type Series struct {
	IDs              IDs
	Title            string
	Aliases          []string
	Overview         string
	Year             int
	FirstAired       string
	LastAired        string
	Status           Status
	RuntimeMin       int
	Network          string
	OriginalNetwork  string
	AirTime          *AirTime
	Genres           []string
	ContentRating    string
	Rating           float64
	RatingVotes      int
	OriginalLanguage string
	OriginalCountry  string
	Seasons          []Season
	Cast             []Person
	Credits          []Credit
	Networks         []NetworkRef
	Keywords         []string
	Images           []Image
	Episodes         []Episode
	// Sources lists the providers that contributed, in priority order.
	Sources []string
}
