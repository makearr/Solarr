package app

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media"
)

// seedGenreTrees applies each media type's shipped genre tree and moves genres stored before they were tags into it;
// a failure is logged, never fatal: the tags already there keep working.
func (p *process) seedGenreTrees(ctx context.Context) {
	people := library.NewPeople(p.library)
	byType := map[string]string{}
	seeded := map[string]bool{}
	for _, descriptor := range p.media.Descriptors() {
		genreTree := descriptor.GenreTree
		byType[string(descriptor.Key)] = genreTree.Key
		if genreTree.Key == "" || seeded[genreTree.Key] {
			continue
		}
		seeded[genreTree.Key] = true
		if err := people.SeedGenreTree(ctx, genreTree.Key, genreTree.Version, genreTreeTags(genreTree.Tags)); err != nil {
			p.logger.Warn("genre tree not applied", slog.String("genre_tree", genreTree.Key), slog.Any("error", err))
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

func genreTreeTags(tags []media.GenreTreeTag) []library.GenreTreeTag {
	out := make([]library.GenreTreeTag, len(tags))
	for i, tag := range tags {
		out[i] = library.GenreTreeTag{Name: tag.Name, Aliases: tag.Aliases, Children: genreTreeTags(tag.Children)}
	}
	return out
}
