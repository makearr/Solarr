package settings

import (
	"context"

	"github.com/makearr/Solarr/internal/platform/clock"
)

// Keys of the import's duplicate check: whether a download is compared with what the series holds, and whether a
// release found to add nothing is blocklisted.
const (
	KeyImportDuplicateCheck     = "import.duplicate_check"
	KeyImportDuplicateBlocklist = "import.duplicate_blocklist"
)

// ImportDuplicateCheck is whether an imported video is compared by its picture with the series' files; on unless turned off.
func (s *Snapshot) ImportDuplicateCheck() bool { return s.policyBool(KeyImportDuplicateCheck, true) }

// ImportDuplicateBlocklist is whether a release that duplicates a file the library holds is blocklisted; off unless turned on.
func (s *Snapshot) ImportDuplicateBlocklist() bool {
	return s.policyBool(KeyImportDuplicateBlocklist, false)
}

// SetImportDuplicates stores both switches together.
func (c *Cache) SetImportDuplicates(ctx context.Context, cl clock.Clock, check, blocklist bool) error {
	return c.setPolicies(ctx, cl, map[string]string{
		KeyImportDuplicateCheck: boolPolicy(check), KeyImportDuplicateBlocklist: boolPolicy(blocklist),
	})
}
