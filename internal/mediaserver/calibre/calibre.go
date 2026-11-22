// Package calibre is the client for a Calibre content server, with or without its digest or basic authentication.
package calibre

import (
	"context"
	"crypto/md5" //nolint:gosec // HTTP digest authentication names MD5 as an algorithm
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/makearr/Solarr/internal/mediaserver"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// Client talks to one content server.
type Client struct {
	doer     mediaserver.Doer
	base     string
	username string
	password string
	cnonce   func() string
}

// New builds a client; with an empty username no credentials are sent. cnonce answers a random client nonce.
func New(doer mediaserver.Doer, base, username, password string, cnonce func() string) *Client {
	return &Client{doer: doer, base: base, username: username, password: password, cnonce: cnonce}
}

func (c *Client) get(ctx context.Context, path string, into any) error {
	address := mediaserver.Join(c.base, path)
	header := http.Header{}
	err := mediaserver.Call(ctx, c.doer, http.MethodGet, address, header, nil, into)
	var refused *httpx.StatusError
	if !errors.As(err, &refused) || refused.Status != http.StatusUnauthorized || c.username == "" {
		return mediaserver.Refused(err)
	}
	authorization, ok := c.answer(refused.Header.Values("WWW-Authenticate"), path)
	if !ok {
		return mediaserver.Refused(err)
	}
	header.Set("Authorization", authorization)
	return mediaserver.Refused(mediaserver.Call(ctx, c.doer, http.MethodGet, address, header, nil, into))
}

// answer builds the Authorization header for the strongest challenge offered: digest, else basic.
func (c *Client) answer(challenges []string, uri string) (string, bool) {
	for _, challenge := range challenges {
		if scheme, rest, _ := strings.Cut(challenge, " "); strings.EqualFold(scheme, "Digest") {
			return c.digest(parseParams(rest), uri), true
		}
	}
	for _, challenge := range challenges {
		if strings.HasPrefix(strings.ToLower(challenge), "basic") {
			return "Basic " + base64.StdEncoding.EncodeToString([]byte(c.username+":"+c.password)), true
		}
	}
	return "", false
}

func (c *Client) digest(params map[string]string, uri string) string {
	newHash := md5.New //nolint:gosec // see import
	algorithm := params["algorithm"]
	if strings.EqualFold(algorithm, "SHA-256") {
		newHash = sha256.New
	} else {
		algorithm = "MD5"
	}
	sum := func(parts ...string) string {
		digest := newHash()
		_, _ = digest.Write([]byte(strings.Join(parts, ":")))
		return hex.EncodeToString(digest.Sum(nil))
	}
	nonce, cnonce, count := params["nonce"], c.cnonce(), "00000001"
	ha1 := sum(c.username, params["realm"], c.password)
	ha2 := sum(http.MethodGet, uri)
	fields := map[string]string{
		"username": c.username, "realm": params["realm"], "nonce": nonce, "uri": uri, "algorithm": algorithm,
	}
	if params["qop"] != "" {
		fields["qop"], fields["nc"], fields["cnonce"] = "auth", count, cnonce
		fields["response"] = sum(ha1, nonce, count, cnonce, "auth", ha2)
	} else {
		fields["response"] = sum(ha1, nonce, ha2)
	}
	if opaque := params["opaque"]; opaque != "" {
		fields["opaque"] = opaque
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for index, name := range names {
		if name == "qop" || name == "nc" || name == "algorithm" {
			parts[index] = name + "=" + fields[name]
		} else {
			parts[index] = fmt.Sprintf("%s=%q", name, fields[name])
		}
	}
	return "Digest " + strings.Join(parts, ", ")
}

// parseParams reads the key="value" pairs of a challenge.
func parseParams(text string) map[string]string {
	params := map[string]string{}
	for len(text) > 0 {
		text = strings.TrimLeft(text, ", ")
		name, rest, found := strings.Cut(text, "=")
		if !found {
			break
		}
		var value string
		if strings.HasPrefix(rest, `"`) {
			end := strings.Index(rest[1:], `"`)
			if end < 0 {
				break
			}
			value, text = rest[1:end+1], rest[end+2:]
		} else {
			value, text, _ = strings.Cut(rest, ",")
		}
		params[strings.ToLower(strings.TrimSpace(name))] = value
	}
	return params
}

type libraryInfo struct {
	LibraryMap map[string]string `json:"library_map"`
}

// Test reads the library list; the content server does not publish a version there.
func (c *Client) Test(ctx context.Context) (mediaserver.Info, error) {
	if _, err := c.Libraries(ctx); err != nil {
		return mediaserver.Info{}, err
	}
	return mediaserver.Info{Name: "Calibre content server", Admin: true}, nil
}

// Libraries lists the Calibre libraries the server offers, by name.
func (c *Client) Libraries(ctx context.Context) ([]mediaserver.Library, error) {
	var info libraryInfo
	if err := c.get(ctx, "/ajax/library-info", &info); err != nil {
		return nil, err
	}
	libraries := make([]mediaserver.Library, 0, len(info.LibraryMap))
	for id, name := range info.LibraryMap {
		libraries = append(libraries, mediaserver.Library{ID: id, Name: name, Type: "book"})
	}
	sort.Slice(libraries, func(i, j int) bool { return libraries[i].ID < libraries[j].ID })
	return libraries, nil
}
