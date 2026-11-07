package settings

import (
	"context"

	"github.com/makearr/Solarr/internal/platform/clock"
)

// KeyVideoPerceptualHash switches the background perceptual hashing of video files (ADR-35) on or off.
const KeyVideoPerceptualHash = "scan.video_phash"

// VideoPerceptualHash is whether the probe sweep hashes videos; off unless an administrator turned it on.
func (s *Snapshot) VideoPerceptualHash() bool { return s.policyBool(KeyVideoPerceptualHash, false) }

// SetVideoPerceptualHash turns the hashing on or off; hashes already stored stay and still serve the duplicate finder.
func (c *Cache) SetVideoPerceptualHash(ctx context.Context, cl clock.Clock, enabled bool) error {
	return c.setPolicies(ctx, cl, map[string]string{KeyVideoPerceptualHash: boolPolicy(enabled)})
}

// KeyVideoPhashFrameBits is how many of a frame hash's 64 bits may differ for two frames to count as the same picture.
const KeyVideoPhashFrameBits = "scan.video_phash_frame_bits"

// Bounds of the frame distance: wider than the upper one calls different pictures alike.
const (
	DefaultVideoPhashFrameBits = 10
	MinVideoPhashFrameBits     = 2
	MaxVideoPhashFrameBits     = 20
)

// VideoPhashFrameBits is the frame distance at the medium similarity level, kept within its bounds.
func (s *Snapshot) VideoPhashFrameBits() int {
	return int(min(MaxVideoPhashFrameBits, max(MinVideoPhashFrameBits, s.policyInt(KeyVideoPhashFrameBits, DefaultVideoPhashFrameBits))))
}

// KeyVideoPreviews switches the preview pictures made from the frames the hashing reads.
const KeyVideoPreviews = "scan.video_previews"

// VideoPreviews is whether the probe sweep makes a single frame and a thumbnail sheet of each video; off unless turned on.
func (s *Snapshot) VideoPreviews() bool { return s.policyBool(KeyVideoPreviews, false) }

// SetVideoPreviews turns the previews on or off. On, it turns the hashing on too, since both read the same frames and
// the hashing then costs next to nothing; off leaves the hashing as it is and the pictures made stay.
func (c *Cache) SetVideoPreviews(ctx context.Context, cl clock.Clock, enabled bool) error {
	changes := map[string]string{KeyVideoPreviews: boolPolicy(enabled)}
	if enabled {
		changes[KeyVideoPerceptualHash] = boolPolicy(true)
	}
	return c.setPolicies(ctx, cl, changes)
}
