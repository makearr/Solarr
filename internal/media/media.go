// Package media is the registry of media types: what core asks instead of naming one (ADR-23).
package media

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/cutoff"
	"github.com/makearr/Solarr/internal/acquiring/dispatch"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/acquiring/plan"
	"github.com/makearr/Solarr/internal/acquiring/rss"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/moving/mover"
	"github.com/makearr/Solarr/internal/moving/naming"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

// Key names a media type and equals category.media_type.
type Key string

// Term is one word of a media type's vocabulary, singular and plural.
type Term struct {
	One  string
	Many string
}

// Vocabulary is what a media type calls the base entities; a zero Term means the type has none.
type Vocabulary struct {
	Work         Term
	Part         Term
	Group        Term
	LeadPeople   Term
	Organisation Term
}

// Role is one way a person is credited on a work of this media type.
type Role struct {
	Key   string
	Label string
	// Lead marks the role a work is listed under, for a type that has one.
	Lead       bool
	Followable bool
	// PartScoped roles are credited per part, not per work.
	PartScoped bool
	// Character roles carry the name of who was played.
	Character bool
}

// Choice is one value a follow option may take.
type Choice struct {
	Key   string
	Label string
}

// FollowOption is one setting of a follow the media type declares, stored under Key in the follow's filters.
type FollowOption struct {
	Key   string
	Label string
	// Many options take a list of choices, the others exactly one.
	Many    bool
	Choices []Choice
	Default []string
}

// Descriptor is everything about a media type that is data: sent to the web interface as it is.
type Descriptor struct {
	Key        Key
	Label      string
	Vocabulary Vocabulary
	Levels     []model.Level
	Roles      []Role
	// FollowOptions are the settings a follow of a person or organisation offers in this type's libraries.
	FollowOptions []FollowOption
	// Facets are the type's own fields of its entities, beyond the base ones.
	Facets []Facet
	// MonitorOptions are the ways a whole work can be monitored at once (Sonarr's "future episodes"), when a type has them.
	MonitorOptions []Choice
	// SearchTerms are the words a person may search with instead of a work's own, when a type takes them.
	SearchTerms []Choice
	// WorkImage is the shape a work's picture has: an album's square cover, a series' tall poster.
	WorkImage ImageShape
	// AddKinds are what a person looks up and adds to a library of the type: albums and artists, series.
	AddKinds []Choice
	// WantedGrain is what Wanted lists: works (Lidarr's albums) or parts (Sonarr's episodes).
	WantedGrain WantedGrain
	// DuplicatesByName says a duplicate is one recording under any work, matched by artist, title and length (music);
	// otherwise two files are duplicates only when they cover the same part.
	DuplicatesByName bool
	// CommitsHeldImports says a held import unit of the type is committed by hand, anyway or as a work named.
	CommitsHeldImports bool
	// ScanHonoursExclusions says the type's scan leaves an excluded work's files out, so a work removed and excluded
	// can go although its files stay.
	ScanHonoursExclusions bool
	// ExtraFiles is what import brings along with the files it places, until a library says otherwise.
	ExtraFiles ExtraFilesDefault
	// GuessFloor is the least confidence "Commit anyway" takes a held unit's best guess at; 0 takes any.
	GuessFloor float64
}

// ExtraFilesDefault is a media type's "Import Extra Files" for a library that has not chosen.
type ExtraFilesDefault struct {
	Import     bool
	Extensions []string
}

// ImageShape is the shape of an entity's picture.
type ImageShape string

// WantedGrain is the level a type's Wanted lists.
type WantedGrain string

// The grains Wanted lists at; the zero value is works.
const (
	WantedWorks WantedGrain = ""
	WantedParts WantedGrain = "part"
)

// The shapes of a work's picture.
const (
	ImageCover  ImageShape = "cover"
	ImagePoster ImageShape = "poster"
)

// Adding is the capability of a media type whose works a person looks up at its sources and adds to a library.
type Adding interface {
	LookupWorks(ctx context.Context, lookup AddLookup) ([]AddCandidate, error)
	AddWork(ctx context.Context, request AddRequest) (AddResult, error)
}

// AddLookup is one search of a type's sources for something to add, of one of its AddKinds.
type AddLookup struct {
	CategoryID  int64
	Kind, Query string
	Limit       int
}

