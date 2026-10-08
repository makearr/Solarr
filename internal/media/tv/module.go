package tv

import (
	"context"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/plan"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/moving/naming"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

// Module is TV as the media registry sees it. The zero value declares the type; Sweep makes it scannable.
type Module struct {
	Sweep scan.Sweep
	// Hunt searches for one wanted season and sends the best release; nil leaves TV unhunted.
	Hunt func(ctx context.Context, itemID int64) (plan.Outcome, error)
	// Feed is TV's part of the RSS sync.
	Feed
	// CutoffJob is TV's part of the cutoff check.
	CutoffJob
	// Retry identifies again the files that waited for review; nil leaves the job unregistered.
	Retry *RetryJob
	// Following is TV's part of a follow.
	Following
	// Profiling is TV's part of a person's page.
	Profiling
}

// ScanSweep implements media.Scanning.
func (m Module) ScanSweep() scan.Sweep { return m.Sweep }

// HuntSearch implements media.Hunting.
func (m Module) HuntSearch(ctx context.Context, itemID int64) (plan.Outcome, error) {
	if m.Hunt == nil {
		return plan.OutcomeNotWanted, nil
	}
	return m.Hunt(ctx, itemID)
}

// ReleaseLabel implements media.Labelling.
func (Module) ReleaseLabel(releaseTitle, itemRef string, now time.Time) string {
	return ReleaseLabel(releaseTitle, itemRef, now)
}

// RegisterJobs implements media.Jobs.
func (m Module) RegisterJobs(register media.JobRegistrar) error {
	if m.Retry == nil {
		return nil
	}
	return register(RetryDefinition, m.Retry)
}

// Descriptor implements media.Module.
func (Module) Descriptor() media.Descriptor {
	return media.Descriptor{
		Key:   media.Key(MediaType),
		Label: "TV",
		Vocabulary: media.Vocabulary{
			Work:         media.Term{One: "Series", Many: "Series"},
			Part:         media.Term{One: "Episode", Many: "Episodes"},
			Group:        media.Term{One: "Season", Many: "Seasons"},
			LeadPeople:   media.Term{One: "Cast member", Many: "Cast"},
			Organisation: media.Term{One: "Network", Many: "Networks"},
		},
		Levels: Navigation{}.Levels(),
		Roles: []media.Role{
			{Key: "actor", Label: "Cast", Followable: true, Character: true},
			{Key: "creator", Label: "Creator", Followable: true},
			{Key: "showrunner", Label: "Showrunner", Followable: true},
			{Key: "executive_producer", Label: "Executive producer", Followable: true},
			{Key: "producer", Label: "Producer", Followable: true},
			{Key: "composer", Label: "Composer", Followable: true},
			{Key: "director", Label: "Director", Followable: true, PartScoped: true},
			{Key: "writer", Label: "Writer", Followable: true, PartScoped: true},
			{Key: "guest_star", Label: "Guest star", PartScoped: true, Character: true},
		},
		FollowOptions: followOptions,
		Facets:        facets,
	}
}

// NamingScheme implements media.Naming.
func (Module) NamingScheme() naming.Scheme { return NamingScheme }

// DefaultNamingProfile implements media.Naming.
func (Module) DefaultNamingProfile() settings.NamingProfile { return DefaultNamingProfile() }

// ValidateNaming implements media.Naming.
func (Module) ValidateNaming(profile settings.NamingProfile) error { return ValidateNaming(profile) }

// NamingExamples implements media.Naming.
func (Module) NamingExamples(profile settings.NamingProfile) []media.NamingExample {
	var out []media.NamingExample
	for _, example := range NamingExamples(NamingFor(&profile)) {
		out = append(out, media.NamingExample{Label: example.Label, Path: example.Path})
	}
	return out
}
