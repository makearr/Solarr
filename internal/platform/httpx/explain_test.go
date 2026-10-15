package httpx_test

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// Each way a connection fails reads as its cause and what to check, with the original error kept at the end.
func TestExplain_SaysWhatWentWrongAndWhatToCheck(t *testing.T) {
	dial := func(err error) error {
		return &url.Error{Op: "Get", URL: "http://lidarr:8686/api/v1/system/status", Err: &net.OpError{Op: "dial", Net: "tcp", Err: err}}
	}
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"refused", dial(&os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED}), "Nothing answers on that port"},
		{"no route", dial(&os.SyscallError{Syscall: "connect", Err: syscall.EHOSTUNREACH}), "There is no route"},
		{"unknown name", dial(&net.DNSError{Err: "no such host", Name: "lidarr", IsNotFound: true}), `The name "lidarr" does not resolve`},
		{"timeout", fmt.Errorf("indexer: caps: %w", context.DeadlineExceeded), "did not answer in time"},
		{"self-signed", dial(x509.UnknownAuthorityError{}), "certificate is not trusted"},
		{"plain http on https", errors.New(`Get "https://sab:8080/api": http: server gave HTTP response to HTTPS client`), "speaks plain HTTP"},
		{"bad key", fmt.Errorf("sabnzbd: %w", &httpx.StatusError{Provider: "sabnzbd", Status: 403}), "refused the API key"},
		{"wrong url base", &httpx.StatusError{Provider: "lidarr", Status: 404}, "check the URL base"},
		{"server error", &httpx.StatusError{Provider: "prowlarr", Status: 502}, "internal error (502)"},
		{"blocked", &httpx.BlockedError{Host: "example.test", Address: netip.MustParseAddr("127.0.0.1"), Reason: httpx.ReasonLoopback}, "refuses to contact example.test"},
	} {
		got := httpx.Explain(tc.err)
		if !strings.Contains(got, tc.want) || !strings.HasSuffix(got, "("+tc.err.Error()+")") {
			t.Errorf("%s: Explain = %q; want it to say %q and end with the original error", tc.name, got, tc.want)
		}
	}
	if got := httpx.Explain(errors.New("qbittorrent: login failed")); got != "qbittorrent: login failed" {
		t.Errorf("an unrecognised error = %q, want it unchanged", got)
	}
}
