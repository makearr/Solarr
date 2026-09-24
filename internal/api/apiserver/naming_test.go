package apiserver_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
)

// fakeNamingSchemes declares one slot, "track", and renders its template as the example path.
type fakeNamingSchemes struct{}

func (fakeNamingSchemes) Scheme(mediaType string) (apiserver.NamingScheme, bool) {
	if mediaType != "music" {
		return apiserver.NamingScheme{}, false
	}
	return apiserver.NamingScheme{
		Slots:   []apiserver.NamingSlot{{Key: "track", Label: "Track Format", Help: "h"}},
		Tokens:  []apiserver.NamingToken{{Spelling: "{Track Title}", Group: "Track", Example: "Title", Help: "h"}},
		Presets: []apiserver.NamingPreset{{Name: "Default", Formats: map[string]string{"track": "{Track Title}"}}},
		Default: settings.NamingProfile{
			RenameFiles: true, ReplaceIllegalCharacters: true, ColonReplacement: "smart",
			Formats: map[string]string{"track": "{Track Title}"},
		},
	}, true
}

func (fakeNamingSchemes) Examples(_ string, profile settings.NamingProfile) ([]apiserver.NamingExample, []string) {
	if profile.Formats["track"] == "" {
		return nil, []string{"Track Format is empty"}
	}
	return []apiserver.NamingExample{{Label: "Example", Path: profile.Formats["track"]}}, nil
}

type namingBody struct {
	Naming struct {
		RenameFiles              bool              `json:"rename_files"`
		ReplaceIllegalCharacters bool              `json:"replace_illegal_characters"`
		ColonReplacement         string            `json:"colon_replacement"`
		Formats                  map[string]string `json:"formats"`
	} `json:"naming"`
	IsDefault bool `json:"is_default"`
	Scheme    struct {
		Slots []struct {
			Key string `json:"key"`
		} `json:"slots"`
	} `json:"scheme"`
	Examples []struct {
		Label string `json:"label"`
		Path  string `json:"path"`
	} `json:"examples"`
}

func decodeNaming(t *testing.T, raw []byte) namingBody {
	t.Helper()
	var body namingBody
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return body
}

func adminWithLibrary(t *testing.T) (worksFixture, []*http.Cookie, int64) {
	t.Helper()
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	category, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 10)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	return fixture, cookies, category.ID
}

func TestLibraryNaming_NeverSavedIsTheSchemeDefault(t *testing.T) {
	fixture, cookies, id := adminWithLibrary(t)

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/categories/"+strconv.FormatInt(id, 10)+"/naming"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, %s", recorder.Code, recorder.Body)
	}
	body := decodeNaming(t, recorder.Body.Bytes())
	if !body.IsDefault || body.Naming.Formats["track"] != "{Track Title}" || body.Naming.ColonReplacement != "smart" {
		t.Errorf("body = %+v, want the scheme default marked is_default", body)
	}
	if len(body.Scheme.Slots) != 1 || body.Scheme.Slots[0].Key != "track" {
		t.Errorf("slots = %+v", body.Scheme.Slots)
	}
	if len(body.Examples) != 1 || body.Examples[0].Path != "{Track Title}" {
		t.Errorf("examples = %+v", body.Examples)
	}
}

func TestLibraryNaming_SaveThenRead(t *testing.T) {
	fixture, cookies, id := adminWithLibrary(t)
	saved := `{"rename_files":false,"replace_illegal_characters":false,"colon_replacement":"dash",` +
		`"formats":{"track":"{track:00} {Track Title}"}}`

	recorder := fixture.do(authedPut(t, cookies, "/api/v1/categories/"+strconv.FormatInt(id, 10)+"/naming", saved))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, %s", recorder.Code, recorder.Body)
	}
	category, _ := fixture.cache.Snapshot().Category(id)
	want := settings.NamingProfile{
		RenameFiles: false, ReplaceIllegalCharacters: false, ColonReplacement: "dash",
		Formats: map[string]string{"track": "{track:00} {Track Title}"},
	}
	if category.Naming == nil || !reflect.DeepEqual(*category.Naming, want) {
		t.Errorf("stored %+v, want %+v", category.Naming, want)
	}
	if body := decodeNaming(t, recorder.Body.Bytes()); body.IsDefault {
		t.Error("a saved library still reports is_default")
	}
}

