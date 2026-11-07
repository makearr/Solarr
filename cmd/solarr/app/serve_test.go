package app

import (
	"testing"

	"github.com/makearr/Solarr/internal/platform/config"
)

// database.cache_size_kb is one ceiling for every connection of every database, so the connections' sizes added up
// must never pass it (documentation/database-layout.md).
func TestDatabaseProfiles_StayUnderTheOneCacheCeiling(t *testing.T) {
	for _, ceilingKB := range []int{config.DefaultCacheSizeKB, 131072, 1 << 20} {
		running := &process{config: config.Default()}
		running.config.Database.CacheSizeKB = ceilingKB

		totalKB := 0
		for _, profile := range []databaseProfile{libraryProfile, jobsProfile, settingsProfile, statisticsProfile, metadataProfile} {
			totalKB += (1 + profile.readPoolSize) * running.storeConfig(profile).CacheSizeKB
		}
		if totalKB > ceilingKB {
			t.Errorf("ceiling %d KiB: the connections reach %d KiB", ceilingKB, totalKB)
		}
	}
}
