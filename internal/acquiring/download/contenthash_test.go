package download_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/download"
)

func nzb(head string, segments ...string) []byte {
	body := `<?xml version="1.0"?><nzb><head>` + head + `</head><file subject="x"><segments>`
	for i, id := range segments {
		body += `<segment bytes="100" number="` + string(rune('1'+i)) + `">` + id + `</segment>`
	}
	return []byte(body + `</segments></file></nzb>`)
}

// Section 10.11: one upload listed by two indexers is one content, whatever each writes in its head.
func TestNZBContentHash_TheSameArticlesAreTheSameContent(t *testing.T) {
	first := download.NZBContentHash(nzb(`<meta type="title">One</meta>`, "a@x", "b@x"))
	reordered := download.NZBContentHash(nzb(`<meta type="name">Other</meta>`, "b@x", "a@x"))
	other := download.NZBContentHash(nzb("", "a@x", "c@x"))
	if first == "" || first != reordered {
		t.Errorf("same articles hashed %q and %q, want one non-empty hash", first, reordered)
	}
	if other == first {
		t.Error("different articles hashed the same")
	}
	if got := download.NZBContentHash([]byte("not an nzb")); got != "" {
		t.Errorf("no articles hashed %q, want empty", got)
	}
}