// AddCandidate is one thing a lookup found.
type AddCandidate struct {
	Kind, Provider, ExternalID, Title string
	// Subtitle tells candidates apart: an album's artist, an artist's disambiguation, a series' network.
	Subtitle, Overview, PosterURL, Status string
	Year                                  int64
	// LibraryID is the work or person already held for it, 0 when it is new.
	LibraryID int64
}

// AddRequest is one add a person asked for.
type AddRequest struct {
	CategoryID, UserID         int64
	Kind, Provider, ExternalID string
	Monitored                  bool
	// Option is one of the type's monitor options, empty for its default; Specials adds its specials group.
	Option    string
	Specials  bool
	ProfileID *int64
	Search    bool
}

// AddResult is what an add stored.
type AddResult struct {
	Kind             string
	WorkID, PersonID int64
	// Searching says the type started looking for what was added; false leaves a search of the work to the caller.
	Searching bool
}

// Refreshing is the capability of a media type whose works are read again from their sources on request.
type Refreshing interface {
	// RefreshWork reads one work again; duplicate when a refresh of it was already on its way.
	RefreshWork(ctx context.Context, workID int64) (duplicate bool, err error)
}

// Merging is the capability of a media type that places files again on a work that took another's over.
type Merging interface {
	// AfterMerge places the files the kept work took over on its own parts.
	AfterMerge(ctx context.Context, keepID int64) error
}

// ImageOption is one picture a provider offers for a work or a person.
type ImageOption struct {
	URL, ThumbURL, Source, Label string
}

// ErrImageNotOffered is a pick of an address no provider listed.
var ErrImageNotOffered = errors.New("media: that image is not one the providers offer")

// Picturing is the capability of a media type whose work picture is chosen from what its providers hold.
type Picturing interface {
	WorkPictures(ctx context.Context, workID int64) ([]ImageOption, error)
	// PickWorkPicture keeps imageURL, one of WorkPictures, over later refreshes; ErrImageNotOffered otherwise.
	PickWorkPicture(ctx context.Context, workID int64, imageURL string) error
}

// Placement is a person's answer to "what is this file": a work by a provider's id and, for a type with groups, the
// group and parts it holds when its name does not say (no parts: read them from the name).
type Placement struct {
	Provider, ExternalID string
	Group                int
	Parts                []int
}

// PlacingFiles is the capability of a media type whose library files nothing identified are placed by a person.
type PlacingFiles interface {
	// PlaceFile places one waiting file of the library, and with wholeFolder those waiting beside it; it answers the
	// work and how many files were placed.
	PlaceFile(ctx context.Context, categoryID, mediaObjectID int64, placement Placement, wholeFolder bool) (workID, placed int64, err error)
}

// Pass is one of the periodic passes that keep metadata current; each is one schedule for every media type.
type Pass string

// The passes: works read again from their sources, people read again, pictures fetched into the cache.
const (
	PassWorks    Pass = "works"
	PassPeople   Pass = "people"
	PassPictures Pass = "pictures"
)

// Enriching is the capability of a media type that keeps its metadata current by periodic passes.
type Enriching interface {
	// StartPass queues the type's pass; started is false for a pass the type does not have. manual is a person's "Run now".
	StartPass(ctx context.Context, pass Pass, manual bool) (started, duplicate bool, err error)
}

// PassStart queues one pass and says whether one was already on its way.
type PassStart func(ctx context.Context, manual bool) (duplicate bool, err error)

// Passes is a module's periodic passes, embedded to implement Enriching; a nil one is a pass the type does not have.
type Passes struct {
	WorksPass, PeoplePass, PicturesPass PassStart
}

// StartPass implements Enriching.
func (p Passes) StartPass(ctx context.Context, pass Pass, manual bool) (started, duplicate bool, err error) {
	start := map[Pass]PassStart{PassWorks: p.WorksPass, PassPeople: p.PeoplePass, PassPictures: p.PicturesPass}[pass]
	if start == nil {
		return false, false, nil
	}
	duplicate, err = start(ctx, manual)
	return true, duplicate, err
}

// ErrNotWired reports a capability whose module was built without what it needs.
var ErrNotWired = errors.New("media: not wired in this build")

// Numbering is the capability of a media type that places a part for a reader: a heading within its group, and
// the number it is shown with ("Disc 2", "3"; "", "7").
type Numbering interface {
	PartPosition(ref string) (section, number string)
}

