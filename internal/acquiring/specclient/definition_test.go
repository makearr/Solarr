package specclient_test

import (
	"os"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/specclient"
	"github.com/makearr/Solarr/internal/platform/clock"
)

func portText(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("testdata/sabnzbd.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestParseRefusesWhatCouldLeaveTheOwnersAddressOrLeaveStatesUnmapped(t *testing.T) {
	cases := []struct{ name, from, to, want string }{
		{"absolute path", "path: /api, query: {mode: version", "path: \"http://evil.example/api\", query: {mode: version", "must start with a single /"},
		{"scheme-relative path", "path: /api, query: {mode: version", "path: //evil.example/api, query: {mode: version", "must start with a single /"},
		{"userinfo path", "path: /api, query: {mode: version", "path: /@evil.example, query: {mode: version", "must start with a single /"},
		{"unmapped default", "default_state: queued", "default_state: sideways", "not a download state"},
		{"unknown mapped state", "Paused: paused", "Paused: asleep", "not a download state"},
		{"torrent protocol", "protocol: usenet", "protocol: torrent", "usenet only"},
		{"unknown key", "label: SABnzbd (definition)", "label: x\nunknown_key: 1", "unknown_key"},
		{"unknown filter", "name: scale, args: [\"1024\"]", "name: shout, args: [\"1024\"]", "unknown filter"},
		{"no remove", "remove:\n", "removed:\n", "unknown field"},
	}
	for _, one := range cases {
		text := strings.Replace(portText(t), one.from, one.to, 1)
		if text == portText(t) {
			t.Fatalf("%s: the replacement did not apply", one.name)
		}
		if _, err := specclient.Parse("sabnzbd-port", "x", []byte(text)); err == nil || !strings.Contains(err.Error(), one.want) {
			t.Errorf("%s: err = %v, want it to contain %q", one.name, err, one.want)
		}
	}
}

func TestTheSecretGoesWhereTheAuthSays(t *testing.T) {
	for _, one := range []struct{ auth, check string }{
		{"auth: {type: header, name: X-Api-Key, prefix: \"Key \"}", "header"},
		{"auth: {type: basic}", "basic"},
	} {
		text := strings.Replace(portText(t), "auth:\n  type: query\n  name: apikey", one.auth, 1)
		def, err := specclient.Parse("sabnzbd-port", "x", []byte(text))
		if err != nil {
			t.Fatal(err)
		}
		server := sabServer(sabAnswers)
		credentials := "the-key"
		if one.check == "basic" {
			credentials = "ann:pw"
		}
		client := specclient.New(def, testClient(t, server), clock.NewFakeAtEpoch(), "http://sabnzbd.local:8080", credentials)
		if _, err := client.QueueStatus(t.Context()); err != nil {
			t.Fatal(err)
		}
		seen := server.Seen[0]
		if strings.Contains(seen.Query, "apikey") || strings.Contains(seen.Query, "the-key") {
			t.Errorf("%s: the secret is in the address: %s", one.check, seen.Query)
		}
		if one.check == "header" && seen.Header.Get("X-Api-Key") != "Key the-key" {
			t.Errorf("header = %q", seen.Header.Get("X-Api-Key"))
		}
		if one.check == "basic" && !strings.HasPrefix(seen.Header.Get("Authorization"), "Basic ") {
			t.Errorf("authorization = %q", seen.Header.Get("Authorization"))
		}
	}
}

func TestAnAnswerThatIsNotJSONNamesItsStartWithoutTheSecret(t *testing.T) {
	server := sabServer(map[string]string{"queue": "API Key Incorrect"})
	client := specclient.New(loadPort(t), testClient(t, server), clock.NewFakeAtEpoch(), "http://sabnzbd.local:8080", "the-key")
	_, err := client.QueueStatus(t.Context())
	if err == nil || !strings.Contains(err.Error(), "API Key Incorrect") || strings.Contains(err.Error(), "the-key") {
		t.Errorf("err = %v", err)
	}
}

func TestAnAddressThatIsNotHTTPFailsEveryCall(t *testing.T) {
	client := specclient.New(loadPort(t), testClient(t, sabServer(sabAnswers)), clock.NewFakeAtEpoch(), "ftp://sabnzbd.local", "k")
	if _, err := client.QueueStatus(t.Context()); err == nil {
		t.Error("an ftp address was used")
	}
}
