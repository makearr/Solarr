package specprovider_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/specprovider"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// fake answers by URL substring and records what was asked.
type fake struct {
	mu       sync.Mutex
	answers  map[string]string
	status   int
	requests []*http.Request
}

func (f *fake) Do(_ context.Context, request *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, request)
	for part, body := range f.answers {
		if strings.Contains(request.URL.String(), part) {
			status := f.status
			if status == 0 {
				status = 200
			}
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
		}
	}
	return nil, &url.Error{Op: "Get", URL: request.URL.String(), Err: errors.New("connection refused")}
}

const jsonDefinition = `schema: 1
kind: metadata
key: sample-comics
name: Sample Comics
engine: http
media_types: [comic]
hosts: [api.example.org]
pace: {per_minute: 30, max_per_minute: 60}
settings:
  - {name: api_key, type: password, label: API key, required: true}
ids: {own: sample}
search:
  request:
    path: "https://api.example.org/search"
    query: {q: "{{ .Query.Title }}", key: "{{ .Settings.api_key }}"}
  rows: {selector: results}
  fields:
    id: {selector: id}
    title: {selector: name}
    year: {selector: date, filters: [{name: regexp, args: "(\\d{4})"}]}
item:
  request: {path: "https://api.example.org/item/{{ .ID }}"}
  fields:
    id: {selector: id}
    title: {selector: name}
    status: {selector: state, map: {Ongoing: continuing, Done: ended}}
    genres: {selector: genres, split: ","}
    rating: {selector: score}
  parts:
    rows: {selector: issues}
    fields: {number: {selector: no}, title: {selector: name}, date: {selector: released}}
  people:
    rows: {selector: staff}
    fields: {name: {selector: name}, role: {selector: job}}
  images:
    rows: {selector: covers}
    fields: {url: {selector: url}, kind: {text: poster}}
`

func provider(t *testing.T, source string, doer *fake) *specprovider.Provider {
	t.Helper()
	def, err := specprovider.Parse("sample-comics", "Sample Comics", []byte(source))
	if err != nil {
		t.Fatal(err)
	}
	return specprovider.New(def, doer, clock.NewFakeAtEpoch(), func() map[string]string { return map[string]string{"api_key": "sekret"} })
}

func TestSearchReadsRowsIntoRecordsAndSendsTheSettingOnlyToTheDeclaredHost(t *testing.T) {
	doer := &fake{answers: map[string]string{"/search": `{"results":[{"id":7,"name":"Example Hero","date":"2019-05-01"},{"id":8,"name":"Example Hero Returns","date":"2021"}]}`}}
	got, err := provider(t, jsonDefinition, doer).Search(t.Context(), specprovider.Query{Title: "Example Hero"})
	if err != nil || len(got) != 2 || got[0].IDs["sample"] != "7" || got[0].Title != "Example Hero" || got[0].Year != 2019 || got[1].Year != 2021 {
		t.Fatalf("records = %+v, %v", got, err)
	}
	if query := doer.requests[0].URL.Query(); query.Get("q") != "Example Hero" || query.Get("key") != "sekret" {
		t.Errorf("query = %v", query)
	}
}

func TestItemReadsFieldsPartsPeopleAndImagesAndDropsAnUnmappedStatus(t *testing.T) {
	doer := &fake{answers: map[string]string{"/item/7": `{"id":7,"name":"Example Hero","state":"Hiatus","genres":"Action, Comedy","score":"8.5",
	 "issues":[{"no":1,"name":"Origins","released":"2019-05-01"}],"staff":[{"name":"A. Writer","job":"writer"}],"covers":[{"url":"https://img.example.org/1.jpg"}]}`}}
	got, err := provider(t, jsonDefinition, doer).Item(t.Context(), "7")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Example Hero" || got.Status != "" || got.Dropped != 1 || len(got.Genres) != 2 || got.Genres[1] != "Comedy" || got.Rating.Value != 8.5 ||
		len(got.Parts) != 1 || got.Parts[0].Number != 1 || got.Parts[0].Title != "Origins" || len(got.People) != 1 || got.People[0].Role != "writer" ||
		len(got.Images) != 1 || got.Images[0].Kind != "poster" || got.Images[0].URL != "https://img.example.org/1.jpg" {
		t.Errorf("record = %+v", got)
	}
}

