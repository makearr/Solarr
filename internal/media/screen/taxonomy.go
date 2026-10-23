// Package screen holds what film and television share as data: the genre taxonomy both map their genres onto.
package screen

import "github.com/makearr/Solarr/internal/media"

// Taxonomy is the genres of series and films alike, TMDB's, TVmaze's and Skyhook's merged (plan 10-libraries L1).
var Taxonomy = media.Taxonomy{
	Key: "screen", Label: "Film and TV", Version: 1,
	Tags: []media.TaxonomyTag{
		{Name: "Action", Aliases: []string{"Action & Adventure"}, Children: []media.TaxonomyTag{{Name: "Martial Arts"}}},
		{Name: "Adventure", Aliases: []string{"Action & Adventure"}},
		{Name: "Animation", Children: []media.TaxonomyTag{{Name: "Anime"}}},
		{Name: "Comedy", Children: []media.TaxonomyTag{{Name: "Sitcom"}}},
		{Name: "Crime"},
		{Name: "Documentary"},
		{Name: "Drama", Children: []media.TaxonomyTag{{Name: "Soap"}}},
		{Name: "Family"},
		{Name: "Fantasy", Aliases: []string{"Sci-Fi & Fantasy"}},
		{Name: "History"},
		{Name: "Horror"},
		{Name: "Kids", Aliases: []string{"Children"}},
		{Name: "Music", Children: []media.TaxonomyTag{{Name: "Musical"}}},
		{Name: "Mystery"},
		{Name: "News"},
		{Name: "Reality", Children: []media.TaxonomyTag{
			{Name: "Game Show"}, {Name: "Food"}, {Name: "Home and Garden"}, {Name: "Travel"},
		}},
		{Name: "Romance"},
		{Name: "Science Fiction", Aliases: []string{"Sci-Fi & Fantasy", "Sci-Fi", "SciFi"}},
		{Name: "Sport", Aliases: []string{"Sports"}},
		{Name: "Talk", Aliases: []string{"Talk Show"}},
		{Name: "Thriller", Children: []media.TaxonomyTag{{Name: "Suspense"}}},
		{Name: "War", Aliases: []string{"War & Politics"}},
		{Name: "Politics", Aliases: []string{"War & Politics"}},
		{Name: "Western"},
		{Name: "Mini-Series"},
		{Name: "TV Movie"},
	},
}
