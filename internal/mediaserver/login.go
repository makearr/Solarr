package mediaserver

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// Session signs in with a user name and password on first use, keeps the token and signs in again when the server refuses it.
type Session struct {
	mu     sync.Mutex
	token  string
	signIn func(ctx context.Context) (string, error)
}

// NewSession wraps signIn, which answers a fresh token.
func NewSession(signIn func(ctx context.Context) (string, error)) *Session {
	return &Session{signIn: signIn}
}

func (s *Session) current(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token == "" {
		token, err := s.signIn(ctx)
		if err != nil {
			return "", err
		}
		s.token = token
	}
	return s.token, nil
}

func (s *Session) forget(stale string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token == stale {
		s.token = ""
	}
}

// CallSigned is Call with the header built from the session's token; a 401 signs in once more and repeats the request.
// A nil session sends header(""), the static-key case.
func CallSigned(ctx context.Context, doer Doer, session *Session, header func(token string) http.Header, method, address string, body, into any) error {
	if session == nil {
		return Call(ctx, doer, method, address, header(""), body, into)
	}
	for attempt := 0; ; attempt++ {
		token, err := session.current(ctx)
		if err != nil {
			return err
		}
		err = Call(ctx, doer, method, address, header(token), body, into)
		var status *httpx.StatusError
		if attempt == 0 && errors.As(err, &status) && status.Status == http.StatusUnauthorized {
			session.forget(token)
			continue
		}
		return err
	}
}

// FetchSigned reads a picture the way CallSigned reads JSON: header from the session's token, one retry after a 401; the body is
// limited to limit bytes.
func FetchSigned(ctx context.Context, doer Doer, session *Session, header func(token string) http.Header, address string, limit int64) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		token := ""
		if session != nil {
			var err error
			if token, err = session.current(ctx); err != nil {
				return nil, err
			}
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err != nil {
			return nil, err
		}
		for name, values := range header(token) {
			for _, value := range values {
				request.Header.Add(name, value)
			}
		}
		response, err := doer.Do(ctx, request)
		var status *httpx.StatusError
		if attempt == 0 && session != nil && errors.As(err, &status) && status.Status == http.StatusUnauthorized {
			session.forget(token)
			continue
		}
		if err != nil {
			return nil, err
		}
		data, err := httpx.ReadLimited(response.Body, limit)
		_ = response.Body.Close()
		if err != nil {
			return nil, err
		}
		if response.StatusCode != http.StatusOK {
			return nil, &httpx.StatusError{Status: response.StatusCode}
		}
		return data, nil
	}
}
