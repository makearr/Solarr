package arrimport_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/importing/arrimport"
)

type recordingTransport struct {
	responses map[string]string
	puts      map[string]string
}

func (r *recordingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method == http.MethodPut {
		body, _ := io.ReadAll(request.Body)
		r.puts[request.URL.Path] = string(body)
		return &http.Response{StatusCode: 202, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: request}, nil
	}
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(r.responses[request.URL.Path])), Request: request}, nil
}

func TestWhisparrReadsScenesPerformersAndStudiosByTheirStashIds(t *testing.T) {
	transport := &recordingTransport{responses: map[string]string{
		"/api/v3/movie":     `[{"id":1,"title":"A Scene","itemType":"scene","stashId":"s-1","foreignId":"s-1","studioTitle":"Night Studio","monitored":true,"hasFile":true,"rootFolderPath":"/porn"},{"id":2,"title":"Old","foreignId":"s-2"}]`,
		"/api/v3/performer": `[{"id":7,"fullName":"Ada Vale","foreignId":"p-1","monitored":true,"rootFolderPath":"/porn/"}]`,
		"/api/v3/studio":    `[{"id":9,"title":"Night Studio","foreignId":"st-1","tpdbId":"t-1","monitored":false}]`,
	}, puts: map[string]string{}}
	client := arrimport.NewLidarrClient(newTestClient(t, transport), "http://whisparr.local", "key").Whisparr()
	scenes, err := client.Items(t.Context())
	if err != nil || len(scenes) != 2 || scenes[0].Identifier() != "s-1" || scenes[1].Identifier() != "s-2" || !scenes[0].Monitored {
		t.Fatalf("scenes = %+v, %v", scenes, err)
	}
	performers, err := client.Performers(t.Context())
	if err != nil || len(performers) != 1 || performers[0].ForeignID != "p-1" {
		t.Errorf("performers = %+v, %v", performers, err)
	}
	studios, err := client.Studios(t.Context())
	if err != nil || len(studios) != 1 || studios[0].TPDBID != "t-1" || studios[0].Monitored {
		t.Errorf("studios = %+v, %v", studios, err)
	}
}

func TestSetMonitoredChangesTheOneFieldAndWritesTheRestBack(t *testing.T) {
	transport := &recordingTransport{responses: map[string]string{
		"/api/v3/movie/5": `{"id":5,"title":"Keep","monitored":false,"tags":[1,2],"movieFile":{"id":9}}`,
	}, puts: map[string]string{}}
	client := arrimport.NewLidarrClient(newTestClient(t, transport), "http://radarr.local", "key").Radarr()
	if err := client.SetMonitored(t.Context(), "/api/v3/movie", 5, true); err != nil {
		t.Fatal(err)
	}
	var written map[string]json.RawMessage
	if err := json.Unmarshal([]byte(transport.puts["/api/v3/movie/5"]), &written); err != nil {
		t.Fatalf("put body %q: %v", transport.puts["/api/v3/movie/5"], err)
	}
	if string(written["monitored"]) != "true" || string(written["title"]) != `"Keep"` || string(written["tags"]) != "[1,2]" || string(written["movieFile"]) != `{"id":9}` {
		t.Errorf("written = %v, want the same resource with only monitored changed", written)
	}
}
