package adapters

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
)

func TestApplyBulkChange_LaysOnlyWhatIsPresentOverTheAlbum(t *testing.T) {
	profile := int64(4)
	yes := true
	cases := []struct {
		name   string
		edit   apiserver.WorkEditRow
		change apiserver.BulkEditChange
		want   apiserver.WorkEditRow
	}{
		{
			name:   "nothing present changes nothing",
			edit:   apiserver.WorkEditRow{Monitored: true, ProfileID: &profile, Tags: []string{"Rock"}},
			change: apiserver.BulkEditChange{},
			want:   apiserver.WorkEditRow{Monitored: true, ProfileID: &profile, Tags: []string{"Rock"}},
		},
		{
			name:   "monitor and clear the profile",
			edit:   apiserver.WorkEditRow{ProfileID: &profile, Tags: []string{}},
			change: apiserver.BulkEditChange{Monitored: &yes, SetProfile: true},
			want:   apiserver.WorkEditRow{Monitored: true, Tags: []string{}},
		},
		{
			name:   "tags add without duplicating and remove case-insensitively",
			edit:   apiserver.WorkEditRow{Tags: []string{"Rock", "Sleep"}},
			change: apiserver.BulkEditChange{AddTags: []string{"rock", "Road trip"}, RemoveTags: []string{"SLEEP"}},
			want:   apiserver.WorkEditRow{Tags: []string{"Rock", "Road trip"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := applyBulkChange(tc.edit, tc.change); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// Saving the tags a work already has, in whatever order, writes nothing.
func TestSameTagNames_IgnoresOrderButNotContent(t *testing.T) {
	current := []library.TagName{{ID: 1, Name: "Rock"}, {ID: 2, Name: "Live"}}
	if !sameTagNames(current, []string{"Live", "Rock"}) {
		t.Error("the same tags in another order read as a change")
	}
	if sameTagNames(current, []string{"Live"}) || sameTagNames(current, []string{"Live", "Jazz"}) {
		t.Error("different tags read as the same")
	}
	if !sameTagNames(nil, []string{}) {
		t.Error("no tags before and after read as a change")
	}
}
