package music

import (
	"context"

	"github.com/makearr/Solarr/internal/acquiring/plan"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/moving/naming"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

// MediaType is music's key: category.media_type and work.media_type carry it.
const MediaType media.Key = "music"

// Module is music as the media registry sees it. The zero value declares the type; Sweep makes it scannable.
type Module struct {
	Sweep scan.Sweep
	// Hunt searches for one wanted album and sends the best release; nil leaves music unhunted.
	Hunt func(ctx context.Context, itemID int64) (plan.Outcome, error)
	// RSS matches the indexers' feeds against the wanted albums; nil leaves the job unregistered.
	RSS *RSSJob
}

// HuntSearch implements media.Hunting.
func (m Module) HuntSearch(ctx context.Context, itemID int64) (plan.Outcome, error) {
	if m.Hunt == nil {
		return plan.OutcomeNotWanted, nil
	}
	return m.Hunt(ctx, itemID)
}

// RegisterJobs implements media.Jobs.
func (m Module) RegisterJobs(register media.JobRegistrar) error {
	if m.RSS == nil {
		return nil
	}
	return register(RSSDefinition, m.RSS)
}

// ScanSweep implements media.Scanning.
func (m Module) ScanSweep() scan.Sweep { return m.Sweep }

// Descriptor implements media.Module.
func (Module) Descriptor() media.Descriptor {
	return media.Descriptor{
		Key:   MediaType,
		Label: "Music",
		Vocabulary: media.Vocabulary{
			Work:         media.Term{One: "Album", Many: "Albums"},
			Part:         media.Term{One: "Track", Many: "Tracks"},
			Group:        media.Term{One: "Disc", Many: "Discs"},
			LeadPeople:   media.Term{One: "Artist", Many: "Artists"},
			Organisation: media.Term{One: "Label", Many: "Labels"},
		},
		Levels: Navigation{}.Levels(),
		Roles: []media.Role{
			{Key: "artist", Label: "Artist", Lead: true, Followable: true},
		},
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