// Module is one media type. Its behaviour is in the capability interfaces it also implements.
type Module interface {
	Descriptor() Descriptor
}

// NamingExample is one rendered sample path of a naming profile.
type NamingExample struct {
	Label string
	Path  string
}

// Naming is the capability of a media type that renders library paths from templates.
type Naming interface {
	NamingScheme() naming.Scheme
	DefaultNamingProfile() settings.NamingProfile
	ValidateNaming(profile settings.NamingProfile) error
	NamingExamples(profile settings.NamingProfile) []NamingExample
}

// Scanning is the capability of a media type whose libraries are swept on disk.
type Scanning interface {
	ScanSweep() scan.Sweep
}

// Hunting is the capability of a media type whose wanted items are searched for without being asked.
type Hunting interface {
	// HuntSearch searches for one wanted item the hunt chose and sends the best release.
	HuntSearch(ctx context.Context, itemID int64) (plan.Outcome, error)
}

// Labelling is the capability of a media type that names what a release holds within a wanted item.
type Labelling interface {
	// ReleaseLabel is the part of an item a release covers, for a queue or history row: "S03E10", a season, a date.
	ReleaseLabel(releaseTitle, itemRef string, now time.Time) string
}

// LeadRole answers the role a work of this type is listed under, and whether the type has one.
func (d Descriptor) LeadRole() (string, bool) {
	for _, role := range d.Roles {
		if role.Lead {
			return role.Key, true
		}
	}
	return "", false
}

// Renaming is the capability of a media type that can rename a library's files to its naming formats.
type Renaming interface {
	// StartRename plans, and unless preview starts, the rename of one root's files within scope. It answers the plan,
	// the job started (0 for a preview) and warnings per media object.
	StartRename(ctx context.Context, rootFolderID int64, scope RenameScope, preview bool) (mover.MovePlan, int64, map[int64][]string, error)
}

// RenameScope narrows a rename: to one library's works, and to one work; zero means no narrowing.
type RenameScope struct {
	CategoryID int64
	WorkID     int64
}

// Feeding is the capability of a media type whose wanted items are also filled from the indexers' feeds.
type Feeding interface {
	rss.Feed
}

// Upgrading is the capability of a media type whose owned files are checked against their library's cutoff.
type Upgrading interface {
	cutoff.Check
}

// Follow is one follow of a person or organisation in a library of this media type, as a module reads it.
type Follow struct {
	SubjectType string
	SubjectID   int64
	CategoryID  int64
	// Roles limits a person's follow to these roles; empty is any.
	Roles         []string
	FiltersJSON   string
	BackCatalogue bool
	// CreatedAt is when the follow was made, Unix seconds: what counts as new for it.
	CreatedAt int64
}

// FollowCandidate is one work a follow would add, as the providers know it.
type FollowCandidate struct {
	// Provider and ExternalID name the work at the provider, which is also what an exclusion is kept by.
	Provider   string
	ExternalID string
	Title      string
	Year       int64
	PosterURL  string
	// Role is what the person did on it; empty for an organisation.
	Role string
	// Held is set when the library already has the work, monitored or not.
	Held bool
}

// Following is the capability of a media type whose works can be added by following a person or organisation.
type Following interface {
	// FollowCandidates lists the subject's works the follow admits, newest first, with what the library holds marked.
	FollowCandidates(ctx context.Context, follow Follow) ([]FollowCandidate, error)
	// AddFollowed adds one candidate to the follow's library. Additive only: a held work is left as it is (Invariant 71).
	AddFollowed(ctx context.Context, follow Follow, candidate FollowCandidate) error
	// FollowLimit bounds what one run adds for one follow; 0 is no bound.
	FollowLimit(follow Follow) int
}

// Monitoring is the capability of a media type with monitor options: one applied to a work of a library.
type Monitoring interface {
	// ApplyMonitorOption sets which of the work's parts are monitored; withSpecials adds the specials group.
	ApplyMonitorOption(ctx context.Context, categoryID, workID int64, option string, withSpecials bool) error
}

// ErrUnknownMonitorOption reports a monitor option the work's media type does not have.
var ErrUnknownMonitorOption = errors.New("media: unknown monitor option")

