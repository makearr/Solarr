package apiserver_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/platform/events"
)

// frame is one parsed SSE message.
type frame struct {
	id    string
	name  string
	data  string
	batch []events.Event
}

// readFrames parses SSE frames off a live response body until the deadline.
func readFrames(t *testing.T, body *bufio.Reader, want int, deadline time.Duration) []frame {
	t.Helper()

	type parsed struct {
		frames []frame
		err    error
	}
	done := make(chan parsed, 1)

	go func() {
		var collected []frame
		var current frame
		for {
			line, err := body.ReadString('\n')
			if err != nil {
				done <- parsed{collected, err}
				return
			}
			line = strings.TrimRight(line, "\r\n")

			switch {
			case line == "":
				if current.name != "" {
					var payload struct {
						Events []events.Event `json:"events"`
					}
					_ = json.Unmarshal([]byte(current.data), &payload)
					current.batch = payload.Events
					collected = append(collected, current)
					if len(collected) >= want {
						done <- parsed{collected, nil}
						return
					}
				}
				current = frame{}
			case strings.HasPrefix(line, "id: "):
				current.id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "event: "):
				current.name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				current.data = strings.TrimPrefix(line, "data: ")
			}
		}
	}()

	select {
	case result := <-done:
		return result.frames
	case <-time.After(deadline):
		t.Fatalf("timed out waiting for %d SSE frames", want)
		return nil
	}
}

// openStream starts a real HTTP server and connects to the event stream.
func (f authFixture) openStream(t *testing.T, username, query string) (*bufio.Reader, func()) {
	t.Helper()
	cookies := f.login(t, username, testPassword).Result().Cookies()

	httpServer := httptest.NewServer(f.server.Handler())
	request, err := http.NewRequest(http.MethodGet, httpServer.URL+"/api/v1/events"+query, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.AddCookie(cookies[0])

	response, err := http.DefaultClient.Do(request) //nolint:bodyclose // closed by the returned func
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d, want 200", response.StatusCode)
	}
	if got := response.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}

	return bufio.NewReader(response.Body), func() {
		_ = response.Body.Close()
		httpServer.Close()
	}
}

// Section 13.4: a bulk import at 500k rows would otherwise emit an event storm that saturates
// the connection and pins the browser.
func TestEvents_CoalescesRepeatedChangesToOneEntity(t *testing.T) {
	fixture := newAuthFixture(t, apiserver.AuthLocal)
	body, closeStream := fixture.openStream(t, "avs", "")
	defer closeStream()

	for progress := range 50 {
		fixture.bus.Publish(events.Event{
			Topic: events.TopicJob, Entity: "job:1", Kind: "updated",
			Data: map[string]int{"progress": progress},
		})
	}
	fixture.clock.BlockUntilWaiters(1)
	fixture.clock.Advance(apiserver.FlushInterval)

	frames := readFrames(t, body, 1, 3*time.Second)
	if len(frames) != 1 {
		t.Fatalf("got %d frames, want 1", len(frames))
	}
	if len(frames[0].batch) != 1 {
		t.Fatalf("frame carried %d events, want 1 after coalescing", len(frames[0].batch))
	}

	// The latest state, not the first.
	var payload map[string]int
	raw, _ := json.Marshal(frames[0].batch[0].Data)
	_ = json.Unmarshal(raw, &payload)
	if payload["progress"] != 49 {
		t.Errorf("progress = %d, want the latest value 49", payload["progress"])
	}
}

func TestEvents_BatchesUnrelatedChangesIntoOneMessage(t *testing.T) {
	fixture := newAuthFixture(t, apiserver.AuthLocal)
	body, closeStream := fixture.openStream(t, "avs", "")
	defer closeStream()

	fixture.bus.Publish(events.Event{Topic: events.TopicJob, Entity: "job:1", Kind: "updated"})
	fixture.bus.Publish(events.Event{Topic: events.TopicJob, Entity: "job:2", Kind: "updated"})
	fixture.bus.Publish(events.Event{Topic: events.TopicJob, Entity: "job:3", Kind: "updated"})
	fixture.clock.BlockUntilWaiters(1)
	fixture.clock.Advance(apiserver.FlushInterval)

	frames := readFrames(t, body, 1, 3*time.Second)
	if len(frames[0].batch) != 3 {
		t.Errorf("frame carried %d events, want all 3 batched", len(frames[0].batch))
	}
}

