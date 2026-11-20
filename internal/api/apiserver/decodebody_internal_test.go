package apiserver

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeBody_AnswersTooLargeNotMalformed(t *testing.T) {
	server := &Server{logger: slog.New(slog.DiscardHandler)}
	tests := []struct {
		name       string
		body       string
		wantOK     bool
		wantStatus int
	}{
		{"fits", `{"name":"a"}`, true, http.StatusOK},
		{"malformed", `{"name":`, false, http.StatusBadRequest},
		{"past the limit", `{"name":"` + strings.Repeat("a", 64) + `"}`, false, http.StatusRequestEntityTooLarge},
	}
	for _, test := range tests {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(test.body))
		var body struct{ Name string }
		if got := server.decodeBody(recorder, request, 32, &body, "expected a name"); got != test.wantOK {
			t.Errorf("%s: ok = %v, want %v", test.name, got, test.wantOK)
		}
		if recorder.Code != test.wantStatus {
			t.Errorf("%s: status = %d, want %d", test.name, recorder.Code, test.wantStatus)
		}
	}
}
