package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
)

// defaultTimeout bounds a single request. Indexer searches are the slow case and
// ADR-37's group deadline sits above this.
const defaultTimeout = 30 * time.Second

// defaultMaxRedirects is deliberately low. Every hop is re-validated, and a long
// redirect chain from a user-supplied URL is a probe, not a feature.
const defaultMaxRedirects = 5

// sourceURL is where the corresponding source lives (AGPL §13).
const sourceURL = "https://github.com/makearr/Solarr"

// UserAgent builds the identifying header. MusicBrainz blocks generic agents, so
// this is a functional requirement rather than politeness.
func UserAgent(version string) string {
	return fmt.Sprintf("Solarr/%s ( %s )", version, sourceURL)
}

// ClientConfig describes one provider's client.
type ClientConfig struct {
	// Provider is the rate-limit and breaker bucket key. Every subsystem talking
	// to the same provider must use the same value, or ADR-13 is defeated.
	Provider     string
	UserAgent    string
	Timeout      time.Duration
	MaxRedirects int
	// Transport is injected in tests. When nil the guard's dialer is used, which
	// is what makes the SSRF check unavoidable in production.
	Transport http.RoundTripper
	// Proxy is ADR-58's global egress proxy, read live on every request. Nil
	// means this client never proxies -- the common case for a test.
	Proxy LiveProxyPolicy
	// LiveUserAgent, when set, is read on every request and replaces UserAgent.
	LiveUserAgent func() string
}

// Client is the only way out of the process (Invariant 28). One per provider,
// sharing the limiter and breaker registries.
type Client struct {
	provider      string
	userAgent     string
	liveUserAgent func() string
	timeout       time.Duration
	inner         *http.Client
	limiter       *Limiter
	breaker       *Breaker
	guard         *Guard
	clock         clock.Clock
	// proxy nil means this client never proxies (see checkProxiedDestination).
	proxy LiveProxyPolicy
	// routedBy is the proxy policy the pooled connections were opened under.
	routedBy atomic.Value
}

// NewClient builds a client for one provider.
func NewClient(c clock.Clock, guard *Guard, limiter *Limiter, breaker *Breaker, config ClientConfig) *Client {
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	maxRedirects := config.MaxRedirects
	if maxRedirects <= 0 {
		maxRedirects = defaultMaxRedirects
	}

	transport := config.Transport
	if transport == nil {
		transport = NewTransport(guard, config.Proxy)
	}

	return &Client{
		provider:      config.Provider,
		userAgent:     config.UserAgent,
		liveUserAgent: config.LiveUserAgent,
		timeout:       timeout,
		limiter:       limiter,
		breaker:       breaker,
		guard:         guard,
		clock:         c,
		proxy:         config.Proxy,
		inner: &http.Client{
			Transport: transport,
			CheckRedirect: func(request *http.Request, via []*http.Request) error {
				// An indexer answers a torrent download with a redirect to a magnet link: not a page to fetch.
				if request.URL.Scheme == "magnet" {
					return &MagnetRedirect{Link: request.URL.String()}
				}
				if len(via) >= maxRedirects {
					return fmt.Errorf("httpx: stopped after %d redirects", maxRedirects)
				}
				if err := guard.CheckURL(request.URL); err != nil {
					return err
				}
				return checkProxiedDestination(request.Context(), guard, config.Proxy, request)
			},
		},
	}
}

