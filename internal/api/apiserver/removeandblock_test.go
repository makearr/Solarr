package apiserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/makearr/MediaNexus/internal/api/apiserver"
	"github.com/makearr/MediaNexus/internal/db/settings"
)

// fakeRemoveAndBlocker records the request it was called with and answers a
// canned result or error, so a test can assert the handler's own translation
// (body decoding, 404 mapping) without a real library.db.
type fakeRemoveAndBlocker struct {
	result RemoveAndBlockResultStub
	err    error

	lastMediaObjectID int64
	lastReq           apiserver.RemoveAndBlockInput
	calls             int
}

// RemoveAndBlockResultStub avoids importing apiserver.RemoveAndBlockOutput's
// own name twice in this file's declarations -- same type, different name to
// keep the fake's own field self-explanatory.
type RemoveAndBlockResultStub = apiserver.RemoveAndBlockOutput

func (f *fakeRemoveAndBlocker) RemoveAndBlockFile(
	_ context.Context, mediaObjectID int64, req apiserver.RemoveAndBlockInput,
) (apiserver.RemoveAndBlockOutput, error) {
	f.calls++
	f.lastMediaObjectID = mediaObjectID
	f.lastReq = req
	if f.err != nil {
		return apiserver.RemoveAndBlockOutput{}, f.err
	}
	return f.result, nil
}

func TestRemoveAndBlockFile_RunsTheRequestedSteps(t *testing.T) {
	fixture := newWorksFixture(t)
	blockID := int64(9)
	fixture.removeAndBlocker.result = apiserver.RemoveAndBlockOutput{
		QuarantinedPaths: []string{"/media/root/quarantine/2026-09-05/track.flac"},
		BlockID:          &blockID, ReopenedItems: []int64{5},
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/files/42/remove-block",
		`{"quarantine":true,"block":true,"oshash":"abc123","reopen":true}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}

	if fixture.removeAndBlocker.lastMediaObjectID != 42 {
		t.Errorf("mediaObjectID = %d, want 42", fixture.removeAndBlocker.lastMediaObjectID)
	}
	req := fixture.removeAndBlocker.lastReq
	if !req.Quarantine || !req.Block || req.OSHash != "abc123" || !req.Reopen {
		t.Errorf("request = %+v, want quarantine/block/reopen true and oshash=abc123", req)
	}

	var body struct {
		QuarantinedPaths []string `json:"quarantined_paths"`
		BlockID          int64    `json:"block_id"`
		ReopenedItems    []int64  `json:"reopened_items"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.QuarantinedPaths) != 1 || body.BlockID != 9 || len(body.ReopenedItems) != 1 {
		t.Errorf("body = %+v, want the canned result", body)
	}
}

func TestRemoveAndBlockFile_NoSuchFileAnswers404(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.removeAndBlocker.err = apiserver.ErrNoSuchFile
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/files/999999/remove-block", `{"block":true}`))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a missing file, body = %s", recorder.Code, recorder.Body)
	}
}

func TestRemoveAndBlockFile_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/files/42/remove-block", `{"block":true}`))
	if recorder.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a non-administrator", recorder.Code)
	}
	if fixture.removeAndBlocker.calls != 0 {
		t.Error("RemoveAndBlockFile was called despite the caller not being an administrator")
	}
}

func TestRemoveAndBlockFile_BadJSONAnswers400(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/files/42/remove-block", `{not json`))
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for malformed JSON", recorder.Code)
	}
}

func TestRemoveAndBlockFile_RequiresAuthentication(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)

	recorder := fixture.do(authedPost(t, nil, "/api/v1/files/42/remove-block", `{"block":true}`))
	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for no credential", recorder.Code)
	}
}
