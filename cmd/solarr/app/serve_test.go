package app

import "testing"

// SQLite's cache is per connection, so the ceiling is every connection of every
// database holding a full one at once. It is a ceiling rather than a resident cost, but
// a NAS still has to be able to reach it, and one figure applied to every database
// put it over a gigabyte (documentation/database-layout.md).
func TestDatabaseProfiles_KeepTheCacheCeilingInHand(t *testing.T) {
	const libraryCacheKB = 65536

	// Checkpoint connections are on top of this at 2 MiB each, fixed in the store.
	totalKB := 0
	for _, profile := range []databaseProfile{libraryProfile, jobsProfile, settingsProfile, statisticsProfile} {
		cacheKB := profile.cacheSizeKB
		if cacheKB == 0 {
			cacheKB = libraryCacheKB
		}
		totalKB += (1 + profile.readPoolSize) * cacheKB
	}

	const ceilingKB = 384 << 10
	if totalKB > ceilingKB {
		t.Errorf("the configured page caches reach %d KiB, over the %d KiB ceiling",
			totalKB, ceilingKB)
	}
	// The library keeps everything it had; shrinking the others is the whole change.
	if libraryProfile.cacheSizeKB != 0 {
		t.Errorf("the library takes %d KiB from its profile, but config.yml is meant to size it",
			libraryProfile.cacheSizeKB)
	}
}
