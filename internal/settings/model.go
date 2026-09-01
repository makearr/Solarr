// Package settings holds configuration policy: the contents of settings.db,
// loaded into memory at startup and kept there (ADR-25).
//
// Resolving a category or a root folder becomes a map lookup rather than a
// join, on every one of 500k library rows (ADR-28). library.db references these
// by integer id with no foreign key, so integrity is the application's job:
// deleting something that is in use is refused, never cascaded (Invariant 41).
package settings

import "time"

// Role is what a user may do outside any single category.
type Role string

// Roles as of ADR-14. OIDC, LDAP and WebAuthn are deferred until someone asks.
const (
	RoleAdmin    Role = "admin"
	RoleOperator Role = "operator"
	RoleReadonly Role = "readonly"
)

// Tier is how much of the settings surface a user is shown (ADR-26). Raising it
// reveals settings; it never changes behaviour.
type Tier string

// Tiers in increasing order of detail.
const (
	TierNormal   Tier = "normal"
	TierAdvanced Tier = "advanced"
	TierExpert   Tier = "expert"
)

// Permission is per-category access (ADR-27). An absent grant is Denied, so a
// new category is private until shared rather than public until restricted.
type Permission string

// Permissions in increasing order of capability.
const (
	// PermissionNone means the category does not exist as far as this user is
	// concerned. A hidden category returns 404, never 403 (Invariant 37).
	PermissionNone Permission = "none"
	// PermissionRead allows browsing and searching.
	PermissionRead Permission = "read"
	// PermissionRequest adds requesting additions for approval.
	PermissionRequest Permission = "request"
	// PermissionManage adds monitoring, editing, upgrading and deleting.
	PermissionManage Permission = "manage"
)

// rank orders permissions so "at least read" is expressible.
func (p Permission) rank() int {
	switch p {
	case PermissionRead:
		return 1
	case PermissionRequest:
		return 2
	case PermissionManage:
		return 3
	default:
		return 0
	}
}

// AtLeast reports whether this permission includes the one asked for.
func (p Permission) AtLeast(required Permission) bool { return p.rank() >= required.rank() }

// User is a local account.
type User struct {
	ID       int64
	Username string
	// HasPassword is false for an account that authenticates only by proxy
	// header or API key. The hash itself is never held in the snapshot.
	HasPassword bool
	Role        Role
	CreatedAt   time.Time
	Tier        Tier
}

// Category is the policy boundary for storage, naming, indexers, profiles and
// visibility (§19.1).
type Category struct {
	ID        int64
	MediaType string
	Name      string
	// PriorityRank feeds the composite acquisition priority (ADR-36). Lower is
	// more important.
	PriorityRank int64
}

// RootFolder is a directory the library is stored in.
type RootFolder struct {
	ID    int64
	Path  string
	Label string
	// Online is false for an unmounted or unreachable root, which counts as
	// unavailable rather than as free space (Invariant 20a).
	Online bool
}

// CategoryRoot attaches a root folder to a category (§11.7).
type CategoryRoot struct {
	CategoryID   int64
	RootFolderID int64
	Order        int64
	Weight       int64
	// AcceptsNew drains a root without unmounting it.
	AcceptsNew bool
}
