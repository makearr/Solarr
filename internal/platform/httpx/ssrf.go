// Package httpx is the outbound HTTP seam (Invariant 28, ADR-13).
package httpx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// BlockReason names why an address was refused. It is a value rather than a
// formatted string so the API can report it and the UI can translate it.
type BlockReason string

// Why an address is not on the public internet. Each maps to a user-facing
// explanation; none of them is ever a transient condition worth retrying.
const (
	ReasonLoopback      BlockReason = "loopback"
	ReasonLinkLocal     BlockReason = "link_local"
	ReasonPrivate       BlockReason = "private"
	ReasonMulticast     BlockReason = "multicast"
	ReasonUnspecified   BlockReason = "unspecified"
	ReasonSharedAddress BlockReason = "shared_address_space"
	ReasonReserved      BlockReason = "reserved"
	ReasonUnroutable    BlockReason = "unroutable"
)

var (
	// ErrBlockedAddress reports a destination the guard refuses to contact.
	ErrBlockedAddress = errors.New("httpx: address blocked")

	// ErrBlockedScheme reports a URL scheme other than http or https.
	ErrBlockedScheme = errors.New("httpx: scheme blocked")

	// ErrNoAllowedAddress reports a hostname whose every resolved address was blocked.
	ErrNoAllowedAddress = errors.New("httpx: hostname resolved only to blocked addresses")
)

// BlockedError carries the address and the reason it was refused.
type BlockedError struct {
	Host    string
	Address netip.Addr
	Reason  BlockReason
}

func (e *BlockedError) Error() string {
	return fmt.Sprintf("httpx: %s resolves to %s, blocked as %s", e.Host, e.Address, e.Reason)
}

func (e *BlockedError) Unwrap() error { return ErrBlockedAddress }

// blockedPrefixes covers the ranges netip's own predicates miss.
var blockedPrefixes = []struct {
	prefix netip.Prefix
	reason BlockReason
}{
	{netip.MustParsePrefix("0.0.0.0/8"), ReasonUnspecified},
	{netip.MustParsePrefix("100.64.0.0/10"), ReasonSharedAddress}, // carrier-grade NAT
	{netip.MustParsePrefix("192.0.0.0/24"), ReasonReserved},       // IETF protocol assignments
	{netip.MustParsePrefix("192.0.2.0/24"), ReasonReserved},       // TEST-NET-1
	{netip.MustParsePrefix("198.18.0.0/15"), ReasonReserved},      // benchmarking
	{netip.MustParsePrefix("198.51.100.0/24"), ReasonReserved},    // TEST-NET-2
	{netip.MustParsePrefix("203.0.113.0/24"), ReasonReserved},     // TEST-NET-3
	{netip.MustParsePrefix("240.0.0.0/4"), ReasonReserved},        // reserved, incl. broadcast
	{netip.MustParsePrefix("64:ff9b::/96"), ReasonReserved},       // NAT64, a route to IPv4 privates
	{netip.MustParsePrefix("100::/64"), ReasonUnroutable},         // discard-only
	{netip.MustParsePrefix("2001:db8::/32"), ReasonReserved},      // documentation
}

// ClassifyAddress reports why an address must not be contacted, or an empty reason if it may
// be.
func ClassifyAddress(address netip.Addr) BlockReason {
	address = address.Unmap()

	switch {
	case address.IsUnspecified():
		return ReasonUnspecified
	case address.IsLoopback():
		return ReasonLoopback
	case address.IsMulticast(), address.IsInterfaceLocalMulticast():
		// Before the link-local check, because link-local multicast is both and
		// multicast is the more useful thing to tell the user.
		return ReasonMulticast
	case address.IsLinkLocalUnicast():
		// 169.254.169.254 is the cloud metadata endpoint and the reason this
		// whole guard exists.
		return ReasonLinkLocal
	case address.IsPrivate():
		return ReasonPrivate
	}

	for _, blocked := range blockedPrefixes {
		if blocked.prefix.Contains(address) {
			return blocked.reason
		}
	}
	return ""
}

// Resolver looks a hostname up. Injected so the guard is testable without DNS.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// DialFunc opens a connection to an already-validated address.
type DialFunc func(ctx context.Context, network, address string) (net.Conn, error)

// Guard refuses outbound connections to addresses that are not on the public internet (ADR-14).
type Guard struct {
	resolver Resolver
	dial     DialFunc
	// allowedHosts is the opt-out for indexers that genuinely live on the LAN.
	// It names hosts rather than disabling the check, and is empty by default.
	allowedHosts map[string]struct{}
}

// NewGuard returns a guard. allowedHosts are exact, case-insensitive hostnames
// exempted from address classification; passing none is the default posture.
//
// A nil resolver or dialer means the system's. They were once taken literally,
// and the nil then surfaced as a segfault inside a background dial -- which
// takes the process with it, far from the call that caused it.
func NewGuard(resolver Resolver, dial DialFunc, allowedHosts []string) *Guard {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	if dial == nil {
		dial = (&net.Dialer{Timeout: defaultDialTimeout, KeepAlive: defaultKeepAlive}).DialContext
	}
	allowed := make(map[string]struct{}, len(allowedHosts))
	for _, host := range allowedHosts {
		allowed[strings.ToLower(host)] = struct{}{}
	}
	return &Guard{resolver: resolver, dial: dial, allowedHosts: allowed}
}

