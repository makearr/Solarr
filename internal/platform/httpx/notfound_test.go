package httpx_test

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

func TestIsNotFound_OnlyAnAnswerOfNothingThere(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"sentinel", httpx.ErrNotFound, true},
		{"404", &httpx.StatusError{Status: http.StatusNotFound}, true},
		{"410 wrapped", fmt.Errorf("x: %w", &httpx.StatusError{Status: http.StatusGone}), true},
		{"500", &httpx.StatusError{Status: http.StatusInternalServerError}, false},
		{"transport", errors.New("connection refused"), false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		if got := httpx.IsNotFound(c.err); got != c.want {
			t.Errorf("%s: IsNotFound = %v, want %v", c.name, got, c.want)
		}
	}
}