func TestAMappedStatusIsKept(t *testing.T) {
	doer := &fake{answers: map[string]string{"/item/7": `{"id":7,"name":"X","state":"Ongoing"}`}}
	got, err := provider(t, jsonDefinition, doer).Item(t.Context(), "7")
	if err != nil || got.Status != "continuing" || got.Dropped != 0 {
		t.Errorf("record = %+v, %v", got, err)
	}
}

func TestHTMLAndXMLAnswersBecomeRecords(t *testing.T) {
	htmlDefinition := strings.NewReplacer("engine: http", "engine: http").Replace(`schema: 1
kind: metadata
key: sample-html
name: Sample
engine: http
hosts: [site.example.org]
pace: {per_minute: 20}
item:
  request: {path: "https://site.example.org/g/{{ .ID }}"}
  response: html
  fields:
    title: {selector: "h1.title"}
    tags: {selector: "span.tag", split: "|"}
`)
	doer := &fake{answers: map[string]string{"/g/9": `<html><h1 class="title">Example Gallery</h1><span class="tag">a|b</span></html>`}}
	def, err := specprovider.Parse("sample-html", "Sample", []byte(htmlDefinition))
	if err != nil {
		t.Fatal(err)
	}
	got, err := specprovider.New(def, doer, clock.NewFakeAtEpoch(), nil).Item(t.Context(), "9")
	if err != nil || got.Title != "Example Gallery" || len(got.Tags) != 2 {
		t.Errorf("html record = %+v, %v", got, err)
	}
	xmlDefinition := `schema: 1
kind: metadata
key: sample-xml
name: Sample
engine: http
hosts: [site.example.org]
pace: {per_minute: 20}
item:
  request: {path: "https://site.example.org/x/{{ .ID }}"}
  response: xml
  fields:
    title: {selector: "anime.title"}
    year: {selector: "anime.@year"}
`
	doer = &fake{answers: map[string]string{"/x/3": `<anime year="2004"><title>Example Show</title></anime>`}}
	def, err = specprovider.Parse("sample-xml", "Sample", []byte(xmlDefinition))
	if err != nil {
		t.Fatal(err)
	}
	got, err = specprovider.New(def, doer, clock.NewFakeAtEpoch(), nil).Item(t.Context(), "3")
	if err != nil || got.Title != "Example Show" || got.Year != 2004 {
		t.Errorf("xml record = %+v, %v", got, err)
	}
}

func TestATwoStepOperationFeedsTheFirstAnswerToTheSecondRequest(t *testing.T) {
	source := `schema: 1
kind: metadata
key: sample-steps
name: Sample
engine: http
hosts: [api.example.org]
pace: {per_minute: 20}
item:
  steps:
    - name: first
      request: {path: "https://api.example.org/token"}
      fields: {token: {selector: access}}
  request: {path: "https://api.example.org/item/{{ .ID }}", headers: {Authorization: "Bearer {{ .Step.first.token }}"}}
  fields: {title: {selector: name}}
`
	doer := &fake{answers: map[string]string{"/token": `{"access":"t0k"}`, "/item/5": `{"name":"Example"}`}}
	def, err := specprovider.Parse("sample-steps", "Sample", []byte(source))
	if err != nil {
		t.Fatal(err)
	}
	got, err := specprovider.New(def, doer, clock.NewFakeAtEpoch(), nil).Item(t.Context(), "5")
	if err != nil || got.Title != "Example" || doer.requests[1].Header.Get("Authorization") != "Bearer t0k" {
		t.Errorf("record = %+v, %v", got, err)
	}
}

