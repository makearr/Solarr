package download_test

import (
	"crypto/sha1" //nolint:gosec // the info hash under test is SHA-1
	"crypto/sha256"
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

// A string length near the integer limit is malformed, never a panic: a torrent file is the indexer's.
func TestInfoHash_AnOverlongStringLengthIsRefused(t *testing.T) {
	for _, file := range []string{
		"d9223372036854775807:xe",
		"d4:infod4:name9223372036854775807:xee",
	} {
		if _, err := download.InfoHash([]byte(file)); err == nil {
			t.Errorf("InfoHash(%q) succeeded", file)
		}
		if name := download.TorrentName([]byte(file)); name != "" {
			t.Errorf("TorrentName(%q) = %q", file, name)
		}
	}
}

func TestInfoHash_AV2OnlyTorrentIsListedByTheSHA256OfItsInfo(t *testing.T) {
	const v2Info = "d12:meta versioni2e4:name1:a12:piece lengthi16384ee"
	sum := sha256.Sum256([]byte(v2Info))
	want := hex.EncodeToString(sum[:20])
	got, err := download.InfoHash([]byte("d4:info" + v2Info + "e"))
	if err != nil || got != want {
		t.Errorf("v2 only = %q, %v, want %q", got, err, want)
	}

	const hybridInfo = "d12:meta versioni2e4:name1:a12:piece lengthi16384e6:pieces20:01234567890123456789e"
	v1 := sha1.Sum([]byte(hybridInfo)) //nolint:gosec // see above
	if got, err := download.InfoHash([]byte("d4:info" + hybridInfo + "e")); err != nil || got != hex.EncodeToString(v1[:]) {
		t.Errorf("hybrid = %q, %v, want the v1 hash", got, err)
	}
}

func TestInfoHash_ReadsAV2MagnetAndPrefersTheV1Hash(t *testing.T) {
	multihash := strings.Repeat("ab", 32)
	if got, err := download.InfoHash([]byte("magnet:?xt=urn:btmh:1220" + multihash + "&dn=x")); err != nil || got != multihash[:40] {
		t.Errorf("btmh = %q, %v, want the first 40 hex", got, err)
	}
	v1 := strings.Repeat("cd", 20)
	both := "magnet:?xt=urn:btmh:1220" + multihash + "&xt=urn:btih:" + v1
	if got, _ := download.InfoHash([]byte(both)); got != v1 {
		t.Errorf("both = %q, want the btih %q", got, v1)
	}
	if _, err := download.InfoHash([]byte("magnet:?xt=urn:btmh:1220abcd")); !errors.Is(err, download.ErrNoInfoHash) {
		t.Errorf("a short btmh: %v, want ErrNoInfoHash", err)
	}
}
