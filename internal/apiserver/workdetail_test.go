package apiserver_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/makearr/MediaNexus/internal/apiserver"
	"github.com/makearr/MediaNexus/internal/settings"
)

// fakeWorkDetailReader mirrors fakeWorksReader: it records the id it was called
// with and answers a canned row or error, so a test can assert what visibility
// computed without a real library.db.
type fakeWorkDetailReader struct {
	row    apiserver.WorkDetailRow
	err    error
	lastID int64
	calls  int
}

func (f *fakeWorkDetailReader) GetWorkDetail(_ context.Context, id int64) (apiserver.WorkDetailRow, error) {
	f.calls++
	f.lastID = id
	if f.err != nil {
		return apiserver.WorkDetailRow{}, f.err
	}
	return f.row, nil
}

// Invariant 37: a work under a root folder attached to a category the caller
// cannot read answers 404, indistinguishable from a work that does not exist.
func TestGetWork_UnderARootFolderTheCallerCannotReadAnswers404(t *testing.T) {
	fixture := newWorksFixture(t)
	_, _ = fixture.createCategoryWithRoot(t, "Music", "/media/music")
	_, rootHidden := fixture.createCategoryWithRoot(t, "Private Stash", "/media/private")
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	fixture.workDetail.row = apiserver.WorkDetailRow{
		ID: 1, MediaType: "music", Title: "Secret Album", SortTitle: "secret album",
		RootFolderIDs: []int64{rootHidden.ID},
	}

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/works/1"))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a work under a root folder this caller cannot read", recorder.Code)
	}
}

func TestGetWork_UnderAVisibleRootFolderAnswers200WithFullShape(t *testing.T) {
	fixture := newWorksFixture(t)
	visible, rootVisible := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	user := fixture.createUser(t, "listener", settings.RoleReadonly)
	if err := fixture.cache.SetCategoryAccess(t.Context(), user.ID, visible.ID, settings.PermissionRead); err != nil {
		t.Fatalf("SetCategoryAccess: %v", err)
	}
	cookies := fixture.signIn(t, "listener")

	year := int64(1997)
	fixture.workDetail.row = apiserver.WorkDetailRow{
		ID: 1, MediaType: "music", Title: "KI Pecezinu", SortTitle: "ki pecezinu", Year: &year,
		AddedAt: fixture.clock.Now(), UpdatedAt: fixture.clock.Now(),
		ExternalIDs: []apiserver.ExternalIDRow{{Provider: "musicbrainz:release-group", ExternalID: "rg-1"}},
		Editions: []apiserver.EditionRow{
			{ID: 10, Kind: "official", Label: "release-1", IsDefault: true,
				ExternalIDs: []apiserver.ExternalIDRow{{Provider: "musicbrainz:release", ExternalID: "release-1"}}},
		},
		Parts: []apiserver.PartRow{
			{ID: 20, MediaRef: "01", SortKey: "01", Monitored: true,
				ExternalIDs: []apiserver.ExternalIDRow{{Provider: "musicbrainz:recording", ExternalID: "rec-1"}}},
		},
		RootFolderIDs: []int64{rootVisible.ID},
	}

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/works/1"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}
	if fixture.workDetail.lastID != 1 {
		t.Errorf("GetWorkDetail called with id %d, want 1", fixture.workDetail.lastID)
	}

	var body struct {
		ID          int64  `json:"id"`
		Title       string `json:"title"`
		Year        *int   `json:"year"`
		ExternalIDs []struct {
			Provider   string `json:"provider"`
			ExternalID string `json:"external_id"`
		} `json:"external_ids"`
		Editions []struct {
			ID          int64  `json:"id"`
			Kind        string `json:"kind"`
			Label       string `json:"label"`
			IsDefault   bool   `json:"is_default"`
			ExternalIDs []struct {
				Provider   string `json:"provider"`
				ExternalID string `json:"external_id"`
			} `json:"external_ids"`
		} `json:"editions"`
		Parts []struct {
			ID       int64  `json:"id"`
			MediaRef string `json:"media_ref"`
			SortKey  string `json:"sort_key"`
		} `json:"parts"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if body.ID != 1 || body.Title != "KI Pecezinu" || body.Year == nil || *body.Year != 1997 {
		t.Fatalf("body = %+v, want id 1, title KI Pecezinu, year 1997", body)
	}
	if len(body.ExternalIDs) != 1 || body.ExternalIDs[0].Provider != "musicbrainz:release-group" {
		t.Errorf("external_ids = %+v, want the one work-level external id", body.ExternalIDs)
	}
	if len(body.Editions) != 1 || body.Editions[0].Label != "release-1" || len(body.Editions[0].ExternalIDs) != 1 {
		t.Errorf("editions = %+v, want the one edition with its own external id", body.Editions)
	}
	if len(body.Parts) != 1 || body.Parts[0].MediaRef != "01" {
		t.Errorf("parts = %+v, want the one part", body.Parts)
	}
}

func TestGetWork_MalformedIDAnswers400(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/works/not-a-number"))
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a non-numeric id", recorder.Code)
	}
}

func TestGetWork_MissingWorkAnswers404(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	fixture.workDetail.err = sql.ErrNoRows

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/works/404"))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for an id that does not exist", recorder.Code)
	}
}
