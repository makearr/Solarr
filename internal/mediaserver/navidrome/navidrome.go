// Package navidrome is the client for a Navidrome server over the Subsonic API; the password is never sent, only a salted hash.
package navidrome

import (
	"context"
	"crypto/md5" //nolint:gosec // Subsonic token authentication is defined as md5(password + salt)
	"encoding/hex"
	"net/http"
	"net/url"
	"strconv"

	"github.com/makearr/Solarr/internal/mediaserver"
)

const apiVersion = "1.16.1"

// Client talks to one Navidrome server.
type Client struct {
	doer     mediaserver.Doer
	base     string
	username string
	password string
	salt     func() string
}

// New builds a client; salt answers a fresh random salt for every request (a test fixes it).
func New(doer mediaserver.Doer, base, username, password string, salt func() string) *Client {
	return &Client{doer: doer, base: base, username: username, password: password, salt: salt}
}

func (c *Client) address(method string, extra url.Values) string {
	salt := c.salt()
	sum := md5.Sum([]byte(c.password + salt)) //nolint:gosec // see import
	query := url.Values{"u": {c.username}, "t": {hex.EncodeToString(sum[:])}, "s": {salt}, "v": {apiVersion}, "c": {"solarr"}, "f": {"json"}}
	for name, values := range extra {
		query[name] = values
	}
	return mediaserver.Join(c.base, "/rest/"+method) + mediaserver.Query(query)
}

type answer struct {
	Status        string `json:"status"`
	ServerVersion string `json:"serverVersion"`
	Type          string `json:"type"`
	Error         struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	ScanStatus struct {
		Scanning bool `json:"scanning"`
	} `json:"scanStatus"`
	MusicFolders struct {
		MusicFolder []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"musicFolder"`
	} `json:"musicFolders"`
}

func (c *Client) call(ctx context.Context, method string, extra url.Values) (answer, error) {
	var wrapper struct {
		Response answer `json:"subsonic-response"`
	}
	if err := mediaserver.Call(ctx, c.doer, http.MethodGet, c.address(method, extra), nil, nil, &wrapper); err != nil {
		return answer{}, err
	}
	if wrapper.Response.Status != "ok" {
		if wrapper.Response.Error.Code == 40 || wrapper.Response.Error.Code == 41 {
			return answer{}, mediaserver.ErrRejected
		}
		return answer{}, &SubsonicError{Code: wrapper.Response.Error.Code, Message: wrapper.Response.Error.Message}
	}
	return wrapper.Response, nil
}

// SubsonicError is an error the server answered inside a 200.
type SubsonicError struct {
	Code    int
	Message string
}

func (e *SubsonicError) Error() string { return "navidrome: " + e.Message }

// Test pings the server; Subsonic has no administrator notion in the answer, so Admin stays false.
func (c *Client) Test(ctx context.Context) (mediaserver.Info, error) {
	reply, err := c.call(ctx, "ping", nil)
	if err != nil {
		return mediaserver.Info{}, err
	}
	return mediaserver.Info{Name: "Navidrome", Version: reply.ServerVersion}, nil
}

// Libraries lists the music folders the account may see.
func (c *Client) Libraries(ctx context.Context) ([]mediaserver.Library, error) {
	reply, err := c.call(ctx, "getMusicFolders", nil)
	if err != nil {
		return nil, err
	}
	libraries := make([]mediaserver.Library, len(reply.MusicFolders.MusicFolder))
	for index, folder := range reply.MusicFolders.MusicFolder {
		libraries[index] = mediaserver.Library{ID: strconv.Itoa(folder.ID), Name: folder.Name, Type: "music"}
	}
	return libraries, nil
}

// Changed starts one incremental scan; the server cannot scan one folder, and a running scan is left to finish.
func (c *Client) Changed(ctx context.Context, _ []mediaserver.Change) error {
	status, err := c.call(ctx, "getScanStatus", nil)
	if err != nil {
		return err
	}
	if status.ScanStatus.Scanning {
		return mediaserver.ErrBusy
	}
	_, err = c.call(ctx, "startScan", url.Values{"fullScan": {"false"}})
	return err
}