func TestParseRefusesWhatTheSafetyModelForbids(t *testing.T) {
	base := "schema: 1\nkind: metadata\nkey: sample-bad\nname: Bad\nengine: http\nhosts: [api.example.org]\npace: {per_minute: 20}\n"
	cases := map[string]string{
		"an undeclared literal host":        base + "item:\n  request: {path: \"https://elsewhere.example.net/x\"}\n  fields: {title: {selector: a}}\n",
		"a relative path":                   base + "item:\n  request: {path: \"/x\"}\n  fields: {title: {selector: a}}\n",
		"a field that is not in the record": base + "item:\n  request: {path: \"https://api.example.org/x\"}\n  fields: {colour: {selector: a}}\n",
		"an unknown key":                    base + "scripts: [run]\nitem:\n  request: {path: \"https://api.example.org/x\"}\n  fields: {title: {selector: a}}\n",
		"no pace":                           strings.Replace(base, "pace: {per_minute: 20}\n", "", 1) + "item:\n  request: {path: \"https://api.example.org/x\"}\n  fields: {title: {selector: a}}\n",
		"nine requests": base + "item:\n  steps:\n" + strings.Repeat("    - {name: s, request: {path: \"https://api.example.org/x\"}}\n", 8) +
			"  request: {path: \"https://api.example.org/x\"}\n  fields: {title: {selector: a}}\n",
	}
	for name, source := range cases {
		if _, err := specprovider.Parse("sample-bad", "Bad", []byte(source)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestATemplatedHostOutsideTheDeclaredListIsRefusedAtRunTime(t *testing.T) {
	source := `schema: 1
kind: metadata
key: sample-host
name: Sample
engine: http
hosts: [api.example.org]
pace: {per_minute: 20}
item:
  request: {path: "https://{{ .ID }}/x"}
  fields: {title: {selector: a}}
`
	doer := &fake{answers: map[string]string{"": `{}`}}
	def, err := specprovider.Parse("sample-host", "Sample", []byte(source))
	if err != nil {
		t.Fatal(err)
	}
	_, err = specprovider.New(def, doer, clock.NewFakeAtEpoch(), nil).Item(t.Context(), "evil.example.net")
	var failure *specprovider.Error
	if !errors.As(err, &failure) || failure.Code != specprovider.CodeBlocked || len(doer.requests) != 0 {
		t.Errorf("error = %v, requests = %d", err, len(doer.requests))
	}
}

func TestStatusesAndOversizeAnswersBecomeStructuredErrors(t *testing.T) {
	cases := map[int]specprovider.Code{404: specprovider.CodeNotFound, 401: specprovider.CodeUnauthorised, 403: specprovider.CodeUnauthorised, 429: specprovider.CodeRateLimited, 500: specprovider.CodeBadAnswer}
	for status, want := range cases {
		doer := &fake{status: status, answers: map[string]string{"/item/1": `{}`}}
		_, err := provider(t, jsonDefinition, doer).Item(t.Context(), "1")
		var failure *specprovider.Error
		if !errors.As(err, &failure) || failure.Code != want {
			t.Errorf("status %d: error = %v, want %s", status, err, want)
		}
	}
	htmlSource := "schema: 1\nkind: metadata\nkey: sample-big\nname: Big\nengine: http\nhosts: [site.example.org]\npace: {per_minute: 20}\n" +
		"item:\n  request: {path: \"https://site.example.org/g/{{ .ID }}\"}\n  response: html\n  fields: {title: {selector: h1}}\n"
	def, err := specprovider.Parse("sample-big", "Big", []byte(htmlSource))
	if err != nil {
		t.Fatal(err)
	}
	doer := &fake{answers: map[string]string{"/g/1": "<h1>Example</h1>" + strings.Repeat("a", 9<<20)}}
	_, err = specprovider.New(def, doer, clock.NewFakeAtEpoch(), nil).Item(t.Context(), "1")
	var failure *specprovider.Error
	if !errors.As(err, &failure) || failure.Code != specprovider.CodeBadAnswer {
		t.Errorf("oversize: error = %v", err)
	}
}

func TestATransportFailureDoesNotLeakTheSecretInTheError(t *testing.T) {
	doer := &fake{answers: map[string]string{}}
	_, err := provider(t, jsonDefinition, doer).Search(t.Context(), specprovider.Query{Title: "x"})
	if err == nil || strings.Contains(err.Error(), "sekret") {
		t.Errorf("error = %v", err)
	}
}
