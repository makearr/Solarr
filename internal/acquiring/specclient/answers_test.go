package specclient_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/clienttest"
	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/acquiring/specclient"
	"github.com/makearr/Solarr/internal/platform/clock"
)

const queueRequest = "queue_status:\n  request: {path: /api, query: {mode: queue, output: json}}\n  fields:\n"

// withQueue is the SABnzbd port with its queue totals read from another kind of answer.
func withQueue(t *testing.T, request string, fields string) specclient.Definition {
	t.Helper()
	text := portText(t)
	start := strings.Index(text, queueRequest)
	end := strings.Index(text[start:], "    left: {selector: queue.mbleft")
	if start < 0 || end < 0 {
		t.Fatal("the port's queue_status block moved")
	}
	line := strings.Index(text[start+end:], "\n") + start + end + 1
	text = text[:start] + "queue_status:\n  request: " + request + "\n  fields:\n" + fields + text[line:]
	def, err := specclient.Parse("sabnzbd-port", "x", []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return def
}

func TestAnXmlAnswerIsReadByTheSameSelectorsAsJson(t *testing.T) {
	def := withQueue(t, "{path: /api, query: {mode: queue}, answer: xml}",
		"    paused: {selector: queue.paused}\n    speed: {selector: queue.kbpersec, filters: [{name: scale, args: [\"1024\"]}]}\n    items: {selector: queue.slots.@count}\n    left: {selector: queue.mbleft, filters: [{name: scale, args: [\"1048576\"]}]}\n")
	server := &clienttest.Server{Routes: map[string]clienttest.Route{"/api": {Body: `<?xml version="1.0"?><queue><paused>true</paused><kbpersec>2</kbpersec><mbleft>3</mbleft><slots count="4"/></queue>`}}}
	client := specclient.New(def, testClient(t, server), clock.NewFakeAtEpoch(), "http://sabnzbd.local:8080", "the-key")
	got, err := client.QueueStatus(t.Context())
	want := download.QueueStatus{Paused: true, SpeedBytesPerSec: 2048, Items: 4, SizeLeftBytes: 3 * 1048576}
	if err != nil || got != want {
		t.Fatalf("queue = %+v, %v; want %+v", got, err, want)
	}
}

func TestAnHtmlAnswerIsReadBySelectors(t *testing.T) {
	def := withQueue(t, "{path: /status, answer: html}",
		"    paused: {selector: \"span.paused\"}\n    speed: {selector: \"span.speed\", filters: [{name: scale, args: [\"1024\"]}]}\n    items: {selector: \"ul li:nth-child(2)\"}\n    left: {selector: \"span.left\", attribute: data-mb, filters: [{name: scale, args: [\"1048576\"]}]}\n")
	server := &clienttest.Server{Routes: map[string]clienttest.Route{"/status": {Body: `<html><body><span class="paused">false</span><span class="speed">5</span><span class="left" data-mb="7">7 MB</span><ul><li>1</li><li>9</li></ul></body></html>`}}}
	client := specclient.New(def, testClient(t, server), clock.NewFakeAtEpoch(), "http://sabnzbd.local:8080", "the-key")
	got, err := client.QueueStatus(t.Context())
	want := download.QueueStatus{SpeedBytesPerSec: 5120, Items: 9, SizeLeftBytes: 7 * 1048576}
	if err != nil || got != want {
		t.Fatalf("queue = %+v, %v; want %+v", got, err, want)
	}
	if _, err := specclient.Parse("x", "x", []byte(strings.Replace(portText(t), "{path: /api, query: {mode: version, output: json}}", "{path: /api, query: {mode: version}, answer: yaml}", 1))); err == nil || !strings.Contains(err.Error(), "json, xml or html") {
		t.Fatalf("an unknown answer kind: %v", err)
	}
}

func loginDefinition(t *testing.T) specclient.Definition {
	t.Helper()
	text := strings.Replace(portText(t), "auth:\n  type: query\n  name: apikey", "auth:\n  type: login\n  login: {method: POST, path: /login, body: \"user={{.Settings.user}}&pass={{.Settings.secret}}\", body_type: form}", 1)
	text = strings.Replace(text, "categories: true\n", "categories: true\nuser_name: true\n", 1)
	def, err := specclient.Parse("sabnzbd-port", "x", []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return def
}

func TestALoginSessionIsKeptAndMadeAgainWhenItExpires(t *testing.T) {
	logins, session := 0, "s1"
	server := &clienttest.Server{Answer: func(request clienttest.Request) (clienttest.Route, bool) {
		if request.Path == "/login" {
			logins++
			if request.Method != http.MethodPost || request.Body != "user=ann&pass=pw" {
				return clienttest.Route{Status: http.StatusOK, Body: "Fails."}, true
			}
			return clienttest.Route{Body: "Ok.", Header: http.Header{"Set-Cookie": {"sid=" + session + "; Path=/"}}}, true
		}
		if !strings.Contains(request.Header.Get("Cookie"), "sid="+session) {
			return clienttest.Route{Status: http.StatusForbidden}, true
		}
		return clienttest.Route{Body: `{"queue":{"paused":false,"kbpersec":"1","noofslots_total":2,"mbleft":"1"}}`}, true
	}}
	client := specclient.New(loginDefinition(t), testClient(t, server), clock.NewFakeAtEpoch(), "http://sabnzbd.local:8080", "ann:pw")
	for round := 0; round < 2; round++ {
		if got, err := client.QueueStatus(t.Context()); err != nil || got.Items != 2 {
			t.Fatalf("round %d: %+v, %v", round, got, err)
		}
	}
	if logins != 1 {
		t.Fatalf("logins = %d, want one for two calls", logins)
	}
	session = "s2"
	if got, err := client.QueueStatus(t.Context()); err != nil || got.Items != 2 {
		t.Fatalf("after the session expired: %+v, %v", got, err)
	}
	if logins != 2 {
		t.Fatalf("logins = %d, want a second after the expiry", logins)
	}
	wrong := specclient.New(loginDefinition(t), testClient(t, server), clock.NewFakeAtEpoch(), "http://sabnzbd.local:8080", "ann:nope")
	if _, err := wrong.QueueStatus(t.Context()); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("a refused login: %v", err)
	}
}
