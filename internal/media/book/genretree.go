package book

import (
	"slices"
	"strings"

	"github.com/makearr/Solarr/internal/media"
)

// GenreTree is the genres of books and audiobooks alike, Open Library's subjects, Google's categories and Audible's
// genres merged; both types share its tags.
var GenreTree = media.GenreTree{
	Key: "books", Label: "Books", Version: 1,
	Tags: []media.GenreTreeTag{
		{Name: "Fiction", Children: []media.GenreTreeTag{
			{Name: "Fantasy", Aliases: []string{"Science Fiction & Fantasy", "Fantasy fiction", "Epic Fantasy", "Urban Fantasy"}},
			{Name: "Science Fiction", Aliases: []string{"Science Fiction & Fantasy", "Science fiction", "Sci-Fi", "SF"}},
			{Name: "Mystery", Aliases: []string{"Mystery, Thriller & Suspense", "Detective and mystery stories", "Crime fiction", "Crime"}},
			{Name: "Thriller", Aliases: []string{"Mystery, Thriller & Suspense", "Suspense", "Thrillers"}},
			{Name: "Horror", Aliases: []string{"Horror fiction", "Horror tales"}},
			{Name: "Romance", Aliases: []string{"Love stories", "Romance fiction"}},
			{Name: "Historical Fiction", Aliases: []string{"Historical fiction"}},
			{Name: "Literary Fiction", Aliases: []string{"Literature & Fiction", "Literary", "Classics"}},
			{Name: "Humour", Aliases: []string{"Humor", "Comedy", "Humorous fiction"}},
			{Name: "Adventure", Aliases: []string{"Action & Adventure", "Adventure stories"}},
			{Name: "Short Stories", Aliases: []string{"Short stories"}},
		}},
		{Name: "Young Adult", Aliases: []string{"Teen & Young Adult", "Young adult fiction", "Juvenile fiction"}},
		{Name: "Children's", Aliases: []string{"Children's Audiobooks", "Juvenile literature", "Children's fiction", "Kids"}},
		{Name: "Comics", Aliases: []string{"Comics & Graphic Novels", "Graphic novels", "Comic books, strips, etc"}},
		{Name: "Poetry"},
		{Name: "Drama", Aliases: []string{"Plays", "Drama & Poetry"}},
		{Name: "Non-fiction", Aliases: []string{"Nonfiction"}, Children: []media.GenreTreeTag{
			{Name: "Biography", Aliases: []string{"Biographies & Memoirs", "Biography & Autobiography", "Memoir", "Autobiography"}},
			{Name: "History"},
			{Name: "Science", Aliases: []string{"Science & Engineering", "Popular science"}},
			{Name: "Philosophy"},
			{Name: "Religion", Aliases: []string{"Religion & Spirituality", "Spirituality"}},
			{Name: "Politics", Aliases: []string{"Politics & Social Sciences", "Political science"}},
			{Name: "Business", Aliases: []string{"Business & Careers", "Economics", "Money & Finance"}},
			{Name: "Self-help", Aliases: []string{"Self-Help", "Self Development", "Personal Development", "Health & Wellness"}},
			{Name: "Psychology"},
			{Name: "True Crime", Aliases: []string{"True crime"}},
			{Name: "Travel", Aliases: []string{"Travel & Tourism"}},
			{Name: "Cooking", Aliases: []string{"Cookery", "Food & Drink"}},
			{Name: "Art", Aliases: []string{"Arts & Entertainment"}},
			{Name: "Computers", Aliases: []string{"Computers & Technology", "Technology", "Programming"}},
			{Name: "Education", Aliases: []string{"Education & Learning", "Language Learning"}},
			{Name: "Sport", Aliases: []string{"Sports", "Sports & Outdoors"}},
		}},
	},
}

// subjectGenres are the genres to look up for a book's subjects: each as given and, for "Fantasy fiction, English",
// the part before the comma; Open Library's subjects are free keywords, so only what the tree knows becomes a tag.
func subjectGenres(subjects []string) []string {
	var genres []string
	for _, subject := range subjects {
		head, _, _ := strings.Cut(subject, ",")
		for _, name := range []string{strings.TrimSpace(subject), strings.TrimSpace(head)} {
			if name != "" && !slices.Contains(genres, name) {
				genres = append(genres, name)
			}
		}
	}
	return genres
}
