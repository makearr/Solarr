package apiserver_test

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/makearr/MediaNexus/internal/apiserver"
	"github.com/makearr/MediaNexus/internal/auth"
	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/settings"
	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/migrations"
)

// Walking every page of a real collection, against a real database.
//
// The paginate package's own tests cover Build in isolation, and they cannot
// catch the failure that actually happens: a cursor without an id tie-break
// silently skips rows when several share a sort value. Every job here is
// created at the same instant on a fake clock, so the sort column is identical
// across all of them -- which is precisely the case that breaks.
func TestJobsPagination_WalksEveryRowExactlyOnce(t *testing.T) {
	const total = 250
	const pageSize = 20

	queue := newJobQueue(t)
	fixture := newJobsFixture(t, queue)

	created := make(map[int64]bool, total)
	for index := range total {
		job := mustEnqueueJob(t, queue, fmt.Sprintf("action:%d", index))
		created[job.ID] = true
	}

	cookies := fixture.signIn(t)

	seen := make(map[int64]int, total)
	cursor := ""
	for page := 0; ; page++ {
		if page > total {
			t.Fatal("pagination did not terminate; the cursor is not advancing")
		}

		target := fmt.Sprintf("/api/v1/jobs?limit=%d", pageSize)
		if cursor != "" {
			target += "&cursor=" + url.QueryEscape(cursor)
		}
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.AddCookie(cookies[0])

		recorder := fixture.do(request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("page %d: status %d (%s)", page, recorder.Code, recorder.Body)
		}

		var body struct {
			Items []struct {
				ID int64 `json:"id"`
			} `json:"items"`
			NextCursor string `json:"next_cursor"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if len(body.Items) > pageSize {
			t.Fatalf("page %d returned %d rows, over the limit of %d",
				page, len(body.Items), pageSize)
		}
		for _, item := range body.Items {
			seen[item.ID]++
		}

		if body.NextCursor == "" {
			break
		}
		cursor = body.NextCursor
	}

	// Every row exactly once. Skipping is the tie-break bug; repeating means the
	// cursor went backwards.
	if len(seen) != total {
		t.Errorf("walked %d distinct rows, want %d -- %d were skipped",
			len(seen), total, total-len(seen))
	}
	for id, times := range seen {
		if times != 1 {
			t.Errorf("row %d appeared %d times", id, times)
		}
	}
	for id := range created {
		if seen[id] == 0 {
			t.Errorf("row %d was created but never returned", id)
		}
	}
}

func newJobQueue(t *testing.T) *jobs.Queue {
	t.Helper()
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatalf("create backup directory: %v", err)
	}

	fake := clock.NewFakeAtEpoch()
	opened, err := store.Open(t.Context(), fake, store.Config{
		Name: "jobs", Path: filepath.Join(directory, "jobs.db"),
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })

	owner, err := migrations.Jobs()
	if err != nil {
		t.Fatalf("migrations: %v", err)
	}
	if err := store.Migrate(t.Context(), opened, backups, owner); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	registry := jobs.NewRegistry()
	registry.MustRegister(jobs.Definition{Type: "test.walk", Pool: jobs.PoolMaintenance})
	return jobs.NewQueue(opened, fake, registry, jobs.NoJitter)
}

func mustEnqueueJob(t *testing.T, queue *jobs.Queue, actionKey string) jobs.Job {
	t.Helper()
	result, err := queue.Enqueue(t.Context(), jobs.Request{Type: "test.walk", ActionKey: actionKey})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	return result.Job
}

// jobsFixture is a server wired to a real job queue. The shared auth fixture
// deliberately has none -- several tests there assert what happens without one --
// so this stands beside it rather than changing its meaning.
type jobsFixture struct {
	server *apiserver.Server
}

func newJobsFixture(t *testing.T, queue *jobs.Queue) jobsFixture {
	t.Helper()
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatalf("create backup directory: %v", err)
	}

	fake := clock.NewFakeAtEpoch()
	opened, err := store.Open(t.Context(), fake, store.Config{
		Name: "settings", Path: filepath.Join(directory, "settings.db"),
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })

	owner, err := migrations.Settings()
	if err != nil {
		t.Fatalf("migrations: %v", err)
	}
	if err := store.Migrate(t.Context(), opened, backups, owner); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	cache, err := settings.NewCache(t.Context(), opened)
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}

	hashed, err := auth.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if _, err := cache.CreateUser(t.Context(), fake, "avs", hashed, settings.RoleAdmin); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	server, err := apiserver.New(apiserver.Config{
		Bind: "127.0.0.1", Port: 0, BaseURL: "/",
		Checker: fakeChecker{},
		Jobs:    queue,
		Clock:   fake,
		Logger:  slog.New(slog.DiscardHandler),
		Auth: apiserver.AuthConfig{
			Mode:     apiserver.AuthLocal,
			Sessions: auth.NewSessions([]byte("0123456789abcdef0123456789abcdef"), fake, time.Hour),
			Settings: cache,
			Clock:    fake,
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return jobsFixture{server: server}
}

func (f jobsFixture) do(request *http.Request) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	f.server.Handler().ServeHTTP(recorder, request)
	return recorder
}

func (f jobsFixture) signIn(t *testing.T) []*http.Cookie {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		strings.NewReader(`{"username":"avs","password":"`+testPassword+`"}`))
	request.Header.Set("Content-Type", "application/json")

	recorder := f.do(request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("login: %d (%s)", recorder.Code, recorder.Body)
	}
	return recorder.Result().Cookies()
}
