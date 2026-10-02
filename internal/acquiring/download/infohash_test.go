package download_test

import (
	"crypto/sha1" //nolint:gosec // the info hash under test is SHA-1
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/download"
)

const torrentInfo = "d6:lengthi5e4:name1:a12:piece lengthi16384e6:pieces20:01234567890123456789e"

func torrentFile() []byte { return []byte("d8:announce3:url4:info" + torrentInfo + "7:comment1:xe") }

func TestInfoHash_IsTheSHA1OfTheInfoDictionaryAndReadsMagnets(t *testing.T) {
	sum := sha1.Sum([]byte(torrentInfo)) //nolint:gosec // see above
	want := hex.EncodeToString(sum[:])
	got, err := download.InfoHash(torrentFile())
	if err != nil || got != want {
		t.Fatalf("InfoHash = %q, %v, want %q", got, err, want)
	}
	if got, _ := download.InfoHash([]byte("magnet:?xt=urn:btih:" + strings.ToUpper(want) + "&dn=x")); got != want {
		t.Errorf("hex magnet = %q, want %q", got, want)
	}
	if _, err := download.InfoHash([]byte("<nzb/>")); !errors.Is(err, download.ErrNoInfoHash) {
		t.Errorf("an NZB: %v, want ErrNoInfoHash", err)
	}
	if _, err := download.InfoHash([]byte("d4:spam3:egge")); !errors.Is(err, download.ErrNoInfoHash) {
		t.Errorf("a dictionary with no info: %v, want ErrNoInfoHash", err)
	}
}

func TestTorrentContentHash_IsEmptyForWhatIsNotATorrent(t *testing.T) {
	if got := download.TorrentContentHash([]byte("<nzb/>")); got != "" {
		t.Errorf("an NZB: %q, want none", got)
	}
	if got := download.TorrentContentHash(torrentFile()); !strings.HasPrefix(got, "bt:") || len(got) != 43 {
		t.Errorf("a torrent: %q, want bt: and a 40 character hash", got)
	}
}
