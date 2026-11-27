package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// ProbeClientFactory builds a short-timeout client for one address a discovery probe tries; injected so this package builds none.
type ProbeClientFactory func(baseURL string) *httpx.Client

// TestConnection asks an *Arr for its version with the key as typed, before any connection is stored, and says in
// a word what is wrong when it cannot: the key, the address or the port.
func (a ArrSync) TestConnection(ctx context.Context, app settings.ArrApp, baseURL, apiKey string) apiserver.ArrConnectionTest {
	client := a.Lidarr(strings.TrimSpace(baseURL), strings.TrimSpace(apiKey))
	var status arrimport.LidarrStatus
	var err error
	switch app {
	case settings.ArrAppSonarr:
		status, err = client.Sonarr().SystemStatus(ctx)
	case settings.ArrAppRadarr:
		status, err = client.Radarr().SystemStatus(ctx)
	case settings.ArrAppReadarr:
		status, err = client.Readarr().SystemStatus(ctx)
	case settings.ArrAppWhisparr:
		status, err = client.Whisparr().SystemStatus(ctx)
	case settings.ArrAppKapowarr:
		status, err = client.Kapowarr().SystemStatus(ctx)
	case settings.ArrAppMylar:
		status, err = client.Mylar().SystemStatus(ctx)
	default:
		status, err = client.SystemStatus(ctx)
	}
	if err != nil {
		return apiserver.ArrConnectionTest{Problem: classifyArrProblem(err), Message: httpx.Explain(err)}
	}
	name := status.AppName
	if name == "" {
		name = string(app)
	}
	return apiserver.ArrConnectionTest{OK: true, AppName: name, Version: status.Version}
}

// classifyArrProblem reads a failed call as what the person has to fix.
func classifyArrProblem(err error) string {
	var status *httpx.StatusError
	var dns *net.DNSError
	var syntax *json.SyntaxError
	var typeMismatch *json.UnmarshalTypeError
	switch {
	case errors.As(err, &status) && (status.Status == http.StatusUnauthorized || status.Status == http.StatusForbidden):
		return apiserver.ArrProblemWrongKey
	case errors.As(err, &status) && status.Status == http.StatusNotFound,
		errors.As(err, &syntax), errors.As(err, &typeMismatch), strings.Contains(err.Error(), "decode response"):
		return apiserver.ArrProblemWrongPort
	case errors.As(err, &dns), errors.Is(err, syscall.ECONNREFUSED), errors.Is(err, syscall.EHOSTUNREACH),
		errors.Is(err, syscall.ENETUNREACH), errors.Is(err, context.DeadlineExceeded), isTimeout(err):
		return apiserver.ArrProblemUnreachable
	}
	return apiserver.ArrProblemOther
}

func isTimeout(err error) bool {
	var network net.Error
	return errors.As(err, &network) && network.Timeout()
}

// discoveryTarget is one app on its usual port and the way to recognise it without a key.
type discoveryTarget struct {
	kind string
	port int
	path string
	// match reads the answer; ok says it is this app and version is what it reports.
	match func(status int, body []byte) (version string, ok bool)
}

func arrPing(status int, body []byte) (string, bool) {
	var ping struct {
		Status string `json:"status"`
	}
	return "", status == http.StatusOK && json.Unmarshal(body, &ping) == nil && strings.EqualFold(ping.Status, "ok")
}

func sabnzbdVersion(status int, body []byte) (string, bool) {
	var answer struct {
		Version string `json:"version"`
	}
	return answer.Version, status == http.StatusOK && json.Unmarshal(body, &answer) == nil && answer.Version != ""
}

// qbittorrentVersion accepts a version number, or the plain Forbidden it answers without a login.
func qbittorrentVersion(status int, body []byte) (string, bool) {
	text := strings.TrimSpace(string(body))
	if status == http.StatusForbidden {
		return "", true
	}
	return text, status == http.StatusOK && text != "" && strings.Trim(text, "0123456789.") == ""
}

// discoveryTargets are the apps the setup assistant looks for, on the ports their own images use.
var discoveryTargets = []discoveryTarget{
	{"lidarr", 8686, "/ping", arrPing}, {"sonarr", 8989, "/ping", arrPing}, {"radarr", 7878, "/ping", arrPing},
	{"readarr", 8787, "/ping", arrPing}, {"whisparr", 6969, "/ping", arrPing}, {"prowlarr", 9696, "/ping", arrPing},
	{"sabnzbd", 8080, "/api?mode=version&output=json", sabnzbdVersion},
	{"qbittorrent", 8080, "/api/v2/app/webapiVersion", qbittorrentVersion},
}

// discoveryHosts are where a container is usually reachable: the app's own container name, this machine, the Docker host.
func discoveryHosts(kind string) []string {
	return []string{kind, "localhost", "host.docker.internal", "172.17.0.1"}
}

// discoveryParallel keeps the probes few enough not to look like a scan.
const discoveryParallel = 8

// Discover tries the usual address of each app and answers those that reply, once per address and kind.
func (a ArrSync) Discover(ctx context.Context) []apiserver.DiscoveredService {
	if a.Probe == nil {
		return nil
	}
	type probe struct {
		target discoveryTarget
		host   string
	}
	var probes []probe
	for _, target := range discoveryTargets {
		for _, host := range discoveryHosts(target.kind) {
			probes = append(probes, probe{target, host})
		}
	}
	found := make([]*apiserver.DiscoveredService, len(probes))
	slots := make(chan struct{}, discoveryParallel)
	var group sync.WaitGroup
	for index, entry := range probes {
		group.Add(1)
		slots <- struct{}{}
		go func() {
			defer group.Done()
			defer func() { <-slots }()
			found[index] = a.probe(ctx, entry.target, entry.host)
		}()
	}
	group.Wait()
	// One answer per app: the probes are in order of how specific the address is, and one app answers on several of them.
	var services []apiserver.DiscoveredService
	seen := map[string]bool{}
	for _, service := range found {
		if service == nil || seen[service.Kind] {
			continue
		}
		seen[service.Kind] = true
		services = append(services, *service)
	}
	return services
}

// probe asks one address; nil when nothing there is the app.
func (a ArrSync) probe(ctx context.Context, target discoveryTarget, host string) *apiserver.DiscoveredService {
	base := "http://" + net.JoinHostPort(host, strconv.Itoa(target.port))
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+target.path, nil)
	if err != nil {
		return nil
	}
	status := http.StatusOK
	var body []byte
	response, err := a.Probe(base).Do(ctx, request)
	if err != nil {
		var refused *httpx.StatusError
		if !errors.As(err, &refused) {
			return nil
		}
		status = refused.Status
	} else {
		defer func() { _ = response.Body.Close() }()
		body, _ = io.ReadAll(io.LimitReader(response.Body, 4096))
	}
	version, matched := target.match(status, body)
	if !matched {
		return nil
	}
	return &apiserver.DiscoveredService{Kind: target.kind, Host: host, Port: target.port, BaseURL: base, Version: version}
}
