package settings

import (
	"context"
	"strconv"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
)

// Section 10.8's space bound on the recycle bin, beside each library's own retention in days.
const (
	KeyRecycleBinMaxBytes         = "recycle_bin.max_bytes"
	KeyRecycleBinMaxPercentOfRoot = "recycle_bin.max_percent_of_root"
)

// DefaultRecycleBinMaxPercentOfRoot keeps a root's bin under a tenth of the root; no byte ceiling by default.
const DefaultRecycleBinMaxPercentOfRoot = 10

// RecycleBinLimits bounds the bin by space: all bins together, and each root's bin against the root.
// Zero turns a bound off; the oldest entries go first once one is exceeded.
type RecycleBinLimits struct {
	MaxBytes         int64
	MaxPercentOfRoot int64
}

// RecycleBinLimits reads the limits, or the defaults for any not stored.
func (s *Snapshot) RecycleBinLimits() RecycleBinLimits {
	return RecycleBinLimits{
		MaxBytes:         s.policyInt(KeyRecycleBinMaxBytes, 0),
		MaxPercentOfRoot: s.policyInt(KeyRecycleBinMaxPercentOfRoot, DefaultRecycleBinMaxPercentOfRoot),
	}
}

// SetRecycleBinLimits writes the limits, validated on write (ADR-44).
func (c *Cache) SetRecycleBinLimits(ctx context.Context, cl clock.Clock, limits RecycleBinLimits) error {
	if problems := validateRecycleBinLimits(limits); len(problems) > 0 {
		return problems
	}
	return c.setPolicies(ctx, cl, formatInts(map[string]int64{
		KeyRecycleBinMaxBytes: limits.MaxBytes, KeyRecycleBinMaxPercentOfRoot: limits.MaxPercentOfRoot,
	}))
}

func validateRecycleBinLimits(limits RecycleBinLimits) config.Problems {
	var problems config.Problems
	if limits.MaxBytes < 0 {
		problems = append(problems, config.Problem{Path: "max_bytes",
			Value: strconv.FormatInt(limits.MaxBytes, 10), Expectation: "0 (no limit) or a number of bytes"})
	}
	if limits.MaxPercentOfRoot < 0 || limits.MaxPercentOfRoot > 100 {
		problems = append(problems, config.Problem{Path: "max_percent_of_root",
			Value: strconv.FormatInt(limits.MaxPercentOfRoot, 10), Expectation: "0 (no limit) to 100 percent"})
	}
	return problems
}
