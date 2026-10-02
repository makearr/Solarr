package download

import (
	"crypto/sha1" //nolint:gosec // BitTorrent's own info hash is SHA-1, an identity and not a security measure
	"encoding/base32"
	"encoding/hex"
	"errors"
	"net/url"
	"strconv"
	"strings"
)

// ErrNoInfoHash reports bytes that are not a torrent file or a magnet link naming a BitTorrent v1 info hash.
var ErrNoInfoHash = errors.New("download: no info hash in the torrent")

// InfoHash answers the lowercase hex info hash of a torrent file's bytes, or of a magnet link.
func InfoHash(file []byte) (string, error) {
	if strings.HasPrefix(string(file), "magnet:") {
		return magnetHash(string(file))
	}
	start, end, err := infoSpan(file)
	if err != nil {
		return "", err
	}
	sum := sha1.Sum(file[start:end]) //nolint:gosec // see above
	return hex.EncodeToString(sum[:]), nil
}

func magnetHash(magnet string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(magnet))
	if err != nil {
		return "", ErrNoInfoHash
	}
	for _, topic := range parsed.Query()["xt"] {
		btih, found := strings.CutPrefix(strings.ToLower(topic), "urn:btih:")
		if !found {
			continue
		}
		switch len(btih) {
		case 40:
			if _, err := hex.DecodeString(btih); err == nil {
				return btih, nil
			}
		case 32:
			if raw, err := base32.StdEncoding.DecodeString(strings.ToUpper(btih)); err == nil {
				return hex.EncodeToString(raw), nil
			}
		}
	}
	return "", ErrNoInfoHash
}

// infoSpan finds the byte range of the top-level "info" value of a bencoded torrent, which is what the hash covers.
func infoSpan(data []byte) (start, end int, err error) {
	if len(data) == 0 || data[0] != 'd' {
		return 0, 0, ErrNoInfoHash
	}
	position := 1
	for position < len(data) && data[position] != 'e' {
		key, next, err := readString(data, position)
		if err != nil {
			return 0, 0, err
		}
		valueEnd, err := skipValue(data, next)
		if err != nil {
			return 0, 0, err
		}
		if key == "info" {
			return next, valueEnd, nil
		}
		position = valueEnd
	}
	return 0, 0, ErrNoInfoHash
}

func readString(data []byte, position int) (string, int, error) {
	colon := position
	for colon < len(data) && data[colon] != ':' {
		colon++
	}
	length, err := strconv.Atoi(string(data[position:min(colon, len(data))]))
	if err != nil || colon >= len(data) || length < 0 || colon+1+length > len(data) {
		return "", 0, ErrNoInfoHash
	}
	return string(data[colon+1 : colon+1+length]), colon + 1 + length, nil
}

func skipValue(data []byte, position int) (int, error) {
	if position >= len(data) {
		return 0, ErrNoInfoHash
	}
	switch c := data[position]; {
	case c == 'i':
		for end := position; end < len(data); end++ {
			if data[end] == 'e' {
				return end + 1, nil
			}
		}
		return 0, ErrNoInfoHash
	case c == 'l' || c == 'd':
		position++
		for position < len(data) && data[position] != 'e' {
			var err error
			if position, err = skipValue(data, position); err != nil {
				return 0, err
			}
		}
		if position >= len(data) {
			return 0, ErrNoInfoHash
		}
		return position + 1, nil
	case c >= '0' && c <= '9':
		_, next, err := readString(data, position)
		return next, err
	}
	return 0, ErrNoInfoHash
}

// TorrentContentHash identifies what a torrent downloads, as NZBContentHash does an NZB: the info hash, which every
// indexer listing the same torrent shares. Empty when the bytes name none.
func TorrentContentHash(file []byte) string {
	hash, err := InfoHash(file)
	if err != nil {
		return ""
	}
	return "bt:" + hash
}