// Bounds for the default dialer, applied only when one is not supplied.
const (
	defaultDialTimeout = 10 * time.Second
	defaultKeepAlive   = 30 * time.Second
)

// CheckURL rejects a scheme the client will not speak.
func (g *Guard) CheckURL(target *url.URL) error {
	switch target.Scheme {
	case "http", "https":
		return nil
	default:
		return fmt.Errorf("%w: %q is not http or https", ErrBlockedScheme, target.Scheme)
	}
}

// DialContext resolves address, refuses every result that is not publicly
// routable, and connects to the first survivor that answers.
func (g *Guard) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("httpx: split %q: %w", address, err)
	}
	if _, exempt := g.allowedHosts[strings.ToLower(host)]; exempt {
		return g.dial(ctx, network, address)
	}

	candidates, err := g.resolve(ctx, network, host)
	if err != nil {
		return nil, err
	}
	allowed, err := allAllowed(host, candidates)
	if err != nil {
		return nil, err
	}

	// Dial only the literal addresses just validated, never the hostname: re-resolving is the
	// rebinding window. Every one is tried, so a host whose IPv6 is broken still reaches IPv4.
	var failures []error
	for index, address := range interleaveFamilies(allowed) {
		attempt, cancel := ctx, context.CancelFunc(func() {})
		if index < len(allowed)-1 {
			attempt, cancel = context.WithTimeout(ctx, perAddressDialTimeout)
		}
		conn, err := g.dial(attempt, network, net.JoinHostPort(address.String(), port))
		cancel()
		if err == nil {
			return conn, nil
		}
		failures = append(failures, err)
		if ctx.Err() != nil {
			break
		}
	}
	return nil, fmt.Errorf("httpx: dial %s: %w", host, errors.Join(failures...))
}

// perAddressDialTimeout bounds every attempt but the last, so a black-holed address cannot
// spend the whole request's timeout before the next address is tried.
const perAddressDialTimeout = 3 * time.Second

// interleaveFamilies alternates address families starting with the resolver's first choice,
// so one unreachable family costs a single attempt rather than all of its addresses.
func interleaveFamilies(addresses []netip.Addr) []netip.Addr {
	var first, second []netip.Addr
	for _, address := range addresses {
		if address.Is4() == addresses[0].Is4() {
			first = append(first, address)
		} else {
			second = append(second, address)
		}
	}
	ordered := make([]netip.Addr, 0, len(addresses))
	for i := 0; i < len(first) || i < len(second); i++ {
		if i < len(first) {
			ordered = append(ordered, first[i])
		}
		if i < len(second) {
			ordered = append(ordered, second[i])
		}
	}
	return ordered
}

// DialUnchecked connects without classifying address. Its one caller is
// dialing this process's own configured proxy (ADR-47/58, Invariant 65).
func (g *Guard) DialUnchecked(ctx context.Context, network, address string) (net.Conn, error) {
	return g.dial(ctx, network, address)
}

// CheckDestination resolves and classifies host without dialing -- what a
// proxied request needs, since DialContext then only ever dials the proxy.
func (g *Guard) CheckDestination(ctx context.Context, host string) error {
	if _, exempt := g.allowedHosts[strings.ToLower(host)]; exempt {
		return nil
	}
	candidates, err := g.resolve(ctx, "ip", host)
	if err != nil {
		return err
	}
	_, err = allAllowed(host, candidates)
	return err
}

// allAllowed returns every unblocked candidate in resolver order, or the last reason none was.
func allAllowed(host string, candidates []netip.Addr) ([]netip.Addr, error) {
	var allowed []netip.Addr
	var lastBlocked error
	for _, candidate := range candidates {
		if reason := ClassifyAddress(candidate); reason != "" {
			lastBlocked = &BlockedError{Host: host, Address: candidate, Reason: reason}
			continue
		}
		allowed = append(allowed, candidate)
	}
	switch {
	case len(allowed) > 0:
		return allowed, nil
	case lastBlocked != nil:
		return nil, lastBlocked
	}
	return nil, fmt.Errorf("%w: %s", ErrNoAllowedAddress, host)
}

func (g *Guard) resolve(ctx context.Context, network, host string) ([]netip.Addr, error) {
	if literal, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{literal}, nil
	}
	candidates, err := g.resolver.LookupNetIP(ctx, lookupNetwork(network), host)
	if err != nil {
		return nil, fmt.Errorf("httpx: resolve %q: %w", host, err)
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNoAllowedAddress, host)
	}
	return candidates, nil
}

// lookupNetwork maps a dial network onto the one LookupNetIP understands.
func lookupNetwork(network string) string {
	switch network {
	case "tcp4", "udp4":
		return "ip4"
	case "tcp6", "udp6":
		return "ip6"
	default:
		return "ip"
	}
}
