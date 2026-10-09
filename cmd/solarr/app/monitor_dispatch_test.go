package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// monitor posts a scope and fails the test on anything but 200.
func (g goldenServer) monitor(t *testing.T, target, body string) {
	t.Helper()
	if status, answer := g.post(t, target, body); status != http.StatusOK {
		t.Fatalf("POST %s %s = %d, body = %s", target, body, status, answer)
	}
}

type monitoredWork struct {
	Groups []struct {
		Ref       string `json:"ref"`
		Monitored bool   `json:"monitored"`
	} `json:"groups"`
}

type monitoredParts struct {
	Items []struct {
		ID        int64  `json:"id"`
		MediaRef  string `json:"media_ref"`
		Monitored bool   `json:"monitored"`
	} `json:"items"`
}

func (g goldenServer) monitored(t *testing.T, workID int64) (groups, parts map[string]bool, partIDs map[string]int64) {
	t.Helper()
	var work monitoredWork
	if err := json.Unmarshal(g.get(t, fmt.Sprintf("/works/%d", workID)), &work); err != nil {
		t.Fatal(err)
	}
	var page monitoredParts
	if err := json.Unmarshal(g.get(t, fmt.Sprintf("/works/%d/parts?limit=200", workID)), &page); err != nil {
		t.Fatal(err)
	}
	groups, parts, partIDs = map[string]bool{}, map[string]bool{}, map[string]int64{}
	for _, group := range work.Groups {
		groups[group.Ref] = group.Monitored
	}
	for _, part := range page.Items {
		parts[part.MediaRef], partIDs[part.MediaRef] = part.Monitored, part.ID
	}
	return groups, parts, partIDs
}

// One monitor for every media type: a season with its episodes, one episode, a monitor option, an album.
func TestMonitorWork_ReachesGroupsPartsAndOptionsOfEveryType(t *testing.T) {
	server := newGoldenServer(t)
	ids := server.seedLibraries(t)
	series := fmt.Sprintf("/works/%d/monitor", ids.series)

	server.monitor(t, series, `{"monitored":false,"group":"s02"}`)
	groups, parts, partIDs := server.monitored(t, ids.series)
	if groups["s02"] || parts["s02:e01"] || !groups["s01"] || !parts["s01:e01"] {
		t.Fatalf("after unmonitoring season 2: groups %v, parts %v", groups, parts)
	}
	server.monitor(t, series, fmt.Sprintf(`{"monitored":false,"parts":[%d]}`, partIDs["s01:e02"]))
	if _, parts, _ = server.monitored(t, ids.series); parts["s01:e02"] || !parts["s01:e01"] {
		t.Errorf("after unmonitoring one episode: parts %v", parts)
	}
	server.monitor(t, series, `{"option":"all"}`)
	if groups, parts, _ = server.monitored(t, ids.series); !groups["s02"] || !parts["s02:e01"] || !parts["s01:e02"] {
		t.Errorf("after the all option: groups %v, parts %v", groups, parts)
	}

	album := fmt.Sprintf("/works/%d/monitor", ids.album)
	server.monitor(t, album, `{"monitored":false}`)
	if groups, _, _ = server.monitored(t, ids.album); groups[""] {
		t.Errorf("after unmonitoring the album: groups %v", groups)
	}
	if status, _ := server.post(t, album, `{"option":"all"}`); status != http.StatusBadRequest {
		t.Errorf("a monitor option on an album: status %d, want 400 (music has none)", status)
	}
}
