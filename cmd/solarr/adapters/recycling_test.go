package adapters

import (
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
)

type fixedLibraries map[int64]settings.Category

func (f fixedLibraries) Category(id int64) (settings.Category, bool) {
	category, found := f[id]
	return category, found
}

// A file in two libraries is kept as long as either wants, and its folders removed only if both say so.
func TestRecyclingFor_CombinesLibrariesCautiously(t *testing.T) {
	keepLong, tidy := settings.DefaultFileManagement, settings.DefaultFileManagement
	keepLong.RecycleRetentionDays = 30
	tidy.DeleteEmptyFolders = true
	libraries := fixedLibraries{1: {ID: 1, FileManagement: keepLong}, 2: {ID: 2, FileManagement: tidy}}

	cases := []struct {
		name        string
		categoryIDs []int64
		want        library.Recycling
	}{
		{"both", []int64{1, 2}, library.Recycling{Retain: 30 * 24 * time.Hour, RemoveEmptyFolders: false}},
		{"tidy alone", []int64{2}, library.Recycling{Retain: 7 * 24 * time.Hour, RemoveEmptyFolders: true}},
		{"no library", nil, library.Recycling{Retain: 7 * 24 * time.Hour, RemoveEmptyFolders: false}},
	}
	for _, testCase := range cases {
		if got := RecyclingFor(libraries, testCase.categoryIDs); got != testCase.want {
			t.Errorf("%s: %+v, want %+v", testCase.name, got, testCase.want)
		}
	}
}
