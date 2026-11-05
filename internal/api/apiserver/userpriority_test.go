package apiserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

func TestUserPriority_ARequestCarriesTheFirstLane(t *testing.T) {
	var got httpx.Priority
	handler := (&Server{}).userPriority(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = httpx.PriorityFrom(r.Context())
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if got != httpx.PriorityUser {
		t.Errorf("priority = %d, want %d", got, httpx.PriorityUser)
	}
}
