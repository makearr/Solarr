package apiserver

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/auth"
)

// sealedBodyLimit is the largest body opened for sealed values; a larger one passes as it is, to its handler's limit.
const sealedBodyLimit = 1 << 20

// GetTransportKey answers the key the browser seals secrets with.
func (s *Server) GetTransportKey(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.logger, http.StatusOK, gen.TransportKey{Scheme: gen.TransportKeySchemeEncv1, PublicKey: s.auth.Transport.PublicKey()})
}

// openSealed replaces every sealed string of a JSON request body with its plaintext before the handler reads it.
func (s *Server) openSealed(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.auth.Transport == nil || r.Body == nil || !jsonBody(r) {
			next.ServeHTTP(w, r)
			return
		}
		head, err := io.ReadAll(io.LimitReader(r.Body, sealedBodyLimit+1))
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "the request body could not be read")
			return
		}
		if len(head) > sealedBodyLimit || !bytes.Contains(head, []byte(auth.SealedPrefix)) {
			r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(head), r.Body))
			next.ServeHTTP(w, r)
			return
		}
		opened, err := s.openBody(head)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Sealed value refused",
				"a secret sent encrypted could not be opened; reload the page and try again")
			return
		}
		r.Body, r.ContentLength = io.NopCloser(bytes.NewReader(opened)), int64(len(opened))
		next.ServeHTTP(w, r)
	})
}

func jsonBody(r *http.Request) bool {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
	default:
		return false
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mediaType == "application/json"
}

// openBody decodes a JSON body, opens its sealed strings wherever they are, and encodes it again; numbers keep their
// exact text.
func (s *Server) openBody(body []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	opened, err := s.openValue(document)
	if err != nil {
		return nil, err
	}
	return json.Marshal(opened)
}

func (s *Server) openValue(value any) (any, error) {
	switch typed := value.(type) {
	case string:
		if !auth.IsSealed(typed) {
			return typed, nil
		}
		return s.auth.Transport.Open(typed)
	case map[string]any:
		for key, item := range typed {
			opened, err := s.openValue(item)
			if err != nil {
				return nil, err
			}
			typed[key] = opened
		}
		return typed, nil
	case []any:
		for i, item := range typed {
			opened, err := s.openValue(item)
			if err != nil {
				return nil, err
			}
			typed[i] = opened
		}
		return typed, nil
	}
	return value, nil
}