// ADR-27: a job-progress message naming an album from a hidden category is a
// leak, and the filter runs before the event is ever buffered.
func TestEvents_HidesCategoriesTheViewerCannotSee(t *testing.T) {
	fixture := newAuthFixture(t, apiserver.AuthLocal)
	body, closeStream := fixture.openStream(t, "housemate", "")
	defer closeStream()

	fixture.bus.Publish(events.Event{
		Topic: events.TopicLibrary, Entity: "work:secret",
		CategoryID: fixture.adult.ID, Kind: "updated",
	})
	fixture.bus.Publish(events.Event{
		Topic: events.TopicLibrary, Entity: "work:ok",
		CategoryID: fixture.music.ID, Kind: "updated",
	})
	fixture.clock.BlockUntilWaiters(1)
	fixture.clock.Advance(apiserver.FlushInterval)

	frames := readFrames(t, body, 1, 3*time.Second)
	for _, event := range frames[0].batch {
		if event.CategoryID == fixture.adult.ID {
			t.Errorf("an event from a hidden category reached the client: %+v", event)
		}
	}
	if len(frames[0].batch) != 1 || frames[0].batch[0].Entity != "work:ok" {
		t.Errorf("batch = %+v, want only the visible work", frames[0].batch)
	}
	// The entity name itself must not appear anywhere in the frame.
	if strings.Contains(frames[0].data, "work:secret") {
		t.Errorf("the hidden entity was named in the payload: %s", frames[0].data)
	}
}

// Events with no category are system-wide, and who sees them is a question
// about roles rather than category grants.
func TestEvents_SystemEventsFollowRole(t *testing.T) {
	fixture := newAuthFixture(t, apiserver.AuthLocal)

	body, closeStream := fixture.openStream(t, "housemate", "")
	fixture.bus.Publish(events.Event{Topic: events.TopicPressure, Entity: "disk:1", Kind: "updated"})
	fixture.bus.Publish(events.Event{
		Topic: events.TopicLibrary, Entity: "work:ok",
		CategoryID: fixture.music.ID, Kind: "updated",
	})
	fixture.clock.BlockUntilWaiters(1)
	fixture.clock.Advance(apiserver.FlushInterval)

	frames := readFrames(t, body, 1, 3*time.Second)
	for _, event := range frames[0].batch {
		if event.Entity == "disk:1" {
			t.Error("a readonly user saw a system event")
		}
	}
	closeStream()

	adminBody, closeAdmin := fixture.openStream(t, "avs", "")
	defer closeAdmin()
	fixture.bus.Publish(events.Event{Topic: events.TopicPressure, Entity: "disk:1", Kind: "updated"})
	fixture.clock.BlockUntilWaiters(1)
	fixture.clock.Advance(apiserver.FlushInterval)

	adminFrames := readFrames(t, adminBody, 1, 3*time.Second)
	if len(adminFrames[0].batch) != 1 || adminFrames[0].batch[0].Entity != "disk:1" {
		t.Errorf("admin batch = %+v, want the system event", adminFrames[0].batch)
	}
}

func TestEvents_FiltersByRequestedTopics(t *testing.T) {
	fixture := newAuthFixture(t, apiserver.AuthLocal)
	body, closeStream := fixture.openStream(t, "avs", "?topics=job")
	defer closeStream()

	fixture.bus.Publish(events.Event{Topic: events.TopicLibrary, Entity: "work:1", Kind: "updated"})
	fixture.bus.Publish(events.Event{Topic: events.TopicJob, Entity: "job:1", Kind: "updated"})
	fixture.clock.BlockUntilWaiters(1)
	fixture.clock.Advance(apiserver.FlushInterval)

	frames := readFrames(t, body, 1, 3*time.Second)
	if len(frames[0].batch) != 1 || frames[0].batch[0].Topic != events.TopicJob {
		t.Errorf("batch = %+v, want only the job event", frames[0].batch)
	}
}

