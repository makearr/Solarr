package settings

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"unicode"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
)

// KeyBrainzMashUserAgent is the agent the BrainzMash source sends, read live on every request.
const KeyBrainzMashUserAgent = "metadata.brainzmash.user_agent"

// DefaultBrainzMashUserAgent passes BrainzMash's front door, which refuses agents not starting "Lidarr/".
const DefaultBrainzMashUserAgent = "Lidarr/2.9.6.4552"

// maxUserAgentLength bounds a header value nobody legitimately needs longer.
const maxUserAgentLength = 256

// KeyMetadataSourceOrder is the order metadata sources are tried in, read live
// on every lookup (the user's own 2026-09-17 request). Advanced tier: a working
// library needs none of it changed.
const KeyMetadataSourceOrder = "metadata.source_order"

// The metadata source names, matching musicbrainz's own constants. Duplicated
// rather than imported: internal/db/settings may not depend on a provider
// (ADR-25 layering), the same split the transfer ladder already carries.
const (
	SourceBrainzMash  = "brainzmash"
	SourceLidarrProxy = "musicbrainz-proxy"
	SourceMusicBrainz = "musicbrainz"
)

// DefaultMetadataSourceOrder is ADR-29's own order, and the one the user gave:
// BrainzMash, the Lidarr proxy, then direct MusicBrainz.
var DefaultMetadataSourceOrder = []string{SourceBrainzMash, SourceLidarrProxy, SourceMusicBrainz}

// ValidMetadataSource reports whether name is a source that can be ordered.
func ValidMetadataSource(name string) bool {
	switch name {
	case SourceBrainzMash, SourceLidarrProxy, SourceMusicBrainz:
		return true
	default:
		return false
	}
}

// Enrichment switches (2026-09-19 review, answered 2026-09-24: per media type, shown per library).
const (
	// KeyMetadataCoverSources orders where album covers come from; a source left out is off.
	KeyMetadataCoverSources = "metadata.cover_sources"
	// KeyMetadataWikidata turns Wikidata's artist photos and biographies on or off.
	KeyMetadataWikidata = "metadata.wikidata"
	// KeyMetadataAcoustID turns fingerprint identification on or off; it still needs its key.
	KeyMetadataAcoustID = "metadata.acoustid"
	// KeyMetadataTheAudioDB turns TheAudioDB's artist photos and biographies on or off (2026-09-25).
	KeyMetadataTheAudioDB = "metadata.theaudiodb"
	// KeyMetadataTVSources orders the TV providers: the first to hold a field supplies it; numbering stays with TheTVDB's.
	KeyMetadataTVSources = "metadata.tv_sources"
	// KeyMetadataPeopleBackfillPerRun bounds how many artists one enrichment run gives a photo and biography.
	KeyMetadataPeopleBackfillPerRun = "metadata.people_backfill_per_run"
)

// How long after its time a part (an episode) is waited for before it is owed, by how sure that time is: an exact
// time, a likely one, a date alone (section 19.2; settings since 2026-10-14).
const (
	KeyAvailabilityGraceHigh   = "availability.grace_high_hours"
	KeyAvailabilityGraceMedium = "availability.grace_medium_hours"
	KeyAvailabilityGraceLow    = "availability.grace_low_hours"
)

// DefaultAvailabilityGrace is the grace a library waits when none is set.
var DefaultAvailabilityGrace = AvailabilityGraceHours{High: 2, Medium: 12, Low: 36}

// maxAvailabilityGraceHours is two weeks: a part later than that is better marked by hand.
const maxAvailabilityGraceHours = 336

// AvailabilityGraceHours is the wait after a part's time, in hours, by how sure the time is.
type AvailabilityGraceHours struct{ High, Medium, Low int64 }

// DefaultPeopleBackfillPerRun suits BrainzMash's 10 req/s; the old 60 was sized for MusicBrainz's 1.
const DefaultPeopleBackfillPerRun = 200

// maxPeopleBackfillPerRun keeps one run from holding the metadata bucket for hours.
const maxPeopleBackfillPerRun = 10000

// The cover sources, in the artwork job's terms.
const (
	CoverSourceArchive    = "coverartarchive"
	CoverSourceEmbedded   = "embedded"
	CoverSourceTheAudioDB = "theaudiodb"
)

// DefaultCoverSources is the archive, then the files, then TheAudioDB for what neither has.
var DefaultCoverSources = []string{CoverSourceArchive, CoverSourceEmbedded, CoverSourceTheAudioDB}

func validCoverSource(name string) bool {
	return name == CoverSourceArchive || name == CoverSourceEmbedded || name == CoverSourceTheAudioDB
}

