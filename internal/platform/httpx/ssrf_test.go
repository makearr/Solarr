package httpx_test

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"github.com/makearr/MediaNexus/internal/platform/httpx"
)

func TestClassifyAddress(t *testing.T) {
	cases := []struct {
		address string
		want    httpx.BlockReason
	}{
		{"93.184.216.34", ""},
		{"1.1.1.1", ""},
		{"2606:2800:220:1:248:1893:25c8:1946", ""},

		{"127.0.0.1", httpx.ReasonLoopback},
		{"127.1.2.3", httpx.ReasonLoopback},
		{"::1", httpx.ReasonLoopback},
		// An IPv4-mapped address must be unmapped first or it walks past every
		// IPv4 check below it. This is the classic bypass.
		{"::ffff:127.0.0.1", httpx.ReasonLoopback},
		{"::ffff:10.0.0.1", httpx.ReasonPrivate},
		{"::ffff:169.254.169.254", httpx.ReasonLinkLocal},
		// netip's own predicates unmap for themselves, but Prefix.Contains does not match a 4-in-6
		// address against an IPv4 prefix.
		{"::ffff:100.64.0.1", httpx.ReasonSharedAddress},
		{"::ffff:192.0.2.1", httpx.ReasonReserved},
		{"::ffff:240.0.0.1", httpx.ReasonReserved},

		// The cloud metadata endpoint, named in ADR-14.
		{"169.254.169.254", httpx.ReasonLinkLocal},
		{"fe80::1", httpx.ReasonLinkLocal},

		{"10.0.0.1", httpx.ReasonPrivate},
		{"172.16.0.1", httpx.ReasonPrivate},
		{"172.31.255.254", httpx.ReasonPrivate},
		{"192.168.1.1", httpx.ReasonPrivate},
		{"fd00::1", httpx.ReasonPrivate},

		{"0.0.0.0", httpx.ReasonUnspecified},
		{"0.1.2.3", httpx.ReasonUnspecified},
		{"::", httpx.ReasonUnspecified},

		{"224.0.0.1", httpx.ReasonMulticast},
		{"ff02::1", httpx.ReasonMulticast},

		{"100.64.0.1", httpx.ReasonSharedAddress},
		{"192.0.2.1", httpx.ReasonReserved},
		{"198.18.0.1", httpx.ReasonReserved},
		{"203.0.113.1", httpx.ReasonReserved},
		{"255.255.255.255", httpx.ReasonReserved},
		{"64:ff9b::7f00:1", httpx.ReasonReserved},
		{"2001:db8::1", httpx.ReasonReserved},

		// 172.32/16 is public despite sitting just past the private block.
		{"172.32.0.1", ""},
	}

	for _, testCase := range cases {
		t.Run(testCase.address, func(t *testing.T) {
			address := netip.MustParseAddr(testCase.address)
			if got := httpx.ClassifyAddress(address); got != testCase.want {
				t.Errorf("ClassifyAddress(%s) = %q, want %q", testCase.address, got, testCase.want)
			}
		})
	}
}

func TestGuard_CheckURLRejectsNonHTTPSchemes(t *testing.T) {
	guard := httpx.NewGuard(fakeResolver{}, refusingDial, nil)

	for _, raw := range []string{"file:///etc/passwd", "gopher://host/", "ftp://host/", "data:text/plain,x"} {
		target, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		if err := guard.CheckURL(target); !errors.Is(err, httpx.ErrBlockedScheme) {
			t.Errorf("CheckURL(%q) = %v, want ErrBlockedScheme", raw, err)
		}
	}

	for _, raw := range []string{"http://example.com/", "https://example.com:9117/api"} {
		target, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		if err := guard.CheckURL(target); err != nil {
			t.Errorf("CheckURL(%q) = %v, want nil", raw, err)
		}
	}
}

func TestGuard_BlocksAHostnameResolvingToAPrivateAddress(t *testing.T) {
	resolver := fakeResolver{"indexer.invalid": {netip.MustParseAddr("192.168.1.50")}}
	guard := httpx.NewGuard(resolver, refusingDial, nil)

	_, err := guard.DialContext(t.Context(), "tcp", "indexer.invalid:80")

	var blocked *httpx.BlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("DialContext = %v, want a BlockedError", err)
	}
	if blocked.Reason != httpx.ReasonPrivate {
		t.Errorf("Reason = %q, want %q", blocked.Reason, httpx.ReasonPrivate)
	}
	if !errors.Is(err, httpx.ErrBlockedAddress) {
		t.Error("BlockedError does not unwrap to ErrBlockedAddress")
	}
}