// Searching is the capability of a media type whose wanted items a person searches for and grabs by hand.
type Searching interface {
	// SearchWanted searches one wanted item and records what it decided; nothing is sent.
	SearchWanted(ctx context.Context, search WantedSearch) (judge.Result, error)
	// GrabWanted searches one wanted item and sends its best accepted release, unless a download is on its way.
	GrabWanted(ctx context.Context, search WantedSearch) (judge.Result, dispatch.Best, error)
}

// WantedSearch is one search a person asked for.
type WantedSearch struct {
	ItemID int64
	// Part narrows the search to one part of the item, by its ref; empty searches what the item is missing.
	Part string
	// Terms replace the work's own words, keyed by the type's SearchTerms.
	Terms map[string]string
}

// ErrNoIndexers reports a search with no enabled indexer to ask.
var ErrNoIndexers = errors.New("media: no indexer is enabled")

// ErrInvalidTerms reports search terms the type cannot search with.
var ErrInvalidTerms = errors.New("media: invalid search terms")

// ErrWholeItemOnly reports a part search of a type that searches whole items, as Lidarr searches whole albums.
var ErrWholeItemOnly = errors.New("media: only a whole item is searched")

// Grouping is the capability of a media type whose works hold their parts in groups (a series' seasons).
type Grouping interface {
	// GroupLabel names a group by its ref, as a page heads it: "Season 3", "Specials".
	GroupLabel(ref string) string
}

// ErrNoFollowSource reports a follow whose provider is not set up yet: no key stored, or no id to follow by.
var ErrNoFollowSource = errors.New("media: the provider a follow needs is not available")

// FollowSearching is the capability of a following media type that can search for what a follow just added.
type FollowSearching interface {
	SearchFollowed(ctx context.Context, follow Follow) error
}

// KnownFor is one work a provider credits a person with, and the library's own work for it when it holds one.
type KnownFor struct {
	Provider   string
	ExternalID string
	// Kind is the provider's word for the work: "tv", "movie", "album".
	Kind      string
	Title     string
	Year      int64
	PosterURL string
	// Role is the character played or the job done.
	Role string
	// WorkID is the library's work, 0 when the library does not hold it.
	WorkID int64
}

// Profiling is the capability of a media type whose providers know more of a person than its libraries hold.
type Profiling interface {
	// KnownFor lists the person's works at the providers, newest first, held ones marked for the library; it may keep
	// what the providers add to the person (dates, biography, the Wikidata bridge).
	KnownFor(ctx context.Context, personID, categoryID int64) ([]KnownFor, error)
}

// JobRegistrar takes one job type with its handler.
type JobRegistrar func(definition jobs.Definition, handler jobs.Handler) error

// Jobs is the capability of a media type that brings job types of its own.
type Jobs interface {
	RegisterJobs(register JobRegistrar) error
}

// Registry holds the media types compiled into the binary, in registration order.
type Registry struct {
	modules map[Key]Module
	order   []Key
}

// NewRegistry registers every module; a missing or repeated key is a wiring fault.
func NewRegistry(modules ...Module) (*Registry, error) {
	registry := &Registry{modules: make(map[Key]Module, len(modules))}
	for _, module := range modules {
		key := module.Descriptor().Key
		if key == "" {
			return nil, fmt.Errorf("media: a module declares no key")
		}
		if _, exists := registry.modules[key]; exists {
			return nil, fmt.Errorf("media: %s is registered twice", key)
		}
		for _, facet := range module.Descriptor().Facets {
			if err := checkFacet(facet); err != nil {
				return nil, fmt.Errorf("media: %s: %w", key, err)
			}
		}
		registry.modules[key] = module
		registry.order = append(registry.order, key)
	}
	return registry, nil
}

// Module answers the media type registered under key.
func (r *Registry) Module(key Key) (Module, bool) {
	module, ok := r.modules[key]
	return module, ok
}

// Known reports whether a media type has a module, so a library of it can do something.
func (r *Registry) Known(key Key) bool {
	_, ok := r.modules[key]
	return ok
}

// Descriptors lists every registered media type in registration order.
func (r *Registry) Descriptors() []Descriptor {
	out := make([]Descriptor, 0, len(r.order))
	for _, key := range r.order {
		out = append(out, r.modules[key].Descriptor())
	}
	return out
}

// Capability answers the registered module as T when it implements that capability.
func Capability[T any](r *Registry, key Key) (T, bool) {
	var none T
	module, ok := r.modules[key]
	if !ok {
		return none, false
	}
	capability, ok := module.(T)
	return capability, ok
}
