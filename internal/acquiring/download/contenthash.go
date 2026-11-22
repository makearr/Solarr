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

// NZBContentHash hashes an NZB's sorted article message-ids, which indexers never rewrite (section 10.11 failure memory by content); empty when there are no articles.
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
