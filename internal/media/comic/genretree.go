package comic

import "github.com/makearr/Solarr/internal/media"

// GenreTree is the genres of comics and manga: AniList's genres and the western publishers' categories merged.
var GenreTree = media.GenreTree{
	Key: "comics", Label: "Comics", Version: 1,
	Tags: []media.GenreTreeTag{
		{Name: "Action", Aliases: []string{"Action & Adventure"}},
		{Name: "Adventure"},
		{Name: "Comedy", Aliases: []string{"Humor", "Humour"}},
		{Name: "Drama"},
		{Name: "Fantasy"},
		{Name: "Horror", Aliases: []string{"Supernatural"}},
		{Name: "Mystery", Aliases: []string{"Crime", "Detective"}},
		{Name: "Romance"},
		{Name: "Science Fiction", Aliases: []string{"Sci-Fi", "Mecha"}},
		{Name: "Slice of Life"},
		{Name: "Sports"},
		{Name: "Superhero", Aliases: []string{"Super-Hero", "Superheroes"}},
		{Name: "Thriller", Aliases: []string{"Psychological"}},
		{Name: "Western"},
		{Name: "War", Aliases: []string{"Military"}},
		{Name: "Historical", Aliases: []string{"History"}},
	},
}
