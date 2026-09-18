package migrations_test

import (
	"testing"

	"github.com/makearr/Solarr/migrations"
)

// A migration's DisablesForeignKeys comes from a marker comment inside its own file (checked into
// the checksum like everything else), not a separate registry -- this proves the marker is
// actually wired through load() end to end, not just present as a comment nobody reads.
func TestLibrary_DisableForeignKeysMarkerIsWiredThrough(t *testing.T) {
	owner, err := migrations.Library()
	if err != nil {
		t.Fatalf("Library: %v", err)
	}

	var marked, unmarked int
	for _, migration := range owner.Migrations {
		if migration.DisablesForeignKeys {
			marked++
		} else {
			unmarked++
		}
	}
	if marked == 0 {
		t.Error("no library migration carries the disable-foreign-keys marker; " +
			"is the migration that needs it missing, or has its marker been dropped?")
	}
	if unmarked == 0 {
		t.Error("every library migration carries the marker; want most of them not to " +
			"(it is an opt-in for the rare rebuild, not the default)")
	}
}
