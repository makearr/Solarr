package parsebook

import (
	"strings"
	"unicode"
)

// Names says a release or file name is the book: its title (or the title before a subtitle) appears in the name as
// whole words, and so does one of its authors, by full name, by surname or as "Surname, First". This is Readarr's
// search-criteria match: a name is held against the wanted book rather than parsed alone. With no authors known, or none
// in a script a Latin name can be compared with, the title alone decides.
func Names(name, title string, authors []string) bool {
	words := " " + CleanTitle(dotsToSpaces(name)) + " "
	if !containsTitle(words, title) {
		return false
	}
	anyCompared := false
	for _, author := range authors {
		if !isLatinName(author) && isLatinName(words) {
			continue
		}
		anyCompared = true
		if NamesAuthor(words, author) {
			return true
		}
	}
	return !anyCompared
}

// isLatinName says the text holds a Latin letter; an author without one cannot be compared with a Latin release name.
func isLatinName(text string) bool {
	return strings.IndexFunc(text, func(r rune) bool { return unicode.Is(unicode.Latin, r) }) >= 0
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