// The rebinding defence: the connection must go to the literal address that was validated,
// never back to the hostname, or DNS can answer differently the second time.
func TestGuard_DialsTheValidatedAddressNotTheHostname(t *testing.T) {
	resolver := fakeResolver{"indexer.invalid": {netip.MustParseAddr("93.184.216.34")}}
	var dialled string
	guard := httpx.NewGuard(resolver, recordingDial(&dialled), nil)

	if _, err := guard.DialContext(t.Context(), "tcp", "indexer.invalid:443"); err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	if want := "93.184.216.34:443"; dialled != want {
		t.Errorf("dialled %q, want %q — re-resolving here is the rebinding window", dialled, want)
	}
}

func TestGuard_SkipsABlockedAddressAndUsesTheNextAllowedOne(t *testing.T) {
	resolver := fakeResolver{"indexer.invalid": {
		netip.MustParseAddr("127.0.0.1"),
		netip.MustParseAddr("93.184.216.34"),
	}}
	var dialled string
	guard := httpx.NewGuard(resolver, recordingDial(&dialled), nil)

	if _, err := guard.DialContext(t.Context(), "tcp", "indexer.invalid:443"); err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	if want := "93.184.216.34:443"; dialled != want {
		t.Errorf("dialled %q, want %q", dialled, want)
	}
}

func TestGuard_BlocksAnIPLiteralWithoutResolving(t *testing.T) {
	guard := httpx.NewGuard(refusingResolver{t}, refusingDial, nil)

	if _, err := guard.DialContext(t.Context(), "tcp", "169.254.169.254:80"); !errors.Is(err, httpx.ErrBlockedAddress) {
		t.Errorf("DialContext = %v, want ErrBlockedAddress", err)
	}
}

func TestGuard_AllowlistExemptsANamedHostOnly(t *testing.T) {
	resolver := fakeResolver{
		"nas.lan":   {netip.MustParseAddr("192.168.1.50")},
		"other.lan": {netip.MustParseAddr("192.168.1.51")},
	}
	var dialled string
	guard := httpx.NewGuard(resolver, recordingDial(&dialled), []string{"NAS.lan"})

	if _, err := guard.DialContext(t.Context(), "tcp", "nas.lan:9117"); err != nil {
		t.Fatalf("allowlisted host: %v", err)
	}
	if want := "nas.lan:9117"; dialled != want {
		t.Errorf("dialled %q, want %q", dialled, want)
	}

	// The allowlist names hosts; it does not turn the check off.
	if _, err := guard.DialContext(t.Context(), "tcp", "other.lan:9117"); !errors.Is(err, httpx.ErrBlockedAddress) {
		t.Errorf("non-allowlisted LAN host = %v, want ErrBlockedAddress", err)
	}
}

func TestGuard_ReportsWhenEveryResolvedAddressIsBlocked(t *testing.T) {
	resolver := fakeResolver{"indexer.invalid": {
		netip.MustParseAddr("127.0.0.1"),
		netip.MustParseAddr("10.0.0.1"),
	}}
	guard := httpx.NewGuard(resolver, refusingDial, nil)

	if _, err := guard.DialContext(t.Context(), "tcp", "indexer.invalid:80"); !errors.Is(err, httpx.ErrBlockedAddress) {
		t.Errorf("DialContext = %v, want ErrBlockedAddress", err)
	}
}

type fakeResolver map[string][]netip.Addr

func (r fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	addresses, ok := r[host]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	return addresses, nil
}

// refusingResolver fails the test if it is consulted, proving an IP literal
// never reaches DNS.
type refusingResolver struct{ t *testing.T }

func (r refusingResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	r.t.Errorf("resolver consulted for the IP literal %q", host)
	return nil, errors.New("should not be called")
}

func refusingDial(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("dial should not have been reached")
}

func recordingDial(target *string) httpx.DialFunc {
	return func(_ context.Context, _, address string) (net.Conn, error) {
		*target = address
		return nil, nil //nolint:nilnil // the caller only inspects the recorded address
	}
}

// A nil resolver or dialer must mean the system's, not a nil dereference. Taken
// literally it segfaults inside a background dial goroutine, which kills the
// process from a stack that names neither the caller nor the reason.
func TestNewGuardDefaultsItsResolverAndDialer(t *testing.T) {
	guard := httpx.NewGuard(nil, nil, nil)

	// Loopback is refused by classification, which proves the resolver ran
	// rather than panicking on the way to it.
	_, err := guard.DialContext(t.Context(), "tcp", "localhost:9")
	if err == nil {
		t.Fatal("dialling loopback through the guard succeeded")
	}
	if strings.Contains(err.Error(), "nil pointer") {
		t.Errorf("the guard dereferenced a nil resolver: %v", err)
	}
}
