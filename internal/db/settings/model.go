// Package settings holds configuration policy: the contents of settings.db, loaded into memory
// at startup and kept there (ADR-25, Invariant 41).
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
	// Tier is the level the account is shown, never above Limits.TierMax.
	Tier   Tier
	Limits UserLimits
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
	// QualityProfileID and LanguageProfileID are nil until a profile is chosen;
	// both are section 19.1/19.8 category-level defaults, not requirements.
	QualityProfileID  *int64
	LanguageProfileID *int64
	// MonitorDiscovered governs what a scan does with an album it finds on
	// disk that nobody asked for: "monitored" (default), "unmonitored", or
	// "none" at all (section 19.6, todo 4.4).
	MonitorDiscovered string
	// TVMonitor is what a scan monitors of a series it finds: "existing" (the episodes on disk), "all", or "none".
	TVMonitor string
	// MonitorReleaseTypes is which release types a monitored artist in this
	// library brings in, overridable per artist by the subscription's own
	// filters (2026-09-19).
	MonitorReleaseTypes []string
	// Naming is nil until the library's naming is first saved; nil means the media type's default.
	Naming *NamingProfile
	// QualityProfileIDs are the profiles the library accepts, earlier preferred; QualityProfileID
	// is always the first. Empty means the instance's default profile.
	QualityProfileIDs []int64
	// QualityCutoffID is the profile of the list at or above which upgrades stop.
	QualityCutoffID *int64
	// QualityTarget is the one exact quality the library aims for; nil when none is set.
	QualityTarget *QualityTarget
	// QualityUpgrades is Lidarr's "Upgrades Allowed": off, an album with every track is complete.
	QualityUpgrades bool
	// PreferredProtocol and ProtocolDelayMinutes are the delay profile (D16): the other protocol's releases wait this long
	// after they were posted. No preference ("") or zero minutes is no delay.
	PreferredProtocol    string
	ProtocolDelayMinutes int64
	// FileManagement is Sonarr's File Management and Permissions; the defaults when never saved.
	FileManagement FileManagement
}

// QualityTarget is a library's aimed-for quality. BitrateKbps is nil for a lossless codec;
// otherwise a file within +-TolerancePct of it meets the target.
type QualityTarget struct {
	AudioCodec   string
	Container    string
	BitrateKbps  *int64
	TolerancePct int64
	// Resolution, VideoCodec and HDR are a video library's target; empty means it does not care.
	Resolution string
	VideoCodec string
	HDR        string
}

// NamingProfile is one library's naming, Lidarr's shape: a template per slot the media type
// declares, and the character options applied to every rendered component.
type NamingProfile struct {
	RenameFiles              bool
	ReplaceIllegalCharacters bool
	ColonReplacement         string
	// Formats maps a slot key ("standard_track") to its template.
	Formats map[string]string
}

// DefaultMonitorReleaseTypes is what a library monitors unless told
// otherwise: the three types anyone means by "their albums".
var DefaultMonitorReleaseTypes = []string{"Album", "EP", "Single"}

// Monitor-discovered policy values (section 19.6).
const (
	MonitorDiscoveredMonitored   = "monitored"
	MonitorDiscoveredUnmonitored = "unmonitored"
	MonitorDiscoveredNone        = "none"

	// The TV scan's choices (a TV library's own, beside MonitorDiscovered's album ones).
	TVMonitorExisting = "existing"
	TVMonitorAll      = "all"
	TVMonitorNone     = "none"
)

// Exclusion pattern actions (exclusion_pattern.action, ADR-53 part 1).
const (
	// ExclusionIgnore means "not media" -- skipped silently, never grouped,
	// never counted.
	ExclusionIgnore = "ignore"
	// ExclusionRefuse poisons the whole unit it is found in: nothing in that
	// unit imports. An executable beside media is the classic shape of a
	// fake, not clutter to skip past.
	ExclusionRefuse = "refuse"
)

