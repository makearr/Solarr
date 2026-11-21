package apiserver

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
)

type panickingCompanion struct{}

func (panickingCompanion) Added(context.Context, AddedWork) error { panic("companion down") }

// notifyHandler hands each logged message to a channel.
type notifyHandler struct{ messages chan string }

func (h notifyHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h notifyHandler) Handle(_ context.Context, record slog.Record) error {
	h.messages <- record.Message
	return nil
}
func (h notifyHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h notifyHandler) WithGroup(string) slog.Handler      { return h }

// An *Arr companion that panics while being told of an add is logged and does not take the process down (RACE-3).
func TestTellCompanion_APanickingCompanionIsLoggedNotFatal(t *testing.T) {
	messages := make(chan string, 1)
	server := &Server{arrCompanion: panickingCompanion{}, logger: slog.New(notifyHandler{messages})}
	server.tellCompanion(t.Context(), AddWorkResult{WorkID: 4, Kind: "album"}, gen.AddWorkRequest{})
	select {
	case message := <-messages:
		if want := "arr companion: the *Arr was not told about an add"; message != want {
			t.Errorf("logged %q, want %q", message, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the companion's panic was not reported")
	}
}

// A request that wrote and succeeded is reported to the after-write hook; a read or a refusal is not (RAM-1).
func TestLogRequest_ReportsOnlySuccessfulWrites(t *testing.T) {
	writes := 0
	server := &Server{logger: slog.New(slog.DiscardHandler), afterWrite: func() { writes++ }}
	handler := server.logRequest(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/refused" {
			w.WriteHeader(http.StatusConflict)
		}
	}))
	for method, path := range map[string]string{http.MethodGet: "/ok", http.MethodDelete: "/refused"} {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, path, nil))
	}
	if writes != 0 {
		t.Fatalf("hook called %d times for a read and a refused write, want 0", writes)
	}
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/ok", nil))
	if writes != 1 {
		t.Errorf("hook called %d times after a successful write, want 1", writes)
	}
}
