package settings

import (
	"context"

	"github.com/makearr/Solarr/internal/platform/clock"
)

// KeyVerifyFiles switches the check that decodes the end of each probed file to find cut-short ones.
const KeyVerifyFiles = "scan.verify_files"

// VerifyFiles is whether the probe decodes the end of each file; on by default.
func (s *Snapshot) VerifyFiles() bool { return s.policyBool(KeyVerifyFiles, true) }

// SetVerifyFiles turns the end-of-file check on or off.
func (c *Cache) SetVerifyFiles(ctx context.Context, cl clock.Clock, enabled bool) error {
	return c.setPolicies(ctx, cl, map[string]string{KeyVerifyFiles: boolPolicy(enabled)})
}
