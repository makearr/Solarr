package app

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media"
)

// seedTaxonomies applies each media type's shipped genre tree and moves genres stored before they were tags into it;
// a failure is logged, never fatal: the tags already there keep working.
func (p *process) seedTaxonomies(ctx context.Context) {
	people := library.NewPeople(p.library)
	byType := map[string]string{}
	seeded := map[string]bool{}
	for _, descriptor := range p.media.Descriptors() {
		taxonomy := descriptor.Taxonomy
		byType[string(descriptor.Key)] = taxonomy.Key
		if taxonomy.Key == "" || seeded[taxonomy.Key] {
			continue
		}
		seeded[taxonomy.Key] = true
		if err := people.SeedTaxonomy(ctx, taxonomy.Key, taxonomy.Version, taxonomyTags(taxonomy.Tags)); err != nil {
			p.logger.Warn("genre taxonomy not applied", slog.String("taxonomy", taxonomy.Key), slog.Any("error", err))
		}
	}
	converted, err := people.ConvertClassificationGenres(ctx, func(mediaType string) string { return byType[mediaType] })
	if err != nil {
		p.logger.Warn("genres not moved to tags", slog.Any("error", err))
	}
	if converted > 0 {
		p.logger.Info("genres moved to tags", slog.Int("works", converted))
	}
}

func taxonomyTags(tags []media.TaxonomyTag) []library.TaxonomyTag {
	out := make([]library.TaxonomyTag, len(tags))
	for i, tag := range tags {
		out[i] = library.TaxonomyTag{Name: tag.Name, Aliases: tag.Aliases, Children: taxonomyTags(tag.Children)}
	}
	return out
}
