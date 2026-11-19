package apiserver

import (
	"reflect"
	"strings"
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

func TestValidPassword(t *testing.T) {
	for password, want := range map[string]string{
		"":                        "a password is required",
		"short":                   "a password is at least 8 characters",
		"1234567":                 "a password is at least 8 characters",
		"åäöüåäö":                 "a password is at least 8 characters",
		"12345678":                "",
		"åäöüåäöü":                "",
		strings.Repeat("a", 1024): "",
		strings.Repeat("a", 1025): "a password is at most 1024 bytes",
	} {
		if got := validPassword(password); got != want {
			t.Errorf("validPassword(%q) = %q, want %q", password, got, want)
		}
	}
}
