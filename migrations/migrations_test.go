package migrations_test

import (
	"regexp"
	"testing"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/migrations"
)

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Every database starts from one squashed baseline; a mangled marker would make an existing database
// refuse to start (ErrMigrationChanged), so the checksum it carries is checked for shape here.
func TestEveryDatabase_StartsFromABaseline(t *testing.T) {
	for name, load := range map[string]func() (store.Owner, error){
		"settings": migrations.Settings, "jobs": migrations.Jobs, "library": migrations.Library,
		"metadata": migrations.Metadata, "statistics": migrations.Statistics,
	} {
		owner, err := load()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		first := owner.Migrations[0]
		if !sha256Hex.MatchString(first.Replaces) {
			t.Errorf("%s: first migration %d replaces %q, want a sha256 checksum", name, first.Version, first.Replaces)
		}
		for _, later := range owner.Migrations[1:] {
			if later.Replaces != "" {
				t.Errorf("%s: migration %d is a second baseline", name, later.Version)
			}
		}
	}
}
