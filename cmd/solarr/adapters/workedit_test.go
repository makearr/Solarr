package adapters

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
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
