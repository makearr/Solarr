package download

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"slices"
	"strings"
)

// nzbSegment is one article of an NZB: <segment bytes=".." number="..">message-id</segment>.
var nzbSegment = regexp.MustCompile(`<segment\b[^>]*>([^<]+)</segment>`)

// NZBContentHash identifies what an NZB downloads: its article message-ids, sorted and hashed.
// Indexers rewrite an NZB's head and file names, never the articles, so one upload listed on two
// indexers hashes the same (section 10.11's failure memory by content). Empty when the file has
// no articles, which is no identity at all.
func NZBContentHash(nzb []byte) string {
	matches := nzbSegment.FindAllSubmatch(nzb, -1)
	if len(matches) == 0 {
		return ""
	}
	ids := make([]string, len(matches))
	for i, match := range matches {
		ids[i] = strings.TrimSpace(string(match[1]))
	}
	slices.Sort(ids)
	sum := sha256.Sum256([]byte(strings.Join(ids, "\n")))
	return "nzb:" + hex.EncodeToString(sum[:])
}