// ExclusionPattern is ADR-53 part 1: "a file shaped like this is not media",
// by name/extension glob, never by content -- the hash-based half
// (blocked_content) lives in library.db instead, since it is a fact about
// content rather than policy. MediaType nil means every media type;
// CategoryID nil means every category of that media type -- .iso is poison
// in a music release and legitimate in a ROM one, which is why both exist.
type ExclusionPattern struct {
	ID         int64
	Pattern    string
	Action     string
	MediaType  string
	CategoryID *int64
	Note       string
	// Builtin marks a shipped default -- still editable and removable, never
	// a reason to hide it from the same list a user-added pattern appears in.
	Builtin bool
	Enabled bool
}

// QualityProfile is ADR-40's policy, evaluated by internal/identify/score: hard gates, then
// lexicographic tiers, then an additive score. RulesJSON and UpgradeJSON are kept
// as raw JSON here -- internal/identify/score owns the parsed shape, since it is the only
// package that interprets them.
type QualityProfile struct {
	ID          int64
	Name        string
	RulesJSON   string
	UpgradeJSON string
	// MediaType is what the profile judges; a library is offered only profiles of its own type.
	MediaType string
}

// LanguageProfile is section 19.8's policy: language is its own axis, belonging to
// a category rather than a media type. Required is any_of semantics -- at least one
// listed language must be present, evaluated from the probe, never the release name.
type LanguageProfile struct {
	ID                  int64
	Name                string
	AudioPreferred      []string
	AudioRequired       []string
	SubtitlePreferred   []string
	SubtitleRequired    []string
	PreferOriginalAudio bool
	RejectDubbed        bool
}

// DefaultMinFreeBytes is the reserve a newly created root folder starts
// with: 8 GB, asked for 2026-09-19.
const DefaultMinFreeBytes int64 = 8_000_000_000

// RootFolder is a directory the library is stored in. Section 11.7's three
// separate concepts, never conflated: Writable is a probed fact, ReadOnly is
// the user's own policy, AcceptsNew (CategoryRoot, per category) is
// draining -- none of the three is derived from either of the others.
type RootFolder struct {
	ID    int64
	Path  string
	Label string
	// Online is false for an unmounted or unreachable root, which counts as
	// unavailable rather than as free space (Invariant 20a).
	Online bool
	// Writable is probed, never set by hand -- true until the capacity job
	// finds otherwise, so a freshly created root is not treated as broken
	// before it has ever been checked.
	Writable       bool
	WritableReason string
	// ReadOnly means "this is curated, look but do not touch": blocks new
	// placement, upgrade-in-place and tag write-back, and blocks renaming
	// unless AllowRename is also set (Invariant 69). It blocks nothing on
	// the read side.
	ReadOnly    bool
	AllowRename bool
	// TotalBytes and FreeBytes are nil until the capacity job has run once.
	TotalBytes *int64
	FreeBytes  *int64
	// MinFreeBytes is the user's own reserve: placement.Resolve refuses a
	// root once FreeBytes minus an item's size would fall below this.
	MinFreeBytes int64
	// CapacityCheckedAt is nil until the capacity job has run once; a nil
	// or stale reading is the caller's signal to refresh before relying on
	// TotalBytes/FreeBytes/Writable for a placement decision.
	CapacityCheckedAt *int64
	// FSID identifies the filesystem this root lives on, for ADR-11's
	// reflink/hardlink tiebreak -- empty until the capacity job runs.
	FSID string
	// CapabilitiesJSON is deliberately not typed further here (section
	// 11.7): which filesystem capabilities matter is expected to grow.
	CapabilitiesJSON string
	// DrainState is ADR-51's own state machine (NONE/DRAINING/PAUSED/
	// DRAINED); DrainJobID is library.db's move_job id behind it, nil when
	// DrainState is NONE.
	DrainState string
	DrainJobID *int64
}

// Root folder drain states (root_folder.drain_state).
const (
	DrainStateNone     = "NONE"
	DrainStateDraining = "DRAINING"
	DrainStatePaused   = "PAUSED"
	DrainStateDrained  = "DRAINED"
)

// CategoryRoot attaches a root folder to a category (§11.7).
type CategoryRoot struct {
	CategoryID   int64
	RootFolderID int64
	Order        int64
	Weight       int64
	// AcceptsNew drains a root without unmounting it.
	AcceptsNew bool
}

// APIKey is a non-interactive credential.
type APIKey struct {
	ID         int64
	UserID     int64
	Name       string
	CreatedAt  time.Time
	LastUsedAt time.Time
}