// The TV providers, matching internal/media/tv's own names.
const (
	TVSourceSkyhook = "skyhook"
	TVSourceTVmaze  = "tvmaze"
	TVSourceTMDB    = "tmdb"
	TVSourceTVDB    = "tvdb"
	TVSourceAniList = "anilist"
)

// DefaultTVSources is the measured order (documentation/tv-providers-benchmark.md, 2026-10-03): Skyhook answers fastest
// and complete, TMDB adds cast, logos and certification quickly, TheTVDB direct is slower for the same facts, TVmaze
// is slowest and thinnest but alone knows time zones, and AniList, which only knows anime, comes last.
var DefaultTVSources = []string{TVSourceSkyhook, TVSourceTMDB, TVSourceTVDB, TVSourceTVmaze, TVSourceAniList}

func validTVSource(name string) bool {
	return slices.Contains(DefaultTVSources, name)
}

// MetadataSettings is the metadata providers' own policy.
type MetadataSettings struct {
	BrainzMashUserAgent string
	// SourceOrder is the order sources are tried. A source left out is not
	// consulted at all -- except direct MusicBrainz, which the provider always
	// falls back to, since something has to answer (ADR-29). Empty on a write
	// means "leave the stored order alone", so a caller changing only the
	// agent need not restate it -- the same convention SubscriptionFilters
	// already uses for an unset field.
	SourceOrder []string
	// CoverSources is the order covers are taken in; nil on a write leaves it alone, and an empty
	// non-nil list turns every cover source off.
	CoverSources []string
	// TVSources is the order the TV providers rank in; nil on a write leaves it alone.
	TVSources []string
	// Wikidata and AcoustID are on unless switched off; nil on a write leaves them alone.
	Wikidata   *bool
	AcoustID   *bool
	TheAudioDB *bool
	// PeopleBackfillPerRun is how many artists one run backfills; nil on a write leaves it alone.
	PeopleBackfillPerRun *int64
	// AvailabilityGrace is the wait after a part's time before it is owed; nil on a write leaves it alone.
	AvailabilityGrace *AvailabilityGraceHours
}

// MetadataSettings reads the configured policy; an absent or empty agent is the shipped default.
func (s *Snapshot) MetadataSettings() MetadataSettings {
	agent := s.policy[KeyBrainzMashUserAgent]
	if agent == "" {
		agent = DefaultBrainzMashUserAgent
	}
	order := s.policyStringList(KeyMetadataSourceOrder)
	if len(order) == 0 {
		order = DefaultMetadataSourceOrder
	}
	wikidata, acoustID := s.policyBool(KeyMetadataWikidata, true), s.policyBool(KeyMetadataAcoustID, true)
	theAudioDB := s.policyBool(KeyMetadataTheAudioDB, true)
	backfill := s.policyInt(KeyMetadataPeopleBackfillPerRun, DefaultPeopleBackfillPerRun)
	grace := s.AvailabilityGrace()
	return MetadataSettings{
		BrainzMashUserAgent: agent, SourceOrder: order,
		CoverSources: s.policyStringListOr(KeyMetadataCoverSources, DefaultCoverSources),
		TVSources:    s.policyStringListOr(KeyMetadataTVSources, DefaultTVSources),
		Wikidata:     &wikidata, AcoustID: &acoustID, TheAudioDB: &theAudioDB,
		PeopleBackfillPerRun: &backfill, AvailabilityGrace: &grace,
	}
}

// AvailabilityGrace reads the wait after a part's time, the defaults where none is set.
func (s *Snapshot) AvailabilityGrace() AvailabilityGraceHours {
	return AvailabilityGraceHours{
		High:   s.policyInt(KeyAvailabilityGraceHigh, DefaultAvailabilityGrace.High),
		Medium: s.policyInt(KeyAvailabilityGraceMedium, DefaultAvailabilityGrace.Medium),
		Low:    s.policyInt(KeyAvailabilityGraceLow, DefaultAvailabilityGrace.Low),
	}
}

