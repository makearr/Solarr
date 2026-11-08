// Package flaresolverr gets an indexer's requests past a Cloudflare challenge through a FlareSolverr the user runs.
package flaresolverr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// maxSolutionBytes bounds the page FlareSolverr hands back.
const maxSolutionBytes = 8 << 20

// solveTimeout is how long FlareSolverr may work on one challenge.
const solveTimeout = 60 * time.Second

// Transport sends a request direct, and when the site answers with a challenge asks FlareSolverr to solve it, remembers
// the clearance cookies and user agent it earned, and repeats the request direct with them.
type Transport struct {
	// Direct reaches the site itself, through the SSRF guard.
	Direct http.RoundTripper
	// Solver reaches FlareSolverr, whose own address the guard has been told to allow.
	Solver http.RoundTripper
	// SolverURL is FlareSolverr's address; /v1 is added when missing.
	SolverURL string
	// CheckDestination refuses a site the guard would not let Direct dial, before FlareSolverr is asked to visit it.
	CheckDestination func(ctx context.Context, host string) error

	mutex     sync.Mutex
	clearance map[string]clearance
}

type clearance struct {
	cookies   []*http.Cookie
	userAgent string
}

// RoundTrip implements http.RoundTripper.
func (t *Transport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method != http.MethodGet {
		return t.Direct.RoundTrip(request)
	}
	host := request.URL.Host
	response, err := t.Direct.RoundTrip(t.withClearance(request, host))
	if err != nil || !challenged(response) {
		return response, err
	}
	_ = response.Body.Close()

	if t.CheckDestination != nil {
		if err := t.CheckDestination(request.Context(), request.URL.Hostname()); err != nil {
			return nil, err
		}
	}
	solution, err := t.solve(request.Context(), request.URL.String())
	if err != nil {
		return nil, err
	}
	t.mutex.Lock()
	if t.clearance == nil {
		t.clearance = map[string]clearance{}
	}
	t.clearance[host] = clearance{cookies: solution.cookies(), userAgent: solution.UserAgent}
	t.mutex.Unlock()

	// With the clearance the site answers the original request itself, binary downloads included.
	again, err := t.Direct.RoundTrip(t.withClearance(request, host))
	if err == nil && !challenged(again) {
		return again, nil
	}
	if err == nil {
		_ = again.Body.Close()
	}
	return &http.Response{
		Status: fmt.Sprintf("%d %s", solution.Status, http.StatusText(solution.Status)), StatusCode: solution.Status,
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1, Header: http.Header{"Content-Type": {"text/html; charset=utf-8"}},
		Body: io.NopCloser(strings.NewReader(solution.Response)), Request: request,
	}, nil
}

func (t *Transport) withClearance(request *http.Request, host string) *http.Request {
	t.mutex.Lock()
	held, ok := t.clearance[host]
	t.mutex.Unlock()
	if !ok {
		return request
	}
	clone := request.Clone(request.Context())
	for _, cookie := range held.cookies {
		clone.AddCookie(cookie)
	}
	if held.userAgent != "" {
		clone.Header.Set("User-Agent", held.userAgent)
	}
	return clone
}

// challenged reports an answer that is Cloudflare's or DDoS-Guard's challenge page rather than the site's.
func challenged(response *http.Response) bool {
	if response.StatusCode != http.StatusForbidden && response.StatusCode != http.StatusServiceUnavailable {
		return false
	}
	server := strings.ToLower(response.Header.Get("Server"))
	return response.Header.Get("Cf-Mitigated") == "challenge" || strings.Contains(server, "cloudflare") || strings.Contains(server, "ddos-guard")
}

type solution struct {
	Status    int    `json:"status"`
	Response  string `json:"response"`
	UserAgent string `json:"userAgent"`
	Cookie    []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"cookies"`
}

func (s solution) cookies() []*http.Cookie {
	cookies := make([]*http.Cookie, len(s.Cookie))
	for index, one := range s.Cookie {
		cookies[index] = &http.Cookie{Name: one.Name, Value: one.Value}
	}
	return cookies
}

func (t *Transport) solve(ctx context.Context, target string) (solution, error) {
	body, err := json.Marshal(map[string]any{"cmd": "request.get", "url": target, "maxTimeout": solveTimeout.Milliseconds()})
	if err != nil {
		return solution{}, err
	}
	address := strings.TrimRight(t.SolverURL, "/")
	if !strings.HasSuffix(address, "/v1") {
		address += "/v1"
	}
	ctx, cancel := context.WithTimeout(ctx, solveTimeout+10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address, bytes.NewReader(body))
	if err != nil {
		return solution{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := t.Solver.RoundTrip(request)
	if err != nil {
		return solution{}, fmt.Errorf("flaresolverr: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxSolutionBytes))
	if err != nil {
		return solution{}, fmt.Errorf("flaresolverr: read: %w", err)
	}
	var answer struct {
		Status   string   `json:"status"`
		Message  string   `json:"message"`
		Solution solution `json:"solution"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return solution{}, fmt.Errorf("flaresolverr: not a FlareSolverr answer: %w", err)
	}
	if answer.Status != "ok" {
		return solution{}, fmt.Errorf("flaresolverr: %s", answer.Message)
	}
	return answer.Solution, nil
}
