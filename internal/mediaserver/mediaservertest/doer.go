// Package mediaservertest gives the client tests a Doer that behaves like httpx.Client without a guard or a limiter.
package mediaservertest

import (
	"context"
	"net/http"

	"github.com/makearr/Solarr/internal/mediaserver"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// Direct sends the request as it is; a status outside 2xx comes back as the *httpx.StatusError the real client returns.
func Direct() mediaserver.Doer {
	return mediaserver.DoerFunc(func(_ context.Context, request *http.Request) (*http.Response, error) {
		response, err := new(http.Client).Do(request)
		if err != nil {
			return nil, err
		}
		if response.StatusCode >= 300 {
			_ = response.Body.Close()
			return nil, &httpx.StatusError{Provider: "test", URL: request.URL.Path, Status: response.StatusCode, Header: response.Header}
		}
		return response, nil
	})
}
