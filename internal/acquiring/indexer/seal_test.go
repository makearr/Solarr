package indexer_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
)

func TestSealLinkRoundTripsAndStoresNoKey(t *testing.T) {
	for _, tc := range []struct{ name, key, link, sealed string }{
		{"query", "abcd1234efgh", "https://nzb.example/getnzb/1.nzb?apikey=abcd1234efgh&i=2",
			"https://nzb.example/getnzb/1.nzb?apikey=solarr-indexer-key&i=2"},
		{"path", "abcd1234efgh", "https://t.example/dl/abcd1234efgh/9.torrent", "https://t.example/dl/solarr-indexer-key/9.torrent"},
		{"escaped", "ab cd+1234", "https://t.example/dl?key=ab+cd%2B1234", "https://t.example/dl?key=solarr-indexer-key-escaped"},
		{"short key left alone", "abc", "https://t.example/abc", "https://t.example/abc"},
		{"magnet without key", "abcd1234efgh", "magnet:?xt=urn:btih:1", "magnet:?xt=urn:btih:1"},
	} {
		if got := indexer.SealLink(tc.link, tc.key); got != tc.sealed {
			t.Errorf("%s: sealed = %q, want %q", tc.name, got, tc.sealed)
		}
		if got := indexer.UnsealLink(tc.sealed, tc.key); got != tc.link {
			t.Errorf("%s: unsealed = %q, want %q", tc.name, got, tc.link)
		}
	}
}
