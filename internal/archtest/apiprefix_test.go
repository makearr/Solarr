package archtest_test

import (
	"os"
	"regexp"
	"testing"
)

// The web client's base URL and the spec's server URL must agree. Getting this
// wrong does not fail loudly: an unknown path falls through to the SPA handler,
// which answers index.html with a 200, so every request succeeds and yields no
// data. It shipped once and cost a first-run wizard that never appeared and
// lists that rendered empty against a populated database.
//
// Checked here rather than in the frontend suite because the Docker web stage
// copies only web/, so a test there cannot see the spec at all.
func TestWebClientPrefixMatchesTheSpec(t *testing.T) {
	spec, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatalf("read the spec: %v", err)
	}
	client, err := os.ReadFile("../../web/src/api/client.ts")
	if err != nil {
		t.Fatalf("read the web client: %v", err)
	}

	declared := regexp.MustCompile(`(?m)^servers:\s*\n\s*-\s*url:\s*(\S+)\s*$`).FindSubmatch(spec)
	if declared == nil {
		t.Fatal("openapi.yaml declares no servers[0].url, so there is nothing to agree with")
	}
	used := regexp.MustCompile(`(?m)^export const API_PREFIX = "([^"]*)";`).FindSubmatch(client)
	if used == nil {
		t.Fatal("client.ts no longer exports API_PREFIX; the base URL is now unguarded")
	}

	if got, want := string(used[1]), string(declared[1]); got != want {
		t.Errorf("the web client calls %q but the spec serves %q; every request would fall "+
			"through to the interface handler and return index.html with a 200", got, want)
	}
}
