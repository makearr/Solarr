package apiserver

import "github.com/makearr/Solarr/internal/db/settings"

// libraryAction is something an account does inside a library besides reading it.
type libraryAction int

const (
	actionSearchWork libraryAction = iota
	actionSendCandidate
	actionGrabWork
	actionSearchPersonMissing
	actionFollow
	actionSetMonitored
	actionSearchLibrary
	actionEditWork
	actionRefreshWork
)

// actionLevels is the permission each action needs in one place, so a new handler cannot pick a level ad hoc. A request-only account asks for
// additions (fileRequest) and refreshes metadata; spending indexer calls, downloading, following and monitoring are Add; editing is Manage.
var actionLevels = map[libraryAction]settings.Permission{
	actionSearchWork:          settings.PermissionAdd,
	actionSendCandidate:       settings.PermissionAdd,
	actionGrabWork:            settings.PermissionAdd,
	actionSearchPersonMissing: settings.PermissionAdd,
	actionFollow:              settings.PermissionAdd,
	actionSetMonitored:        settings.PermissionAdd,
	actionSearchLibrary:       settings.PermissionManage,
	actionEditWork:            settings.PermissionManage,
	actionRefreshWork:         settings.PermissionRequest,
}