func TestLibraryNaming_RefusedSaveIsA400AndStoresNothing(t *testing.T) {
	fixture, cookies, id := adminWithLibrary(t)
	fixture.cache.EnableNamingValidation(func(string, settings.NamingProfile) error {
		return errors.New("Track Format: naming: unknown token(s): Episode Title")
	})

	body := `{"rename_files":true,"replace_illegal_characters":true,"colon_replacement":"smart",` +
		`"formats":{"track":"{Episode Title}"}}`
	recorder := fixture.do(authedPut(t, cookies, "/api/v1/categories/"+strconv.FormatInt(id, 10)+"/naming", body))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", recorder.Code, recorder.Body)
	}
	if !strings.Contains(recorder.Body.String(), "Episode Title") {
		t.Errorf("the problem does not name the bad token: %s", recorder.Body)
	}
	if category, _ := fixture.cache.Snapshot().Category(id); category.Naming != nil {
		t.Errorf("a refused save stored %+v", category.Naming)
	}
}

func TestLibraryNaming_ExamplesForUnsavedSettings(t *testing.T) {
	fixture, cookies, id := adminWithLibrary(t)
	body := `{"rename_files":true,"replace_illegal_characters":true,"colon_replacement":"smart","formats":{"track":""}}`

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/categories/"+strconv.FormatInt(id, 10)+"/naming/examples", body))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, %s", recorder.Code, recorder.Body)
	}
	if got := strings.TrimSpace(recorder.Body.String()); got != `{"examples":[],"problems":["Track Format is empty"]}` {
		t.Errorf("body = %s", got)
	}
	if category, _ := fixture.cache.Snapshot().Category(id); category.Naming != nil {
		t.Error("previewing examples saved the naming")
	}
}

func TestLibraryNaming_AdministratorOnly(t *testing.T) {
	fixture, _, id := adminWithLibrary(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	for _, request := range []*http.Request{
		authedGet(t, cookies, "/api/v1/categories/"+strconv.FormatInt(id, 10)+"/naming"),
		authedPut(t, cookies, "/api/v1/categories/"+strconv.FormatInt(id, 10)+"/naming", `{}`),
		authedPost(t, cookies, "/api/v1/categories/"+strconv.FormatInt(id, 10)+"/rename", `{"preview":true}`),
	} {
		if recorder := fixture.do(request); recorder.Code != http.StatusForbidden {
			t.Errorf("%s %s: status = %d, want 403", request.Method, request.URL.Path, recorder.Code)
		}
	}
}

func TestRenameLibrary_PassesTheLibraryAndPreview(t *testing.T) {
	fixture, cookies, id := adminWithLibrary(t)

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/categories/"+strconv.FormatInt(id, 10)+"/rename", `{"preview":true}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, %s", recorder.Code, recorder.Body)
	}
	starter := fixture.renameStarter
	if starter.libraryCalls != 1 || starter.lastCategoryID != id || !starter.lastPreview {
		t.Errorf("calls=%d category=%d preview=%v", starter.libraryCalls, starter.lastCategoryID, starter.lastPreview)
	}
	if !strings.Contains(recorder.Body.String(), `"root_folder_id":7`) {
		t.Errorf("body = %s, want the one root's plan", recorder.Body)
	}

	missing := fixture.do(authedPost(t, cookies, "/api/v1/categories/999/rename", `{"preview":true}`))
	if missing.Code != http.StatusNotFound {
		t.Errorf("unknown library: status = %d, want 404", missing.Code)
	}
}
