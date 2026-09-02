package main

import (
	"io"
	"strings"
	"testing"
)

func TestRun_VersionReportsTheInjectedBuildMetadata(t *testing.T) {
	var out strings.Builder
	if err := run([]string{"version"}, &out, io.Discard); err != nil {
		t.Fatalf("run(version) = %v", err)
	}

	for _, want := range []string{version, commit, buildDate} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("version output %q does not contain %q", out.String(), want)
		}
	}
}

func TestRun_UnknownCommandIsAnError(t *testing.T) {
	var out strings.Builder
	err := run([]string{"nonsense"}, &out, io.Discard)
	if err == nil {
		t.Fatal("run(nonsense) = nil, want an error")
	}
	if out.Len() != 0 {
		t.Errorf("unknown command wrote %q to stdout, want nothing", out.String())
	}
}

func TestRun_ServeUsesTheConfiguredPath(t *testing.T) {
	var out strings.Builder
	// A config path that cannot be created fails before anything is started,
	// which is enough to prove the flag is honoured without booting a server.
	err := run([]string{"serve", "--config", "/proc/nonexistent/config.yml"}, &out, io.Discard)
	if err == nil {
		t.Fatal("serve with an unusable config path returned no error")
	}
}

// SQLite's cache is per connection, so the ceiling is every connection of every
// database holding a full one at once. It is a ceiling rather than a resident cost, but
// a NAS still has to be able to reach it, and one figure applied to all three databases
// put it over a gigabyte (documentation/database-layout.md).
func TestDatabaseProfiles_KeepTheCacheCeilingInHand(t *testing.T) {
	const libraryCacheKB = 65536

	// Checkpoint connections are on top of this at 2 MiB each, fixed in the store.
	totalKB := 0
	for _, profile := range []databaseProfile{libraryProfile, jobsProfile, settingsProfile} {
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
