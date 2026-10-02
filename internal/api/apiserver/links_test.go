package apiserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
)

type fakeLinks struct{ started []int64 }

func (f *fakeLinks) PreviewLinks(context.Context, int64) (apiserver.LinkPreview, error) {
	return apiserver.LinkPreview{
		Total: 1, ReclaimableBytes: 900,
		Links:   []apiserver.LinkRow{{MediaObjectID: 3, SizeBytes: 900, Keep: "/m/a.flac", Replace: "/m/b.flac", Method: "hardlink"}},
		Skipped: []apiserver.LinkSkip{{Reason: "other-filesystem", Count: 2}},
	}, nil
}

func (f *fakeLinks) StartLinks(_ context.Context, categoryID int64) error {
	f.started = append(f.started, categoryID)
	return nil
}

func TestCategoryLinks_APreviewIsReadableButQueueingNeedsManageAccess(t *testing.T) {
	fixture := newWorksFixture(t)
	category, _ := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	user := fixture.createUser(t, "listener", settings.RoleReadonly)
	if err := fixture.cache.SetCategoryAccess(t.Context(), user.ID, category.ID, settings.PermissionRead); err != nil {
		t.Fatalf("SetCategoryAccess: %v", err)
	}
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/categories/1/links"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("preview status %d, body %s", recorder.Code, recorder.Body)
	}
	var body struct {
		Total       int   `json:"total"`
		Reclaimable int64 `json:"reclaimable_bytes"`
		Links       []struct {
			Replace string `json:"replace"`
			Method  string `json:"method"`
		} `json:"links"`
		Skipped []struct {
			Reason string `json:"reason"`
			Count  int    `json:"count"`
		} `json:"skipped"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Total != 1 || body.Reclaimable != 900 || len(body.Links) != 1 || body.Links[0].Method != "hardlink" ||
		len(body.Skipped) != 1 || body.Skipped[0].Reason != "other-filesystem" {
		t.Errorf("preview = %+v", body)
	}
	if recorder := fixture.do(authedPost(t, cookies, "/api/v1/categories/1/links", "")); recorder.Code != http.StatusForbidden {
		t.Errorf("queueing as a reader: status %d, want 403", recorder.Code)
	}
}

func TestCategoryLinks_AnAdministratorQueuesTheRun(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createCategoryWithRoot(t, "Music", "/media/music")
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	if recorder := fixture.do(authedPost(t, cookies, "/api/v1/categories/1/links", "")); recorder.Code != http.StatusAccepted {
		t.Fatalf("status %d, body %s", recorder.Code, recorder.Body)
	}
}
