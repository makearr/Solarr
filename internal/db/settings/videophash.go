package settings

import (
	"context"

	"github.com/makearr/Solarr/internal/platform/clock"
)

// KeyVideoPerceptualHash switches the background perceptual hashing of video files (ADR-35) on or off.
const KeyVideoPerceptualHash = "scan.video_phash"

// VideoPerceptualHash is whether the probe sweep hashes videos; on unless an administrator turned it off.
func (s *Snapshot) VideoPerceptualHash() bool { return s.policyBool(KeyVideoPerceptualHash, true) }

// SetVideoPerceptualHash turns the hashing on or off; hashes already stored stay and still serve the duplicate finder.
func (c *Cache) SetVideoPerceptualHash(ctx context.Context, cl clock.Clock, enabled bool) error {
	return c.setPolicies(ctx, cl, map[string]string{KeyVideoPerceptualHash: boolPolicy(enabled)})
}
