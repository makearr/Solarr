package download

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// NotAReleaseError is what an indexer sent in place of the release: the code and description of its own error answer,
// or zero and a description of what came instead (a sign-in page, a proxy's error).
type NotAReleaseError struct {
	Code        int
	Description string
}

func (e *NotAReleaseError) Error() string {
	if e.Code != 0 {
		return fmt.Sprintf("the indexer answered error %d: %s", e.Code, e.Description)
	}
	return "the indexer did not send a release: " + e.Description
}

// Newznab codes for an item the indexer no longer has; the rest are the indexer's own trouble (key, limits, API off).
const (
	newznabNoSuchItem = 300
	newznabItemGone   = 410
)

// Gone says the indexer no longer serves this one release, as opposed to being unable to serve any.
func (e *NotAReleaseError) Gone() bool {
	return e.Code == newznabNoSuchItem || e.Code == newznabItemGone
}

var newznabError = regexp.MustCompile(`(?is)<error\b[^>]*\bcode="(\d+)"[^>]*\bdescription="([^"]*)"`)

// nzbHeadWindow is how much of an NZB is searched for its root element.
const nzbHeadWindow = 64 << 10

// CheckRelease refuses a body that is not what the protocol's client takes: an NZB with articles, a torrent file, or a
// magnet link. A client given anything else fails the download as if the release were bad.
func CheckRelease(protocol string, body []byte) error {
	trimmed := bytes.TrimSpace(body)
	if strings.HasPrefix(string(trimmed), "magnet:") {
		return nil
	}
	switch protocol {
	case "usenet":
		head := trimmed[:min(len(trimmed), nzbHeadWindow)]
		if bytes.Contains(bytes.ToLower(head), []byte("<nzb")) && NZBContentHash(body) != "" {
			return nil
		}
		if match := newznabError.FindSubmatch(trimmed); match != nil {
			code, _ := strconv.Atoi(string(match[1]))
			return &NotAReleaseError{Code: code, Description: string(match[2])}
		}
		return &NotAReleaseError{Description: "no NZB came back"}
	case "torrent":
		if len(trimmed) > 0 && trimmed[0] == 'd' && bytes.Contains(trimmed, []byte("4:info")) {
			return nil
		}
		return &NotAReleaseError{Description: "no torrent file came back"}
	}
	return nil
}
