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
