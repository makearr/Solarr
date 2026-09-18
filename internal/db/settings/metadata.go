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
	return MetadataSettings{BrainzMashUserAgent: agent, SourceOrder: order}
}

// SetMetadataSettings writes the metadata policy wholesale (ADR-44: validated on write).
func (c *Cache) SetMetadataSettings(ctx context.Context, cl clock.Clock, ms MetadataSettings) error {
	if problems := validateMetadataSettings(ms); len(problems) > 0 {
		return problems
	}
	if err := c.setPolicy(ctx, cl, KeyBrainzMashUserAgent, ms.BrainzMashUserAgent); err != nil {
		return err
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
