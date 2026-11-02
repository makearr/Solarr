package whisparr

import (
	"strings"
	"testing"
)

func TestShortTerm(t *testing.T) {
	longClause := "Evening Lesson With The New Teacher In Room Twelve, " + strings.Repeat("Tag Word, ", 20)
	longWords := strings.Repeat("word ", 40)
	tags := "ATM, " + strings.Repeat("Tag Word, ", 20)
	for name, test := range map[string]struct{ term, want string }{
		"short stays":           {"A Short Title", "A Short Title"},
		"first clause":          {longClause, "Evening Lesson With The New Teacher In Room Twelve"},
		"cut at a word":         {longWords, strings.TrimSpace(strings.Repeat("word ", 16))},
		"a lone tag is no clue": {tags, strings.TrimSpace(tags[:strings.LastIndexByte(tags[:maxTermRunes], ' ')])},
	} {
		if got := shortTerm(test.term); got != test.want {
			t.Errorf("%s: shortTerm = %q, want %q", name, got, test.want)
		}
	}
}
