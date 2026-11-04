package apiserver_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
)

func TestGetRequestPriority_AUserRequestRanksAboveEveryAutomaticSearch(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	library, _ := fixture.createCategoryWithRoot(t, "Music", "/library/music")

	recorder := fixture.do(authedGet(t, cookies, fmt.Sprintf("/api/v1/priority/request?category_id=%d", library.ID)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var got struct {
		Request       int64 `json:"request"`
		BestAutomatic int64 `json:"best_automatic"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// The shipped weights are 40 for a request and 35 for an RSS find, each worth a thousand, less 100 per library rank.
	rank := library.PriorityRank
	if got.Request != 40000-100*rank || got.BestAutomatic != 35000-100*rank {
		t.Errorf("got %+v, want request %d and best automatic %d", got, 40000-100*rank, 35000-100*rank)
	}

	if missing := fixture.do(authedGet(t, cookies, "/api/v1/priority/request?category_id=9999")); missing.Code != http.StatusNotFound {
		t.Errorf("unknown library status = %d, want 404", missing.Code)
	}
}