// SetMetadataSettings writes the metadata policy wholesale (ADR-44: validated on write).
func (c *Cache) SetMetadataSettings(ctx context.Context, cl clock.Clock, ms MetadataSettings) error {
	if problems := validateMetadataSettings(ms); len(problems) > 0 {
		return problems
	}
	if err := c.setPolicy(ctx, cl, KeyBrainzMashUserAgent, ms.BrainzMashUserAgent); err != nil {
		return err
	}
	if ms.CoverSources != nil {
		encoded, err := json.Marshal(ms.CoverSources)
		if err != nil {
			return err
		}
		if err := c.setPolicy(ctx, cl, KeyMetadataCoverSources, string(encoded)); err != nil {
			return err
		}
	}
	if ms.TVSources != nil {
		encoded, err := json.Marshal(ms.TVSources)
		if err != nil {
			return err
		}
		if err := c.setPolicy(ctx, cl, KeyMetadataTVSources, string(encoded)); err != nil {
			return err
		}
	}
	for key, value := range map[string]*bool{
		KeyMetadataWikidata: ms.Wikidata, KeyMetadataAcoustID: ms.AcoustID, KeyMetadataTheAudioDB: ms.TheAudioDB,
	} {
		if value == nil {
			continue
		}
		if err := c.setPolicy(ctx, cl, key, strconv.FormatBool(*value)); err != nil {
			return err
		}
	}
	if ms.PeopleBackfillPerRun != nil {
		if err := c.setPolicy(ctx, cl, KeyMetadataPeopleBackfillPerRun,
			strconv.FormatInt(*ms.PeopleBackfillPerRun, 10)); err != nil {
			return err
		}
	}
	if grace := ms.AvailabilityGrace; grace != nil {
		for key, hours := range map[string]int64{
			KeyAvailabilityGraceHigh: grace.High, KeyAvailabilityGraceMedium: grace.Medium, KeyAvailabilityGraceLow: grace.Low,
		} {
			if err := c.setPolicy(ctx, cl, key, strconv.FormatInt(hours, 10)); err != nil {
				return err
			}
		}
	}
	if len(ms.SourceOrder) == 0 {
		return nil
	}
	encoded, err := json.Marshal(ms.SourceOrder)
	if err != nil {
		return err
	}
	return c.setPolicy(ctx, cl, KeyMetadataSourceOrder, string(encoded))
}

func validateMetadataSettings(ms MetadataSettings) config.Problems {
	var problems config.Problems
	if len(ms.SourceOrder) > 0 && !slices.Contains(ms.SourceOrder, SourceMusicBrainz) {
		// Direct MusicBrainz can be reordered but not removed: it is the one
		// source that is always reachable, and the others are proxies of it.
		problems = append(problems, config.Problem{
			Path: KeyMetadataSourceOrder, Expectation: "direct MusicBrainz cannot be removed, only reordered",
		})
	}
	seen := make(map[string]bool, len(ms.SourceOrder))
	for _, name := range ms.SourceOrder {
		if !ValidMetadataSource(name) {
			problems = append(problems, config.Problem{
				Path: KeyMetadataSourceOrder, Value: name,
				Expectation: "one of brainzmash, musicbrainz-proxy, musicbrainz",
			})
			continue
		}
		if seen[name] {
			problems = append(problems, config.Problem{
				Path: KeyMetadataSourceOrder, Value: name, Expectation: "each source named at most once",
			})
		}
		seen[name] = true
	}
	coverSeen := make(map[string]bool, len(ms.CoverSources))
	for _, name := range ms.CoverSources {
		if !validCoverSource(name) || coverSeen[name] {
			problems = append(problems, config.Problem{
				Path: KeyMetadataCoverSources, Value: name,
				Expectation: "coverartarchive, embedded or theaudiodb, each at most once",
			})
		}
		coverSeen[name] = true
	}
	tvSeen := make(map[string]bool, len(ms.TVSources))
	for _, name := range ms.TVSources {
		if !validTVSource(name) || tvSeen[name] {
			problems = append(problems, config.Problem{
				Path: KeyMetadataTVSources, Value: name,
				Expectation: "skyhook, tvmaze, tmdb, tvdb or anilist, each at most once",
			})
		}
		tvSeen[name] = true
	}
	if grace := ms.AvailabilityGrace; grace != nil {
		for key, hours := range map[string]int64{
			KeyAvailabilityGraceHigh: grace.High, KeyAvailabilityGraceMedium: grace.Medium, KeyAvailabilityGraceLow: grace.Low,
		} {
			if hours < 0 || hours > maxAvailabilityGraceHours {
				problems = append(problems, config.Problem{Path: key, Value: strconv.FormatInt(hours, 10),
					Expectation: "0 to " + strconv.Itoa(maxAvailabilityGraceHours) + " hours"})
			}
		}
	}
	if perRun := ms.PeopleBackfillPerRun; perRun != nil && (*perRun < 1 || *perRun > maxPeopleBackfillPerRun) {
		problems = append(problems, config.Problem{
			Path: KeyMetadataPeopleBackfillPerRun, Value: strconv.FormatInt(*perRun, 10),
			Expectation: "1 to " + strconv.Itoa(maxPeopleBackfillPerRun),
		})
	}
	agent := ms.BrainzMashUserAgent
	valid := agent != "" && len(agent) <= maxUserAgentLength
	for _, r := range agent {
		if r > unicode.MaxASCII || unicode.IsControl(r) {
			valid = false
		}
	}
	if !valid {
		problems = append(problems, config.Problem{
			Path: "brainzmash_user_agent", Value: agent,
			Expectation: "printable ASCII, 1 to " + strconv.Itoa(maxUserAgentLength) + " characters",
		})
	}
	return problems
}
