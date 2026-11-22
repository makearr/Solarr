package apiserver_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/secrets"
)

type mediaServerBody struct {
	ID         int64  `json:"id"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	PushFiles  bool   `json:"push_files"`
	Credential struct {
		HasValue bool `json:"has_value"`
	} `json:"credential"`
	Libraries []struct {
		CategoryID      int64  `json:"category_id"`
		ServerLibraryID string `json:"server_library_id"`
	} `json:"libraries"`
	Paths []struct {
		SolarrPrefix string `json:"solarr_prefix"`
		ServerPrefix string `json:"server_prefix"`
	} `json:"paths"`
}

func mediaServerFixture(t *testing.T) (worksFixture, []*http.Cookie) {
	t.Helper()
	fixture := newWorksFixture(t)
	cipher, err := secrets.New(testArrCipherKey())
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	fixture.cache.EnableSecrets(cipher)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	return fixture, fixture.signIn(t, "admin")
}

func TestAddMediaServer_SealsTheCredentialAndMapsLibrariesAndPaths(t *testing.T) {
	fixture, cookies := mediaServerFixture(t)
	category, _ := fixture.createCategoryWithRoot(t, "Albums", "/music")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/media-servers",
		`{"kind":"jellyfin","name":"Den","base_url":"http://den.local:8096","credential":"the-real-key","push_files":true}`))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if bytes.Contains(recorder.Body.Bytes(), []byte("the-real-key")) {
		t.Error("the response echoes the credential")
	}
	var created mediaServerBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if created.Kind != "jellyfin" || !created.PushFiles || !created.Credential.HasValue {
		t.Errorf("created = %+v", created)
	}

	libraries := fmt.Sprintf(`[{"category_id":%d,"server_library_id":"abc","server_library":"Albums"}]`, category.ID)
	recorder = fixture.do(authedPut(t, cookies, fmt.Sprintf("/api/v1/media-servers/%d/libraries", created.ID), libraries))
	if recorder.Code != http.StatusOK {
		t.Fatalf("libraries status = %d, body = %s", recorder.Code, recorder.Body)
	}
	recorder = fixture.do(authedPut(t, cookies, fmt.Sprintf("/api/v1/media-servers/%d/paths", created.ID), `[{"solarr_prefix":"/music","server_prefix":"/data/music"}]`))
	var saved mediaServerBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &saved); err != nil || recorder.Code != http.StatusOK {
		t.Fatalf("paths status = %d, err %v, body = %s", recorder.Code, err, recorder.Body)
	}
	if len(saved.Libraries) != 1 || saved.Libraries[0].ServerLibraryID != "abc" || len(saved.Paths) != 1 || saved.Paths[0].ServerPrefix != "/data/music" {
		t.Errorf("saved = %+v, want one library and one path", saved)
	}
}

func TestSetMediaServerLibraries_RefusesALibraryTheKindDoesNotTake(t *testing.T) {
	fixture, cookies := mediaServerFixture(t)
	category, _ := fixture.createCategoryWithRoot(t, "Albums", "/music")
	recorder := fixture.do(authedPost(t, cookies, "/api/v1/media-servers",
		`{"kind":"kavita","name":"Shelf","base_url":"http://shelf.local:5000","credential":"k"}`))
	var created mediaServerBody
	_ = json.Unmarshal(recorder.Body.Bytes(), &created)

	recorder = fixture.do(authedPut(t, cookies, fmt.Sprintf("/api/v1/media-servers/%d/libraries", created.ID),
		fmt.Sprintf(`[{"category_id":%d,"server_library_id":"1"}]`, category.ID)))
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422, body = %s", recorder.Code, recorder.Body)
	}
}

func TestAddMediaServer_RefusesAnUnknownKindAndANonAdministrator(t *testing.T) {
	fixture, cookies := mediaServerFixture(t)
	recorder := fixture.do(authedPost(t, cookies, "/api/v1/media-servers", `{"kind":"plex","name":"x","base_url":"http://x","credential":"k"}`))
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("unknown kind: status = %d, want 400", recorder.Code)
	}
	fixture.createUser(t, "guest", settings.RoleUser)
	guest := fixture.signIn(t, "guest")
	recorder = fixture.do(authedGet(t, guest, "/api/v1/media-servers"))
	if recorder.Code != http.StatusForbidden {
		t.Errorf("non-administrator: status = %d, want 403", recorder.Code)
	}
}
