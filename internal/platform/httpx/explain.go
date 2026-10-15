package httpx

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
)

// Explain turns a failed connection into what went wrong and what to check, the technical detail kept at the end so
// nothing is lost; an error it does not recognise is returned as it is.
func Explain(err error) string {
	if err == nil {
		return ""
	}
	cause := explanation(err)
	if cause == "" {
		return err.Error()
	}
	return cause + " (" + err.Error() + ")"
}

func explanation(err error) string {
	var status *StatusError
	var dns *net.DNSError
	var blocked *BlockedError
	var unknownAuthority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var certificate *tls.CertificateVerificationError
	var record tls.RecordHeaderError
	var open *OpenError
	switch {
	case errors.As(err, &status):
		return statusExplanation(status.Status)
	case errors.As(err, &blocked):
		return fmt.Sprintf("Solarr refuses to contact %s: it resolves to %s, a %s address other than the one "+
			"configured (a redirect, or a name that resolves somewhere unexpected).", blocked.Host, blocked.Address, blocked.Reason)
	case errors.As(err, &open):
		return "Solarr paused calls to this service after repeated failures; it tries again by itself shortly."
	case errors.As(err, &dns):
		return fmt.Sprintf("The name %q does not resolve from inside Solarr's container. Use an IP address, or the "+
			"service's container name on a Docker network Solarr shares.", dns.Name)
	case errors.Is(err, syscall.ECONNREFUSED):
		return "Nothing answers on that port: the port is wrong, the service is not running, or it listens only on " +
			"127.0.0.1 inside its own container."
	case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH):
		return "There is no route to that address from Solarr's container. Check the IP address, and that the " +
			"container's network can reach your LAN."
	case errors.As(err, &unknownAuthority), errors.As(err, &hostname), errors.As(err, &certificate):
		return "The HTTPS certificate is not trusted (self-signed, expired, or issued for another name). Use the " +
			"service's plain http:// address on your LAN, or give it a valid certificate."
	case errors.As(err, &record), strings.Contains(err.Error(), "server gave HTTP response to HTTPS client"):
		return "The address speaks plain HTTP: change https:// to http:// (or the other way round)."
	case errors.Is(err, context.DeadlineExceeded) || isTimeout(err):
		return "The host did not answer in time. Usually a firewall, or a Docker network that cannot reach your LAN; " +
			"less often the service is overloaded."
	case errors.Is(err, syscall.ECONNRESET), strings.Contains(err.Error(), "malformed HTTP response"):
		return "Something answered on that port, but not the expected service: check the port and http:// or https://."
	}
	return ""
}

func statusExplanation(code int) string {
	switch {
	case code == 401 || code == 403:
		return "Reached the service, but it refused the API key or the user name and password."
	case code == 404:
		return "Reached a web server, but not the service's API at that address: check the URL base, e.g. " +
			"http://host:8686/lidarr when it runs under a path."
	case code == 429:
		return "The service says Solarr asked too often; it waits and tries again."
	case code >= 500:
		return fmt.Sprintf("The service answered with an internal error (%d); its own log says why.", code)
	}
	return ""
}

func isTimeout(err error) bool {
	var timeout interface{ Timeout() bool }
	return errors.As(err, &timeout) && timeout.Timeout()
}
