package apiserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

const memoryConfigYAML = "# a marker comment\nversion: 1\n\nserver:\n  bind: 0.0.0.0\n  port: 5454\n  base_url: /\n\ndatabase:\n  cache_size_kb: 65536\n"

func writeMemoryConfig(t *testing.T, fsys *fsx.Mem) {
	t.Helper()
	if err := fsys.MkdirAll("/config", 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := fsys.Create("/config/config.yml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte(memoryConfigYAML)); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSetDatabaseMemory_WritesConfigYAMLAndReportsTheNewSetting(t *testing.T) {
	fixture := newWorksFixtureWithConfigPath(t, "/config/config.yml")
	writeMemoryConfig(t, fixture.fsys)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPut(t, cookies, "/api/v1/cache/memory", `{"cache_size_kb":131072}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var body struct {
		ConfiguredKB int64 `json:"configured_kb"`
		DefaultKB    int64 `json:"default_kb"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ConfiguredKB != 131072 || body.DefaultKB != 65536 {
		t.Errorf("body = %+v; want configured 131072 and default 65536", body)
	}
	want := strings.Replace(memoryConfigYAML, "cache_size_kb: 65536", "cache_size_kb: 131072", 1)
	if got := readFile(t, fixture.fsys, "/config/config.yml"); got != want {
		t.Errorf("config.yml = %q; want only the one line changed", got)
	}
}

func TestSetDatabaseMemory_RefusesASizeOutsideTheBounds(t *testing.T) {
	fixture := newWorksFixtureWithConfigPath(t, "/config/config.yml")
	writeMemoryConfig(t, fixture.fsys)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	for _, size := range []string{"0", "1024", "99999999999"} {
		recorder := fixture.do(authedPut(t, cookies, "/api/v1/cache/memory", `{"cache_size_kb":`+size+`}`))
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("size %s: status = %d, want 400", size, recorder.Code)
		}
	}
	if got := readFile(t, fixture.fsys, "/config/config.yml"); got != memoryConfigYAML {
		t.Errorf("config.yml changed after refused sizes: %q", got)
	}
}
