// Package screen holds what film and television share as data: the genre tree both map their genres onto.
package screen

import "github.com/makearr/Solarr/internal/media"

// GenreTree is the genres of series and films alike, TMDB's, TVmaze's and Skyhook's merged (plan 10-libraries L1).
var GenreTree = media.GenreTree{
	Key: "screen", Label: "Film and TV", Version: 1,
	Tags: []media.GenreTreeTag{
		{Name: "Action", Aliases: []string{"Action & Adventure"}, Children: []media.GenreTreeTag{{Name: "Martial Arts"}}},
		{Name: "Adventure", Aliases: []string{"Action & Adventure"}},
		{Name: "Animation", Children: []media.GenreTreeTag{{Name: "Anime"}}},
		{Name: "Comedy", Children: []media.GenreTreeTag{{Name: "Sitcom"}}},
		{Name: "Crime"},
		{Name: "Documentary"},
		{Name: "Drama", Children: []media.GenreTreeTag{{Name: "Soap"}}},
		{Name: "Family"},
		{Name: "Fantasy", Aliases: []string{"Sci-Fi & Fantasy"}},
		{Name: "History"},
		{Name: "Horror"},
		{Name: "Kids", Aliases: []string{"Children"}},
		{Name: "Music", Children: []media.GenreTreeTag{{Name: "Musical"}}},
		{Name: "Mystery"},
		{Name: "News"},
		{Name: "Reality", Children: []media.GenreTreeTag{
			{Name: "Game Show"}, {Name: "Food"}, {Name: "Home and Garden"}, {Name: "Travel"},
		}},
		{Name: "Romance"},
		{Name: "Science Fiction", Aliases: []string{"Sci-Fi & Fantasy", "Sci-Fi", "SciFi"}},
		{Name: "Sport", Aliases: []string{"Sports"}},
		{Name: "Talk", Aliases: []string{"Talk Show"}},
		{Name: "Thriller", Children: []media.GenreTreeTag{{Name: "Suspense"}}},
		{Name: "War", Aliases: []string{"War & Politics"}},
		{Name: "Politics", Aliases: []string{"War & Politics"}},
		{Name: "Western"},
		{Name: "Mini-Series"},
		{Name: "TV Movie"},
	},
}
