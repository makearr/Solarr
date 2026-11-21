package download_test

import (
	"errors"
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/clienttest"
	"github.com/makearr/Solarr/internal/acquiring/download"
)

func TestCheckRelease_TellsAReleaseFromWhatAnIndexerSendsInstead(t *testing.T) {
	for _, test := range []struct {
		name     string
		protocol string
		body     []byte
		want     *download.NotAReleaseError // nil: accepted
	}{
		{"an NZB", "usenet", clienttest.NZB(), nil},
		{"an NZB after a byte order mark and a declaration", "usenet", append([]byte("\xef\xbb\xbf"), clienttest.NZB()...), nil},
		{"an NZB with no articles", "usenet", []byte(`<nzb></nzb>`), &download.NotAReleaseError{Description: "no NZB came back"}},
		{"a bad key", "usenet", []byte(`<?xml version="1.0"?><error code="100" description="Incorrect user credentials"/>`),
			&download.NotAReleaseError{Code: 100, Description: "Incorrect user credentials"}},
		{"an item that is gone", "usenet", []byte(`<error code="300" description="no such item"/>`),
			&download.NotAReleaseError{Code: 300, Description: "no such item"}},
		{"a sign-in page", "usenet", []byte(`<html><body>sign in</body></html>`), &download.NotAReleaseError{Description: "no NZB came back"}},
		{"a torrent file", "torrent", clienttest.TorrentFile("Invented Release"), nil},
		{"a sign-in page for a torrent", "torrent", []byte(`<html></html>`), &download.NotAReleaseError{Description: "no torrent file came back"}},
		{"a magnet link", "torrent", []byte("magnet:?xt=urn:btih:abc"), nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := download.CheckRelease(test.protocol, test.body)
			var got *download.NotAReleaseError
			switch {
			case test.want == nil && err != nil:
				t.Errorf("CheckRelease = %v, want it accepted", err)
			case test.want != nil && !errors.As(err, &got):
				t.Errorf("CheckRelease = %v, want %+v", err, test.want)
			case test.want != nil && *got != *test.want:
				t.Errorf("CheckRelease = %+v, want %+v", *got, *test.want)
			}
		})
	}
}

func TestNotAReleaseError_AccountRefusedIsTheAccountCodes(t *testing.T) {
	for code, want := range map[int]bool{100: true, 101: true, 102: true, 910: true, 99: false, 103: false, 300: false, 500: false, 0: false} {
		if got := (&download.NotAReleaseError{Code: code}).AccountRefused(); got != want {
			t.Errorf("code %d: AccountRefused = %v, want %v", code, got, want)
		}
	}
}