// An unknown topic must be an error, not a filter matching nothing: the latter
// looks like a working stream that never delivers.
func TestEvents_RejectsAnUnknownTopic(t *testing.T) {
	fixture := newAuthFixture(t, apiserver.AuthLocal)
	cookies := fixture.login(t, "avs", testPassword).Result().Cookies()

	request := httptest.NewRequest(http.MethodGet, "/api/v1/events?topics=nonsense", nil)
	request.AddCookie(cookies[0])
	if got := fixture.do(t, request).Code; got != http.StatusBadRequest {
		t.Errorf("unknown topic = %d, want 400", got)
	}
}

func TestEvents_RequiresAuthentication(t *testing.T) {
	fixture := newAuthFixture(t, apiserver.AuthLocal)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil)
	if got := fixture.do(t, request).Code; got != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", got)
	}
}

func TestEvents_FrameCarriesAMonotonicID(t *testing.T) {
	fixture := newAuthFixture(t, apiserver.AuthLocal)
	body, closeStream := fixture.openStream(t, "avs", "")
	defer closeStream()

	fixture.bus.Publish(events.Event{Topic: events.TopicJob, Entity: "job:1", Kind: "updated"})
	fixture.clock.BlockUntilWaiters(1)
	fixture.clock.Advance(apiserver.FlushInterval)

	frames := readFrames(t, body, 1, 3*time.Second)
	if frames[0].id == "" {
		t.Error("the frame carries no id; a reconnecting client cannot say where it was")
	}
}

// A partial history would turn a lossy channel into a source of truth, so a
// client too far behind is told to refetch instead.
func TestEvents_TellsAClientToResyncWhenItIsTooFarBehind(t *testing.T) {
	fixture := newAuthFixture(t, apiserver.AuthLocal)
	for range events.DefaultReplayBuffer + 50 {
		fixture.bus.Publish(events.Event{Topic: events.TopicJob, Entity: "job:1", Kind: "updated"})
	}

	cookies := fixture.login(t, "avs", testPassword).Result().Cookies()
	httpServer := httptest.NewServer(fixture.server.Handler())
	defer httpServer.Close()

	request, err := http.NewRequestWithContext(context.Background(),
		http.MethodGet, httpServer.URL+"/api/v1/events", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.AddCookie(cookies[0])
	request.Header.Set("Last-Event-ID", "1")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = response.Body.Close() }()

	frames := readFrames(t, bufio.NewReader(response.Body), 1, 3*time.Second)
	if frames[0].name != "resync" {
		t.Errorf("event = %q, want resync", frames[0].name)
	}
}

func TestEvents_ReplaysWhatAReconnectingClientMissed(t *testing.T) {
	fixture := newAuthFixture(t, apiserver.AuthLocal)
	for range 5 {
		fixture.bus.Publish(events.Event{Topic: events.TopicJob, Entity: "job:1", Kind: "updated"})
	}

	cookies := fixture.login(t, "avs", testPassword).Result().Cookies()
	httpServer := httptest.NewServer(fixture.server.Handler())
	defer httpServer.Close()

	request, err := http.NewRequestWithContext(context.Background(),
		http.MethodGet, httpServer.URL+"/api/v1/events", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.AddCookie(cookies[0])
	request.Header.Set("Last-Event-ID", "2")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = response.Body.Close() }()

	frames := readFrames(t, bufio.NewReader(response.Body), 1, 3*time.Second)
	if frames[0].name != "update" {
		t.Fatalf("event = %q, want update", frames[0].name)
	}
	if len(frames[0].batch) != 3 {
		t.Errorf("replayed %d events, want the 3 missed", len(frames[0].batch))
	}
}
