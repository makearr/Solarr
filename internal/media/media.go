// Package media is the registry of media types: what core asks instead of naming one (ADR-23).
package media

import (
	"context"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/plan"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/jobs"
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

// Descriptor is everything about a media type that is data: sent to the web interface as it is.
type Descriptor struct {
	Key        Key
	Label      string
	Vocabulary Vocabulary
	Levels     []model.Level
	Roles      []Role
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
