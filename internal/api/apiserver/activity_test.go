package apiserver_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
)

// 2026-09-29: the status bar's named running jobs; a library's job only for those who see it.
func TestActivity_NamesRunningJobsAndHidesOtherLibraries(t *testing.T) {
	fixture := newWorksFixture(t)
	visible, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 10)
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := fixture.cache.CreateCategory(t.Context(), "music", "Hidden", 20)
	if err != nil {
		t.Fatal(err)
	}
	user := fixture.createUser(t, "listener", settings.RoleReadonly)
	if err := fixture.cache.SetCategoryAccess(t.Context(), user.ID, visible.ID, settings.PermissionRead); err != nil {
		t.Fatal(err)
	}
	fixture.jobRegistry.MustRegister(jobs.Definition{Type: "music.enrich", Name: "Enriching metadata", Pool: jobs.PoolMetadata})
	fixture.jobRegistry.MustRegister(jobs.Definition{Type: "import.inspect", Name: "Importing", Pool: jobs.PoolImport})
	fixture.jobRegistry.MustRegister(jobs.Definition{Type: "acquire.poll", Pool: jobs.PoolAcquire})
	fixture.running.Start(jobs.Job{ID: 1, Type: "music.enrich"}).Set(3, 10)
	fixture.running.Start(jobs.Job{ID: 2, Type: "import.inspect", CategoryID: hidden.ID})
	fixture.running.Start(jobs.Job{ID: 3, Type: "import.inspect", CategoryID: visible.ID})
	fixture.running.Start(jobs.Job{ID: 4, Type: "acquire.poll"})

	recorder := fixture.do(authedGet(t, fixture.signIn(t, "listener"), "/api/v1/system/activity"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, %s", recorder.Code, recorder.Body)
	}
	var body struct {
		Running []map[string]any `json:"running"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{
		{"job_id": 1.0, "type": "music.enrich", "name": "Enriching metadata", "started_at": "2020-01-01T00:00:00Z",
			"done": 3.0, "total": 10.0},
		{"job_id": 3.0, "type": "import.inspect", "name": "Importing", "started_at": "2020-01-01T00:00:00Z",
			"done": 0.0},
	}
	if !reflect.DeepEqual(body.Running, want) {
		t.Errorf("running = %v\nwant %v", body.Running, want)
	}
}
