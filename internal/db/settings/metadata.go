package settings

import (
	"context"
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

// MetadataSettings is the metadata providers' own policy.
type MetadataSettings struct {
	BrainzMashUserAgent string
}

// MetadataSettings reads the configured policy; an absent or empty agent is the shipped default.
func (s *Snapshot) MetadataSettings() MetadataSettings {
	agent := s.policy[KeyBrainzMashUserAgent]
	if agent == "" {
		agent = DefaultBrainzMashUserAgent
	}
	return MetadataSettings{BrainzMashUserAgent: agent}
}

// SetMetadataSettings writes the metadata policy wholesale (ADR-44: validated on write).
func (c *Cache) SetMetadataSettings(ctx context.Context, cl clock.Clock, ms MetadataSettings) error {
	if problems := validateMetadataSettings(ms); len(problems) > 0 {
		return problems
	}
	return c.setPolicy(ctx, cl, KeyBrainzMashUserAgent, ms.BrainzMashUserAgent)
}

func validateMetadataSettings(ms MetadataSettings) config.Problems {
	agent := ms.BrainzMashUserAgent
	valid := agent != "" && len(agent) <= maxUserAgentLength
	for _, r := range agent {
		if r > unicode.MaxASCII || unicode.IsControl(r) {
			valid = false
		}
	}
	if valid {
		return nil
	}
	return config.Problems{{
		Path: "brainzmash_user_agent", Value: agent,
		Expectation: "printable ASCII, 1 to " + strconv.Itoa(maxUserAgentLength) + " characters",
	}}
}
