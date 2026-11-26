package comic

import (
	"errors"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

func TestDefectOf(t *testing.T) {
	cases := []struct {
		name         string
		ext          string
		facts        Facts
		err          error
		reason, text string
	}{
		{"readable archive", ".cbz", Facts{Pages: Pages{Count: 12}}, nil, "", ""},
		{"unreadable archive", ".CBR", Facts{}, errors.New("bad header"), library.DefectUnreadable, "bad header"},
		{"archive without pages", ".cb7", Facts{}, nil, library.DefectEmpty, "no page images"},
		{"pdf pages are not counted from images", ".pdf", Facts{}, nil, "", ""},
		{"epub may hold text only", ".epub", Facts{}, nil, "", ""},
	}
	for _, c := range cases {
		reason, text := defectOf(c.ext, c.facts, c.err)
		if reason != c.reason || text != c.text {
			t.Errorf("%s: defectOf = %q, %q; want %q, %q", c.name, reason, text, c.reason, c.text)
		}
	}
}
