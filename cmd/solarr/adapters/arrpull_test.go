package adapters

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"

	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/media"
)

func TestMapArrImportList_OnlyTheKindsSolarrReadsAreMapped(t *testing.T) {
	tests := []struct {
		name  string
		entry arrimport.ImportList
		want  media.ListType
		key   string
		value string
		ok    bool
	}{
		{"tmdb list", arrimport.ImportList{Implementation: "TMDbListImport", Fields: map[string]string{"listId": "8123"}}, media.ListTMDBList, "list", "8123", true},
		{"mdblist by link", arrimport.ImportList{Implementation: "MDBListImport", Fields: map[string]string{"linkUrl": "https://mdblist.com/lists/a/b"}}, media.ListMDBList, "mdblist", "https://mdblist.com/lists/a/b", true},
		{"trakt has no equivalent", arrimport.ImportList{Implementation: "TraktListImport", Fields: map[string]string{"listName": "x"}}, "", "", "", false},
		{"tmdb list without an id", arrimport.ImportList{Implementation: "TMDbListImport"}, "", "", "", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			listType, key, value, ok := mapArrImportList(test.entry)
			if listType != test.want || key != test.key || value != test.value || ok != test.ok {
				t.Fatalf("got (%q, %q, %q, %v), want (%q, %q, %q, %v)", listType, key, value, ok, test.want, test.key, test.value, test.ok)
			}
		})
	}
}

// SYNC-4: a pull leaves an attached root as the user set it, and attaches a new one last.
func TestArrRootAttachmentKeepsWhatIsAttachedAndSortsANewOneLast(t *testing.T) {
	attached := []settings.CategoryRoot{
		{CategoryID: 1, RootFolderID: 10, Order: 0, Weight: 5, AcceptsNew: false},
		{CategoryID: 1, RootFolderID: 11, Order: 3, Weight: 1, AcceptsNew: true},
		{CategoryID: 2, RootFolderID: 12, Order: 9, Weight: 1, AcceptsNew: true},
	}
	if got, found := arrRootAttachment(attached, 1, 10); !found || got.Weight != 5 || got.AcceptsNew {
		t.Errorf("attached root = %+v, %v, want it untouched", got, found)
	}
	got, found := arrRootAttachment(attached, 1, 13)
	if found || got.Order != 4 || got.Weight != 1 || !got.AcceptsNew {
		t.Errorf("new root = %+v, %v, want order 4, weight 1, accepting", got, found)
	}
}
