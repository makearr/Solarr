package parsebook

import (
	"strings"
)

// Names says a release or file name is the book: its title (or the title before a subtitle) appears in the name as
// whole words, and so does one of its authors, by full name, by surname or as "Surname, First". This is Readarr's
// search-criteria match: a name is held against the wanted book rather than parsed alone. With no authors known the
// title alone decides.
func Names(name, title string, authors []string) bool {
	words := " " + CleanTitle(dotsToSpaces(name)) + " "
	if !containsTitle(words, title) {
		return false
	}
	if len(authors) == 0 {
		return true
	}
	for _, author := range authors {
		if NamesAuthor(words, author) {
			return true
		}
	}
	return false
}

// NamesAuthor says cleaned words (spaced at both ends) name the author: in full, by surname of four letters or more,
// or as their initials and surname.
func NamesAuthor(words, author string) bool {
	clean := CleanTitle(author)
	if clean == "" {
		return false
	}
	if strings.Contains(words, " "+clean+" ") {
		return true
	}
	parts := strings.Fields(clean)
	surname := parts[len(parts)-1]
	if len([]rune(surname)) >= 4 && strings.Contains(words, " "+surname+" ") {
		return true
	}
	return false
}

func containsTitle(words, title string) bool {
	for _, candidate := range titleForms(title) {
		if candidate != "" && strings.Contains(words, " "+candidate+" ") {
			return true
		}
	}
	return false
}

// titleForms are the spellings of a title a name may use: whole, before its subtitle, without a leading article.
func titleForms(title string) []string {
	forms := []string{CleanTitle(title)}
	for _, cut := range []string{":", " - ", "("} {
		if before, _, found := strings.Cut(title, cut); found {
			if short := CleanTitle(before); len(strings.Fields(short)) >= 1 && len([]rune(short)) >= 3 {
				forms = append(forms, short)
			}
		}
	}
	return forms
}

func dotsToSpaces(name string) string {
	if strings.Contains(name, " ") {
		return name
	}
	return strings.NewReplacer(".", " ", "_", " ").Replace(name)
}
