package mediaserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// Doer is the one method of httpx.Client the clients need; every call goes through the SSRF guard there.
type Doer interface {
	Do(ctx context.Context, request *http.Request) (*http.Response, error)
}

// MaxAnswerBytes bounds every answer read from a server.
const MaxAnswerBytes = 8 << 20

// Call sends one request and decodes a JSON answer into into (nil ignores the body).
func Call(ctx context.Context, doer Doer, method, address string, header http.Header, body any, into any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("mediaserver: encode body: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, address, reader)
	if err != nil {
		return fmt.Errorf("mediaserver: build request: %w", err)
	}
	for name, values := range header {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Accept", "application/json")
	response, err := doer.Do(ctx, request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if into == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, MaxAnswerBytes))
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, MaxAnswerBytes+1))
	if err != nil {
		return fmt.Errorf("mediaserver: read answer: %w", err)
	}
	if len(data) > MaxAnswerBytes {
		return fmt.Errorf("mediaserver: answer exceeds %d bytes", MaxAnswerBytes)
	}
	if err := json.Unmarshal(data, into); err != nil {
		return fmt.Errorf("mediaserver: decode answer: %w", err)
	}
	return nil
}

// Join appends path to a base address, keeping a base path (a server behind a reverse proxy prefix).
func Join(base, path string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
}

// Query encodes parameters for an address.
func Query(values url.Values) string {
	if len(values) == 0 {
		return ""
	}
	return "?" + values.Encode()
}

// DoerFunc lets a function stand in for httpx.Client, for a test or a wrapper.
type DoerFunc func(ctx context.Context, request *http.Request) (*http.Response, error)

// Do calls f.
func (f DoerFunc) Do(ctx context.Context, request *http.Request) (*http.Response, error) {
	return f(ctx, request)
}

// Refused maps the status of a refused request onto the package's errors: 401 means the credential, 403 means its rights.
func Refused(err error) error {
	var status *httpx.StatusError
	if errors.As(err, &status) {
		switch status.Status {
		case http.StatusUnauthorized:
			return fmt.Errorf("%w: %w", ErrRejected, err)
		case http.StatusForbidden:
			return fmt.Errorf("%w: %w", ErrNotAdmin, err)
		}
	}
	return err
}
