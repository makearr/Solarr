package httpx

import (
	"errors"
	"net/url"
	"strings"
)

// redactedValue replaces a credential in anything rendered for a human, and
// reads the same as secrets.Secret's own rendering.
const redactedValue = "<redacted>"

// secretQueryParams name query parameters that carry a credential. Evidence,
// not anticipation: apikey is Newznab/Torznab's and SABnzbd's, client is
// AcoustID's, passkey and rss_key are how torrent indexers pass one.
var secretQueryParams = map[string]struct{}{
	"apikey": {}, "api_key": {}, "apitoken": {}, "api_token": {},
	"client": {}, "key": {}, "token": {}, "access_token": {},
	"passkey": {}, "rss_key": {}, "rsskey": {}, "authkey": {}, "torrent_pass": {}, "password": {}, "secret": {},
	"auth": {}, "sid": {}, "r": {},
}

// RedactURL renders a URL for errors and logs with userinfo masked and every credential-bearing query parameter redacted (Invariant 61, ADR-14), keeping the parameter name.
func RedactURL(target *url.URL) string {
	if target == nil {
		return ""
	}
	query := target.Query()
	found := false
	for name, values := range query {
		if _, secret := secretQueryParams[strings.ToLower(name)]; !secret {
			continue
		}
		for i := range values {
			if values[i] != "" {
				values[i] = redactedValue
				found = true
			}
		}
	}
	if !found {
		// Untouched URLs keep their exact original form: re-encoding reorders
		// the query, and an error is read against the request that caused it.
		return target.Redacted()
	}
	masked := *target
	masked.RawQuery = query.Encode()
	return masked.Redacted()
}

// redactRequestError rebuilds net/http's *url.Error because its Error() prints the URL with its query, and keeps the cause so errors.Is and errors.As still work.
func redactRequestError(err error, redacted string) error {
	var wrapped *url.Error
	if errors.As(err, &wrapped) {
		return &url.Error{Op: wrapped.Op, URL: redacted, Err: wrapped.Err}
	}
	return err
}
