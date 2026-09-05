package settings

import "slices"

// Snapshot is an immutable view of settings.db (ADR-25).
type Snapshot struct {
	users             []User
	categories        []Category
	rootFolders       []RootFolder
	categoryRoot      []CategoryRoot
	qualityProfiles   []QualityProfile
	languageProfiles  []LanguageProfile
	exclusionPatterns []ExclusionPattern
	// policy is instance-wide settings by key, read through typed accessors.
	policy map[string]string

	// Indexes, built once, so a lookup is a map hit rather than a scan.
	userByID             map[int64]User
	userByUsername       map[string]User
	categoryByID         map[int64]Category
	rootFolderByID       map[int64]RootFolder
	accessByUser         map[int64]map[int64]Permission
	qualityProfileByID   map[int64]QualityProfile
	languageProfileByID  map[int64]LanguageProfile
	exclusionPatternByID map[int64]ExclusionPattern
}

// Users returns every account, ordered by username.
func (s *Snapshot) Users() []User { return s.users }

// Categories returns every category, ordered by priority rank then name — the
// order the sidebar shows them in (§21.1).
func (s *Snapshot) Categories() []Category { return s.categories }

// RootFolders returns every root folder, ordered by path.
func (s *Snapshot) RootFolders() []RootFolder { return s.rootFolders }

// CategoryRoots returns every category-to-root attachment.
func (s *Snapshot) CategoryRoots() []CategoryRoot { return s.categoryRoot }

// User looks up an account by id.
func (s *Snapshot) User(id int64) (User, bool) {
	user, found := s.userByID[id]
	return user, found
}

// UserByUsername looks up an account for login.
func (s *Snapshot) UserByUsername(username string) (User, bool) {
	user, found := s.userByUsername[username]
	return user, found
}

// Category looks up a category by id.
func (s *Snapshot) Category(id int64) (Category, bool) {
	category, found := s.categoryByID[id]
	return category, found
}

// RootFolder looks up a root folder by id.
func (s *Snapshot) RootFolder(id int64) (RootFolder, bool) {
	folder, found := s.rootFolderByID[id]
	return folder, found
}

// QualityProfiles returns every quality profile, ordered by name.
func (s *Snapshot) QualityProfiles() []QualityProfile { return s.qualityProfiles }

// QualityProfile looks up a quality profile by id.
func (s *Snapshot) QualityProfile(id int64) (QualityProfile, bool) {
	profile, found := s.qualityProfileByID[id]
	return profile, found
}

// LanguageProfiles returns every language profile, ordered by name.
func (s *Snapshot) LanguageProfiles() []LanguageProfile { return s.languageProfiles }

// LanguageProfile looks up a language profile by id.
func (s *Snapshot) LanguageProfile(id int64) (LanguageProfile, bool) {
	profile, found := s.languageProfileByID[id]
	return profile, found
}

// ExclusionPatterns returns every exclusion pattern, in id order.
func (s *Snapshot) ExclusionPatterns() []ExclusionPattern { return s.exclusionPatterns }

// ExclusionPattern looks up an exclusion pattern by id.
func (s *Snapshot) ExclusionPattern(id int64) (ExclusionPattern, bool) {
	pattern, found := s.exclusionPatternByID[id]
	return pattern, found
}

// Permission reports what a user may do in a category. An absent grant is None:
// a category with no row is invisible, not public.
func (s *Snapshot) Permission(userID, categoryID int64) Permission {
	granted, found := s.accessByUser[userID][categoryID]
	if !found {
		return PermissionNone
	}
	return granted
}

// VisibleCategoryIDs returns the categories a user may see at the given level, in snapshot
// order (Invariant 36).
func (s *Snapshot) VisibleCategoryIDs(userID int64, required Permission) []int64 {
	visible := make([]int64, 0, len(s.categories))
	for _, category := range s.categories {
		if s.Permission(userID, category.ID).AtLeast(required) {
			visible = append(visible, category.ID)
		}
	}
	return visible
}

// RootFoldersForCategory returns the roots attached to a category, in
// attachment order.
func (s *Snapshot) RootFoldersForCategory(categoryID int64) []RootFolder {
	var folders []RootFolder
	for _, attachment := range s.categoryRoot {
		if attachment.CategoryID != categoryID {
			continue
		}
		if folder, found := s.rootFolderByID[attachment.RootFolderID]; found {
			folders = append(folders, folder)
		}
	}
	return folders
}

// CategoriesForRootFolder returns the category ids a root folder is
// attached to, in attachment order -- the reverse of RootFoldersForCategory,
// for ADR-51 drain's own "eligible destinations share a category with the
// root being drained" scoping.
func (s *Snapshot) CategoriesForRootFolder(rootFolderID int64) []int64 {
	var categoryIDs []int64
	for _, attachment := range s.categoryRoot {
		if attachment.RootFolderID == rootFolderID {
			categoryIDs = append(categoryIDs, attachment.CategoryID)
		}
	}
	return categoryIDs
}

// newSnapshot builds the indexes. It clones the slices it is given, so a caller
// keeping a reference cannot mutate a published snapshot.
func newSnapshot(users []User, categories []Category, rootFolders []RootFolder,
	attachments []CategoryRoot, access map[int64]map[int64]Permission,
	qualityProfiles []QualityProfile, languageProfiles []LanguageProfile,
	exclusionPatterns []ExclusionPattern, policy map[string]string) *Snapshot {
	snapshot := &Snapshot{
		policy:               policy,
		users:                slices.Clone(users),
		categories:           slices.Clone(categories),
		rootFolders:          slices.Clone(rootFolders),
		categoryRoot:         slices.Clone(attachments),
		qualityProfiles:      slices.Clone(qualityProfiles),
		languageProfiles:     slices.Clone(languageProfiles),
		exclusionPatterns:    slices.Clone(exclusionPatterns),
		userByID:             make(map[int64]User, len(users)),
		userByUsername:       make(map[string]User, len(users)),
		categoryByID:         make(map[int64]Category, len(categories)),
		rootFolderByID:       make(map[int64]RootFolder, len(rootFolders)),
		accessByUser:         access,
		qualityProfileByID:   make(map[int64]QualityProfile, len(qualityProfiles)),
		languageProfileByID:  make(map[int64]LanguageProfile, len(languageProfiles)),
		exclusionPatternByID: make(map[int64]ExclusionPattern, len(exclusionPatterns)),
	}
	for _, user := range snapshot.users {
		snapshot.userByID[user.ID] = user
		snapshot.userByUsername[user.Username] = user
	}
	for _, category := range snapshot.categories {
		snapshot.categoryByID[category.ID] = category
	}
	for _, folder := range snapshot.rootFolders {
		snapshot.rootFolderByID[folder.ID] = folder
	}
	for _, profile := range snapshot.qualityProfiles {
		snapshot.qualityProfileByID[profile.ID] = profile
	}
	for _, profile := range snapshot.languageProfiles {
		snapshot.languageProfileByID[profile.ID] = profile
	}
	for _, pattern := range snapshot.exclusionPatterns {
		snapshot.exclusionPatternByID[pattern.ID] = pattern
	}
	return snapshot
}