// NewTransport builds a transport that can only reach publicly routable
// addresses. proxy is nil for a transport that never proxies.
func NewTransport(guard *Guard, proxy LiveProxyPolicy) *http.Transport {
	transport := &http.Transport{
		DialContext:           guard.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	if proxy != nil {
		transport.Proxy = ProxyFunc(proxy)
		transport.DialContext = ProxyDial(guard, proxy)
	}
	return transport
}

// MagnetRedirect is a server answering with a redirect to a magnet link, which is the release itself rather than an
// error; the caller takes Link as what it asked to download.
type MagnetRedirect struct{ Link string }

func (e *MagnetRedirect) Error() string { return "httpx: redirected to a magnet link" }

// Do sends a request through the guard, the breaker and the rate limiter (§11.3).
func (c *Client) Do(ctx context.Context, request *http.Request) (*http.Response, error) {
	if err := c.guard.CheckURL(request.URL); err != nil {
		return nil, err
	}
	if err := c.checkProxiedDestination(ctx, request); err != nil {
		return nil, err
	}
	c.dropConnectionsOnProxyChange()
	if err := c.breaker.Allow(c.provider); err != nil {
		return nil, err
	}

	release, err := c.limiter.Acquire(ctx, c.provider)
	if err != nil {
		// The request never happened, so it is not evidence about the provider either way:
		// failure would let a cancelled shutdown open every circuit, success would close one still down.
		c.breaker.Abandon(c.provider)
		return nil, err
	}
	defer release()

	timedOut, cancel := context.WithTimeout(ctx, c.timeout)

	userAgent := c.userAgent
	if c.liveUserAgent != nil {
		userAgent = c.liveUserAgent()
	}
	if userAgent != "" && request.Header.Get("User-Agent") == "" {
		request.Header.Set("User-Agent", userAgent)
	}

	response, err := c.inner.Do(request.WithContext(timedOut))
	var magnet *MagnetRedirect
	if errors.As(err, &magnet) {
		cancel()
		c.limiter.Observe(c.provider, Outcome{Success: true})
		c.breaker.Observe(c.provider, true)
		return nil, magnet
	}
	if err != nil {
		cancel()
		// The caller gave up (shutdown, a closed page): says nothing of the provider; the request's own timeout still counts.
		if ctx.Err() != nil {
			c.breaker.Abandon(c.provider)
		} else {
			c.limiter.Observe(c.provider, Outcome{Success: false})
			c.breaker.Observe(c.provider, false)
		}
		redacted := RedactURL(request.URL)
		return nil, fmt.Errorf("httpx: %s %s: %w", request.Method, redacted,
			redactRequestError(err, redacted))
	}

	class := ClassifyStatus(response.StatusCode)
	retryAfter := ParseRetryAfter(response.Header.Get("Retry-After"), c.clock.Now())
	c.limiter.Observe(c.provider, Outcome{Success: class != ClassRetryable, RetryAfter: retryAfter})
	// A terminal status says our request was wrong, not that the provider is
	// unhealthy, so it must not count towards the breaker.
	c.breaker.Observe(c.provider, class != ClassRetryable)

	if class == ClassSuccess {
		// cancel is deferred to the body's own Close, not run here: a caller
		// reads the body after Do returns, and cancelling on return raced
		// that read against net/http noticing the context was done --
		// an intermittent "context canceled" on any slow or larger response
		// (found live, todo 4.17's Discover search hit it against a real
		// MusicBrainz search response).
		response.Body = &cancelOnCloseBody{ReadCloser: response.Body, cancel: cancel}
		return response, nil
	}
	// The body of an error response is discarded; the status is the whole
	// signal, and a failed close on it tells the caller nothing useful.
	cancel()
	_ = response.Body.Close()
	return nil, &StatusError{
		Provider:   c.provider,
		URL:        RedactURL(request.URL),
		Status:     response.StatusCode,
		Class:      class,
		RetryAfter: retryAfter,
	}
}

// cancelOnCloseBody ties a Do call's own request-scoped timeout to the
// response body's lifetime instead of Do's own stack frame -- see the
// comment where this is constructed.
type cancelOnCloseBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelOnCloseBody) Close() error {
	defer b.cancel()
	return b.ReadCloser.Close()
}

// dropConnectionsOnProxyChange closes pooled connections once the proxy policy changes: HTTP/2
// pools by destination, not by route, so an open connection kept its old route until idle
// (found 2026-09-24 against a real SOCKS5 proxy).
func (c *Client) dropConnectionsOnProxyChange() {
	if c.proxy == nil {
		return
	}
	current := c.proxy()
	route := fmt.Sprint(current.URL, current.Bypass)
	if previous := c.routedBy.Swap(route); previous != nil && previous != route {
		c.inner.CloseIdleConnections()
	}
}

// checkProxiedDestination validates the destination directly when this
// client proxies at all, since a proxied dial never reaches it otherwise.
func (c *Client) checkProxiedDestination(ctx context.Context, request *http.Request) error {
	return checkProxiedDestination(ctx, c.guard, c.proxy, request)
}

func checkProxiedDestination(ctx context.Context, guard *Guard, proxy LiveProxyPolicy, request *http.Request) error {
	if proxy == nil {
		return nil
	}
	// A direct request's dial classifies the address it connects to; resolving here too cost a lookup per request.
	if current := proxy(); current.URL == nil || bypassed(request.URL.Hostname(), current.Bypass) {
		return nil
	}
	return guard.CheckDestination(ctx, request.URL.Hostname())
}

// Get is the common case.
func (c *Client) Get(ctx context.Context, url string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("httpx: build request: %w", err)
	}
	return c.Do(ctx, request)
}

// ErrNotFound is a 404 GetJSON met: the provider knows nothing under what was asked.
var ErrNotFound = errors.New("httpx: not found")

// GetJSON fetches a URL and decodes its JSON body, read up to limit bytes; a 404 answers ErrNotFound.
func (c *Client) GetJSON(ctx context.Context, address string, limit int64, into any) error {
	response, err := c.Get(ctx, address)
	if err != nil {
		var refused *StatusError
		if errors.As(err, &refused) && refused.Status == http.StatusNotFound {
			return ErrNotFound
		}
		return err
	}
	defer func() { _ = response.Body.Close() }()
	shown := address
	if parsed, err := url.Parse(address); err == nil {
		shown = RedactURL(parsed)
	}
	body, err := ReadLimited(response.Body, limit)
	if err != nil {
		return fmt.Errorf("httpx: read %s: %w", shown, err)
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("httpx: decode %s: %w", shown, err)
	}
	return nil
}

// Provider reports the bucket key this client shares with its siblings.
func (c *Client) Provider() string { return c.provider }
