package apiserver

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
)

func TestActionLevels(t *testing.T) {
	want := map[libraryAction]settings.Permission{
		actionSearchWork: settings.PermissionAdd, actionSendCandidate: settings.PermissionAdd, actionGrabWork: settings.PermissionAdd,
		actionSearchPersonMissing: settings.PermissionAdd, actionFollow: settings.PermissionAdd, actionSetMonitored: settings.PermissionAdd,
		actionSearchLibrary: settings.PermissionManage, actionEditWork: settings.PermissionManage, actionRefreshWork: settings.PermissionRequest,
	}
	if !reflect.DeepEqual(actionLevels, want) {
		t.Errorf("actionLevels = %v, want %v", actionLevels, want)
	}
}

func TestParseStart(t *testing.T) {
	for raw, ok := range map[string]bool{"": true, "0": true, "12.5": true, "172800": true, "NaN": false, "nan": false, "Inf": false, "-1": false, "172801": false, "x": false} {
		if _, err := parseStart(raw); (err == nil) != ok {
			t.Errorf("parseStart(%q) error = %v, want ok=%v", raw, err, ok)
		}
	}
}
