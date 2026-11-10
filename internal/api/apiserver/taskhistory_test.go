package apiserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
)

type fakeTaskHistory struct{ rows []apiserver.TaskHourRow }

func (f *fakeTaskHistory) Hours(context.Context, time.Time) ([]apiserver.TaskHourRow, error) {
	return f.rows, nil
}

func TestGetTaskHistory_AnySignedInPersonSeesTheHours(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.taskHistory.rows = []apiserver.TaskHourRow{{JobType: "library.scan", HourStartUnix: 3600, RunMs: 90_000, Runs: 2}}
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/schedules/history"))
	var body struct {
		Hours []struct {
			HourStart int64  `json:"hour_start"`
			JobType   string `json:"job_type"`
			Name      string `json:"name"`
			RunMs     int64  `json:"run_ms"`
			Runs      int64  `json:"runs"`
		} `json:"hours"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); recorder.Code != http.StatusOK || err != nil {
		t.Fatalf("status %d err %v body %s", recorder.Code, err, recorder.Body)
	}
	if len(body.Hours) != 1 || body.Hours[0].HourStart != 3600 || body.Hours[0].JobType != "library.scan" || body.Hours[0].RunMs != 90_000 || body.Hours[0].Runs != 2 || body.Hours[0].Name == "" {
		t.Fatalf("hours = %+v, want the scan row, named", body.Hours)
	}
	if recorder := fixture.do(authedGet(t, nil, "/api/v1/schedules/history")); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("signed out: status %d, want 401", recorder.Code)
	}
}
