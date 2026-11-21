package apiserver

import (
	"context"
	"log/slog"
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
