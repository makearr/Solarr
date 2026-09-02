// Package authz decides what a request may see (ADR-27, Invariant 36).
package authz

import (
	"errors"
	"fmt"

	"github.com/makearr/MediaNexus/internal/settings"
)

var (
	// ErrNotVisible reports a category the caller cannot see at all (Invariant 37).
	ErrNotVisible = errors.New("authz: not found")

	// ErrInsufficientPermission reports a category the caller can see but may not act on.
	ErrInsufficientPermission = errors.New("authz: insufficient permission")

	// ErrNoAccess reports a context carrying no Access at all.
	ErrNoAccess = errors.New("authz: no access context")
)

// Access is who is asking, resolved against one settings snapshot. It is a
// value: copying it is free and it cannot be mutated after a request begins.
type Access struct {
	userID   int64
	role     settings.Role
	snapshot *settings.Snapshot

	// system marks internal work — the planner, the scanner, a scheduled job — which is not a user
	// request and has no user to filter by.
	system bool
}

// ForUser builds the access a request runs under.
func ForUser(snapshot *settings.Snapshot, user settings.User) Access {
	return Access{userID: user.ID, role: user.Role, snapshot: snapshot}
}

// System builds the access background work runs under. Jobs act on the whole
// library by definition; they are not a user and are not filtered.
func System(snapshot *settings.Snapshot) Access {
	return Access{snapshot: snapshot, system: true}
}

// UserID reports whose request this is. It is zero for system access.
func (a Access) UserID() int64 { return a.userID }

// Role reports the caller's role. It is empty for system access.
func (a Access) Role() settings.Role { return a.role }

// IsSystem reports internal work rather than a user request.
func (a Access) IsSystem() bool { return a.system }

// Snapshot is the settings view this access was resolved against. Holding one
// snapshot for the whole request is what keeps its answers consistent (ADR-25).
func (a Access) Snapshot() *settings.Snapshot { return a.snapshot }

// Permission reports what the caller may do in one category.
func (a Access) Permission(categoryID int64) settings.Permission {
	if a.system {
		return settings.PermissionManage
	}
	if a.snapshot == nil {
		return settings.PermissionNone
	}
	return a.snapshot.Permission(a.userID, categoryID)
}

// CanSee reports whether the category exists as far as the caller is concerned.
func (a Access) CanSee(categoryID int64) bool {
	return a.Permission(categoryID).AtLeast(settings.PermissionRead)
}

// Require checks a category against a needed permission, distinguishing "you cannot see this"
// from "you can see this but may not do that".
func (a Access) Require(categoryID int64, required settings.Permission) error {
	held := a.Permission(categoryID)
	if !held.AtLeast(settings.PermissionRead) {
		return fmt.Errorf("%w: category %d", ErrNotVisible, categoryID)
	}
	if !held.AtLeast(required) {
		return fmt.Errorf("%w: category %d requires %s, held %s",
			ErrInsufficientPermission, categoryID, required, held)
	}
	return nil
}

// VisibleCategoryIDs returns the categories the caller may act on at the given
// level. This is what the repository layer narrows every query with.
func (a Access) VisibleCategoryIDs(required settings.Permission) []int64 {
	if a.snapshot == nil {
		return nil
	}
	if a.system {
		all := make([]int64, 0, len(a.snapshot.Categories()))
		for _, category := range a.snapshot.Categories() {
			all = append(all, category.ID)
		}
		return all
	}
	return a.snapshot.VisibleCategoryIDs(a.userID, required)
}

// Categories returns the categories the caller may act on, in snapshot order (Invariant 36).
func (a Access) Categories(required settings.Permission) []settings.Category {
	if a.snapshot == nil {
		return nil
	}
	all := a.snapshot.Categories()
	visible := make([]settings.Category, 0, len(all))
	for _, category := range all {
		if a.Permission(category.ID).AtLeast(required) {
			visible = append(visible, category)
		}
	}
	return visible
}

// Category fetches one category, or reports why not (Invariant 37).
func (a Access) Category(id int64, required settings.Permission) (settings.Category, error) {
	if err := a.Require(id, required); err != nil {
		return settings.Category{}, err
	}
	category, found := a.snapshot.Category(id)
	if !found {
		return settings.Category{}, fmt.Errorf("%w: category %d", ErrNotVisible, id)
	}
	return category, nil
}
