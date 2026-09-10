package httpx

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/makearr/MediaNexus/internal/platform/clock"
)

// defaultTimeout bounds a single request. Indexer searches are the slow case and
// ADR-37's group deadline sits above this.
const defaultTimeout = 30 * time.Second

// defaultMaxRedirects is deliberately low. Every hop is re-validated, and a long
// redirect chain from a user-supplied URL is a probe, not a feature.
const defaultMaxRedirects = 5

// sourceURL is where the corresponding source lives (AGPL §13).
const sourceURL = "https://github.com/makearr/MediaNexus"

// UserAgent builds the identifying header. MusicBrainz blocks generic agents, so
// this is a functional requirement rather than politeness.
func UserAgent(version string) string {
	return fmt.Sprintf("MediaNexus/%s ( %s )", version, sourceURL)
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
}

// Client is the only way out of the process (Invariant 28). One per provider,
// sharing the limiter and breaker registries.
type Client struct {
	provider  string
	userAgent string
	timeout   time.Duration
	inner     *http.Client
	limiter   *Limiter
	breaker   *Breaker
	guard     *Guard
	clock     clock.Clock
	// proxy nil means this client never proxies (see checkProxiedDestination).
	proxy LiveProxyPolicy
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
		provider:  config.Provider,
		userAgent: config.UserAgent,
		timeout:   timeout,
		limiter:   limiter,
		breaker:   breaker,
		guard:     guard,
		clock:     c,
		proxy:     config.Proxy,
		inner: &http.Client{
			Transport: transport,
			CheckRedirect: func(request *http.Request, via []*http.Request) error {
				if len(via) >= maxRedirects {
					return fmt.Errorf("httpx: stopped after %d redirects", maxRedirects)
				}
				// The address is re-checked by the dialer on every new
				// connection; the scheme is not, so it is checked here.
				return guard.CheckURL(request.URL)
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

// Do sends a request through the guard, the breaker and the rate limiter (§11.3).
func (c *Client) Do(ctx context.Context, request *http.Request) (*http.Response, error) {
	if err := c.guard.CheckURL(request.URL); err != nil {
		return nil, err
	}
	if err := c.checkProxiedDestination(ctx, request); err != nil {
		return nil, err
	}
	if err := c.breaker.Allow(c.provider); err != nil {
		return nil, err
	}

	release, err := c.limiter.Acquire(ctx, c.provider)
	if err != nil {
		// The request never happened, so it is not evidence about the provider.
		// Reporting it would let a cancelled shutdown open every circuit.
		c.breaker.Observe(c.provider, true)
		return nil, err
	}
	defer release()

	timedOut, cancel := context.WithTimeout(ctx, c.timeout)

	if c.userAgent != "" && request.Header.Get("User-Agent") == "" {
		request.Header.Set("User-Agent", c.userAgent)
	}

	response, err := c.inner.Do(request.WithContext(timedOut))
	if err != nil {
		cancel()
		c.limiter.Observe(c.provider, Outcome{Success: false})
		c.breaker.Observe(c.provider, false)
		return nil, fmt.Errorf("httpx: %s %s: %w", request.Method, request.URL.Redacted(), err)
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
		URL:        request.URL.Redacted(),
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

// checkProxiedDestination validates the destination directly when this
// client proxies at all, since a proxied dial never reaches it otherwise.
func (c *Client) checkProxiedDestination(ctx context.Context, request *http.Request) error {
	if c.proxy == nil {
		return nil
	}
	return c.guard.CheckDestination(ctx, request.URL.Hostname())
}

// Get is the common case.
func (c *Client) Get(ctx context.Context, url string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("httpx: build request: %w", err)
	}
	return c.Do(ctx, request)
}

// Provider reports the bucket key this client shares with its siblings.
func (c *Client) Provider() string { return c.provider }
