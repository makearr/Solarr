package indexer

import (
	"net/url"
	"strings"
)

// Placeholders stand for the indexer's API key in a stored download link, plain and query-escaped.
const (
	keyPlaceholder        = "solarr-indexer-key"
	keyPlaceholderEscaped = "solarr-indexer-key-escaped"
)

// minSealedKeyLength keeps a short key from mangling unrelated text in a link.
const minSealedKeyLength = 8

// SealLink swaps the indexer's API key in a download link for a placeholder, so no credential is stored (Invariant 40).
func SealLink(link, apiKey string) string {
	if len(apiKey) < minSealedKeyLength {
		return link
	}
	if escaped := url.QueryEscape(apiKey); escaped != apiKey {
		link = strings.ReplaceAll(link, escaped, keyPlaceholderEscaped)
	}
	return strings.ReplaceAll(link, apiKey, keyPlaceholder)
}

// UnsealLink puts the indexer's API key back into a link SealLink made; any other link is returned as it is.
func UnsealLink(link, apiKey string) string {
	if len(apiKey) < minSealedKeyLength {
		return link
	}
	link = strings.ReplaceAll(link, keyPlaceholderEscaped, url.QueryEscape(apiKey))
	return strings.ReplaceAll(link, keyPlaceholder, apiKey)
}
