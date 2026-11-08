package spec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/htmlindex"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// maxAnswerBytes bounds one page read from a site.
const maxAnswerBytes = 8 << 20

// maxRows bounds the releases read from one answer.
const maxRows = 500

// ErrLoginFailed means the site refused the sign-in; the message is the site's own when it gave one.
var ErrLoginFailed = errors.New("spec: sign-in refused")

// Driver searches one site by its spec. Settings holds what the owner filled in (user name, password, cookie, choices).
type Driver struct {
	spec     Spec
	http     *httpx.Client
	clock    clock.Clock
	baseURL  string
	settings map[string]string
}

var _ indexer.Driver = (*Driver)(nil)

// New builds the driver. baseURL is one of the spec's links, or the owner's own address.
func New(spec Spec, client *httpx.Client, cl clock.Clock, baseURL string, settings map[string]string) *Driver {
	if !strings.HasSuffix(baseURL, "/") {
		baseURL += "/"
	}
	return &Driver{spec: spec, http: client, clock: cl, baseURL: baseURL, settings: settings}
}

// Capabilities answers the categories and search modes of the spec, and proves the sign-in when the site needs one.
func (d *Driver) Capabilities(ctx context.Context) (indexer.Capabilities, error) {
	if d.spec.Login != nil && d.spec.Login.Method != "" && d.spec.Login.Method != "cookie" {
		if err := d.login(ctx); err != nil {
			return indexer.Capabilities{}, err
		}
	}
	if d.spec.Login == nil || d.spec.Login.Method == "" || d.spec.Login.Method == "none" {
		// Nothing to sign in to: reaching the site is the whole proof.
		if _, err := d.fetch(ctx, http.MethodGet, d.baseURL, nil, nil); err != nil {
			return indexer.Capabilities{}, err
		}
	}
	return indexer.Capabilities{Categories: d.spec.Categories(), Search: d.spec.SearchModes()}, nil
}

func (d *Driver) variables() *Variables {
	variables := newVariables(d.clock.Now())
	for _, setting := range d.spec.Settings {
		switch setting.Type {
		case "checkbox":
			value, set := d.settings[setting.Name]
			if !set {
				value = setting.Default
			}
			variables.Config[setting.Name] = value == "true" || value == "True"
		case "text", "password", "select":
			value, set := d.settings[setting.Name]
			if !set {
				value = setting.Default
			}
			variables.Config[setting.Name] = value
		}
	}
	for name, value := range d.settings {
		if _, known := variables.Config[name]; !known {
			variables.Config[name] = value
		}
	}
	variables.Config["sitelink"] = d.baseURL
	return variables
}

// resolve makes a path from a spec absolute against the site.
func (d *Driver) resolve(path string) (string, error) {
	base, err := url.Parse(d.baseURL)
	if err != nil {
		return "", err
	}
	target, err := url.Parse(path)
	if err != nil {
		return "", err
	}
	return base.ResolveReference(target).String(), nil
}

type page struct {
	status int
	body   []byte
	url    string
}

// fetch sends one request and reads the answer as UTF-8. A form is sent as the body of a POST.
func (d *Driver) fetch(ctx context.Context, method, target string, form url.Values, headers map[string][]string) (page, error) {
	var body io.Reader
	if method == http.MethodPost {
		body = strings.NewReader(d.encodeValues(form))
	}
	request, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return page{}, err
	}
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for name, values := range headers {
		for _, value := range values {
			rendered, err := render(value, d.variables())
			if err != nil {
				return page{}, err
			}
			request.Header.Add(name, rendered)
		}
	}
	if cookie := d.settings["cookie"]; cookie != "" && d.spec.Login != nil && d.spec.Login.Method == "cookie" {
		request.Header.Set("Cookie", cookie)
	}
	response, err := d.http.Do(ctx, request)
	if err != nil {
		return page{}, err
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxAnswerBytes+1))
	if err != nil {
		return page{}, fmt.Errorf("spec: read %s: %w", d.spec.ID, err)
	}
	if len(raw) > maxAnswerBytes {
		return page{}, fmt.Errorf("spec: %s answered more than %d bytes", d.spec.ID, maxAnswerBytes)
	}
	contentType := response.Header.Get("Content-Type")
	if !strings.Contains(strings.ToLower(contentType), "charset") && d.encoder() != nil {
		contentType += "; charset=" + d.spec.Encoding
	}
	if reader, err := charset.NewReader(bytes.NewReader(raw), contentType); err == nil {
		if decoded, err := io.ReadAll(reader); err == nil {
			raw = decoded
		}
	}
	finalURL := target
	if response.Request != nil && response.Request.URL != nil {
		finalURL = response.Request.URL.String()
	}
	return page{status: response.StatusCode, body: raw, url: finalURL}, nil
}

func (p page) document() (*goquery.Document, error) {
	return goquery.NewDocumentFromReader(bytes.NewReader(p.body))
}

// errorMessage looks for the error selectors in a page; a match returns the site's message.
func (d *Driver) errorMessage(selectors []ErrorSelector, found *goquery.Document) (string, bool) {
	for _, one := range selectors {
		selector, err := render(one.Selector, d.variables())
		if err != nil || selector == "" {
			continue
		}
		matched := found.Find(selector)
		if matched.Length() == 0 {
			continue
		}
		message := strings.TrimSpace(matched.First().Text())
		if one.Message != nil {
			if value, ok, err := readField(htmlNode{found.Selection}, *one.Message, filterContext{variables: d.variables(), now: d.clock.Now()}); err == nil && ok {
				message = value
			}
		}
		return message, true
	}
	return "", false
}

// login signs in as the spec says. A cookie login only sets the owner's cookie, which every request carries.
func (d *Driver) login(ctx context.Context) error {
	login := d.spec.Login
	if login == nil || login.Method == "" || login.Method == "cookie" || login.Method == "none" {
		return nil
	}
	if login.Captcha != nil && login.Captcha.Type != "" {
		return fmt.Errorf("%w: the site asks a captcha, which Solarr cannot answer", ErrLoginFailed)
	}
	variables := d.variables()
	if jar := d.http.CookieJar(); jar != nil {
		if base, err := url.Parse(d.baseURL); err == nil {
			for _, cookie := range login.Cookies {
				if name, value, ok := strings.Cut(cookie, "="); ok {
					jar.SetCookies(base, []*http.Cookie{{Name: strings.TrimSpace(name), Value: strings.TrimSpace(value), Path: "/"}})
				}
			}
		}
	}
	inputs := url.Values{}
	target, err := render(login.Path, variables)
	if err != nil {
		return err
	}
	if target, err = d.resolve(target); err != nil {
		return err
	}
	method := http.MethodPost
	if login.Method == "form" || login.Method == "get" {
		submit, loadedForm, err := d.formInputs(ctx, target, login)
		if err != nil {
			return err
		}
		inputs, target = loadedForm, submit
		if login.Method == "get" {
			method = http.MethodGet
		}
	}
	for name, template := range login.Inputs {
		value, err := render(template, variables)
		if err != nil {
			return err
		}
		inputs.Set(name, value)
	}
	if login.SubmitPath != "" {
		if target, err = d.resolve(login.SubmitPath); err != nil {
			return err
		}
	}
	if method == http.MethodGet {
		target += map[bool]string{true: "&", false: "?"}[strings.Contains(target, "?")] + d.encodeValues(inputs)
	}
	answer, err := d.fetch(ctx, method, target, inputs, login.Headers)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrLoginFailed, err)
	}
	found, err := answer.document()
	if err != nil {
		return err
	}
	if message, failed := d.errorMessage(login.Error, found); failed {
		return fmt.Errorf("%w: %s", ErrLoginFailed, message)
	}
	return d.loginTest(ctx, login)
}

// formInputs loads the sign-in page and answers where its form submits and the fields it already holds (hidden tokens).
func (d *Driver) formInputs(ctx context.Context, target string, login *Login) (string, url.Values, error) {
	answer, err := d.fetch(ctx, http.MethodGet, target, nil, login.Headers)
	if err != nil {
		return "", nil, fmt.Errorf("%w: %w", ErrLoginFailed, err)
	}
	found, err := answer.document()
	if err != nil {
		return "", nil, err
	}
	formSelector := login.Form
	if formSelector == "" {
		formSelector = "form"
	}
	form := found.Find(formSelector).First()
	if form.Length() == 0 {
		return "", nil, fmt.Errorf("%w: the sign-in form was not on the page", ErrLoginFailed)
	}
	values := url.Values{}
	form.Find("input[name]").Each(func(_ int, input *goquery.Selection) {
		name, _ := input.Attr("name")
		value, _ := input.Attr("value")
		if kind, _ := input.Attr("type"); kind != "submit" && kind != "button" && kind != "checkbox" && kind != "radio" {
			values.Set(name, value)
		}
	})
	submit := answer.url
	if action, ok := form.Attr("action"); ok && action != "" {
		if base, err := url.Parse(answer.url); err == nil {
			if relative, err := url.Parse(action); err == nil {
				submit = base.ResolveReference(relative).String()
			}
		}
	}
	for name, extractor := range login.SelectorInputs {
		if value, ok, err := readField(htmlNode{found.Selection}, extractor, filterContext{variables: d.variables(), now: d.clock.Now()}); err == nil && ok {
			values.Set(name, value)
		}
	}
	return submit, values, nil
}

// loginTest opens a page only a signed-in visitor sees.
func (d *Driver) loginTest(ctx context.Context, login *Login) error {
	if login.Test == nil || login.Test.Selector == "" {
		return nil
	}
	target, err := d.resolve(login.Test.Path)
	if err != nil {
		return err
	}
	answer, err := d.fetch(ctx, http.MethodGet, target, nil, nil)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrLoginFailed, err)
	}
	found, err := answer.document()
	if err != nil {
		return err
	}
	if found.Find(login.Test.Selector).Length() == 0 {
		return fmt.Errorf("%w: the site did not show a signed-in page", ErrLoginFailed)
	}
	return nil
}

var sizePattern = regexp.MustCompile(`(?i)^\s*([\d.,]+)\s*([kmgtp]?)i?b?\s*$`)

// parseSize reads "1.5 GB", "700MiB" or a plain number of bytes.
func parseSize(text string) int64 {
	match := sizePattern.FindStringSubmatch(strings.TrimSpace(text))
	if match == nil {
		return 0
	}
	number := match[1]
	switch {
	case strings.Contains(number, ",") && strings.Contains(number, "."):
		number = strings.ReplaceAll(number, ",", "")
	case strings.Count(number, ",") == 1 && len(number)-strings.Index(number, ",") <= 3:
		number = strings.Replace(number, ",", ".", 1)
	default:
		number = strings.ReplaceAll(number, ",", "")
	}
	value, err := strconv.ParseFloat(number, 64)
	if err != nil {
		return 0
	}
	unit := strings.ToLower(match[2])
	for _, step := range "kmgtp" {
		if unit == "" {
			break
		}
		value *= 1024
		if string(step) == unit {
			break
		}
	}
	return int64(value)
}

func parseCount(text string) int {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, text)
	count, _ := strconv.Atoi(digits)
	return count
}

func parseDate(text string, now time.Time) time.Time {
	text = strings.TrimSpace(text)
	if parsed, err := time.Parse(time.RFC3339, text); err == nil {
		return parsed
	}
	if parsed, ok := parseFuzzyTime(text, now); ok {
		return parsed
	}
	if seconds, err := strconv.ParseInt(text, 10, 64); err == nil && seconds > 100000000 {
		return time.Unix(seconds, 0)
	}
	return now
}

// Search runs the spec's search paths for the request.
func (d *Driver) Search(ctx context.Context, request indexer.SearchRequest) ([]indexer.RawItem, error) {
	variables := d.variables()
	variables.Keywords = request.Query
	for key, value := range request.Extra {
		variables.Query[queryKey(key)] = value
	}
	variables.Query["Q"] = request.Query
	if variables.Keywords == "" && (variables.Query["Artist"] != "" || variables.Query["Album"] != "") {
		variables.Keywords = strings.TrimSpace(variables.Query["Artist"] + " " + variables.Query["Album"])
	}
	filtered, err := applyFilters(variables.Keywords, d.spec.Search.KeywordsFilters, filterContext{variables: variables, now: d.clock.Now()})
	if err != nil {
		return nil, err
	}
	variables.Keywords = filtered

	wanted := request.Categories
	variables.Categories = d.spec.siteCategories(wanted)
	if len(wanted) > 0 && len(variables.Categories) == 0 {
		return nil, nil
	}

	var items []indexer.RawItem
	for _, path := range d.spec.Search.Paths {
		if !d.pathServes(path, wanted) {
			continue
		}
		found, err := d.searchPath(ctx, path, variables, request.Limit)
		if err != nil {
			return items, err
		}
		items = append(items, found...)
		if request.Limit > 0 && len(items) >= request.Limit {
			break
		}
	}
	return items, nil
}

// queryKey names a search parameter the way templates do: ids in capitals, the rest capitalised.
func queryKey(key string) string {
	switch lower := strings.ToLower(key); lower {
	case "imdbid", "tvdbid", "tmdbid", "tvmazeid", "traktid", "doubanid", "rid":
		return strings.ToUpper(lower)
	default:
		if lower == "" {
			return key
		}
		return strings.ToUpper(lower[:1]) + lower[1:]
	}
}

// pathServes reports whether a search path is for any of the categories asked for.
func (d *Driver) pathServes(path SearchPath, wanted []int) bool {
	if len(path.Categories) == 0 || len(wanted) == 0 {
		return true
	}
	for _, mapping := range d.spec.Caps.Categories {
		id := mapping.categoryID()
		for _, one := range wanted {
			if one == id || (one%1000 == 0 && id/1000*1000 == one) {
				for _, category := range path.Categories {
					if mapping.Cat == category || strings.HasPrefix(mapping.Cat, category+"/") || mapping.ID == category {
						return true
					}
				}
			}
		}
	}
	return false
}

func (d *Driver) searchPath(ctx context.Context, path SearchPath, variables *Variables, limit int) ([]indexer.RawItem, error) {
	inputs := url.Values{}
	merged := map[string]string{}
	for name, value := range d.spec.Search.Inputs {
		merged[name] = value
	}
	for name, value := range path.Inputs {
		merged[name] = value
	}
	raw := ""
	for name, template := range merged {
		value, err := render(template, variables)
		if err != nil {
			return nil, err
		}
		if name == "$raw" {
			raw = value
			continue
		}
		if value == "" && !d.spec.Search.AllowEmptyInputs {
			continue
		}
		inputs.Add(name, value)
	}
	target, err := render(path.Path, variables)
	if err != nil {
		return nil, err
	}
	if target, err = d.resolve(target); err != nil {
		return nil, err
	}
	method := http.MethodGet
	if strings.EqualFold(path.Method, "post") {
		method = http.MethodPost
	}
	if method == http.MethodGet {
		query := d.encodeValues(inputs)
		if raw != "" {
			query = strings.TrimPrefix(query+"&"+raw, "&")
		}
		if query != "" {
			target += map[bool]string{true: "&", false: "?"}[strings.Contains(target, "?")] + query
		}
	}

	answer, err := d.searchRequest(ctx, method, target, inputs)
	if err != nil {
		return nil, err
	}
	kind := path.Response["type"]
	if kind == "" {
		kind = "html"
	}
	if message := path.Response["noResultsMessage"]; message != "" && bytes.Contains(answer.body, []byte(message)) {
		return nil, nil
	}
	rows, err := d.rows(kind, answer, variables)
	if err != nil {
		return nil, err
	}
	var items []indexer.RawItem
	for _, row := range rows {
		item, ok, err := d.item(row, variables)
		if err != nil {
			if errors.Is(err, errFieldMissing) {
				continue
			}
			return items, err
		}
		if ok {
			items = append(items, item)
		}
		if len(items) >= maxRows || (limit > 0 && len(items) >= limit) {
			break
		}
	}
	return items, nil
}

// searchRequest sends the search, signing in first when the site needs it and again once if it says it was signed out.
func (d *Driver) searchRequest(ctx context.Context, method, target string, form url.Values) (page, error) {
	needsLogin := d.spec.Login != nil && d.spec.Login.Method != "" && d.spec.Login.Method != "cookie" && d.spec.Login.Method != "none"
	for attempt := 0; ; attempt++ {
		answer, err := d.fetch(ctx, method, target, form, d.spec.Search.Headers)
		if err != nil {
			var status *httpx.StatusError
			if needsLogin && attempt == 0 && errors.As(err, &status) && (status.Status == http.StatusUnauthorized || status.Status == http.StatusForbidden) {
				if err := d.login(ctx); err != nil {
					return page{}, err
				}
				continue
			}
			return page{}, err
		}
		if len(d.spec.Search.Error) > 0 && answerIsHTML(answer.body) {
			if found, err := answer.document(); err == nil {
				if message, failed := d.errorMessage(d.spec.Search.Error, found); failed {
					if needsLogin && attempt == 0 {
						if err := d.login(ctx); err != nil {
							return page{}, err
						}
						continue
					}
					return page{}, fmt.Errorf("spec: %s: %s", d.spec.ID, message)
				}
			}
		}
		if needsLogin && attempt == 0 && d.http.CookieJar() != nil && !d.hasCookies() {
			if err := d.login(ctx); err != nil {
				return page{}, err
			}
			continue
		}
		return answer, nil
	}
}

// hasCookies reports whether the jar holds any cookie for the site: none means nobody has signed in yet.
func (d *Driver) hasCookies() bool {
	base, err := url.Parse(d.baseURL)
	if err != nil {
		return false
	}
	return len(d.http.CookieJar().Cookies(base)) > 0
}

func answerIsHTML(body []byte) bool {
	trimmed := bytes.TrimSpace(body)
	return len(trimmed) > 0 && trimmed[0] == '<'
}

// row is one release in an answer.
type row struct {
	source node
	// heading is the text of the date heading above the row, empty when the spec has none.
	heading string
}

func (d *Driver) rows(kind string, answer page, variables *Variables) ([]row, error) {
	spec := d.spec.Search.Rows
	selector, err := render(spec.Selector, variables)
	if err != nil {
		return nil, err
	}
	body := answer.body
	for _, filter := range d.spec.Search.PreprocessingFilters {
		filtered, err := applyFilter(string(body), filter.Name, filter.Args, filterContext{variables: variables, now: d.clock.Now()})
		if err != nil {
			return nil, err
		}
		body = []byte(filtered)
	}
	if kind == "json" {
		var decoded any
		if err := json.Unmarshal(body, &decoded); err != nil {
			return nil, fmt.Errorf("spec: %s did not answer JSON: %w", d.spec.ID, err)
		}
		return d.jsonRows(decoded, selector, spec), nil
	}
	found, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if spec.Remove != "" {
		found.Find(spec.Remove).Remove()
	}
	selection := found.Find(selector)
	after := 0
	if spec.After != "" {
		after, _ = strconv.Atoi(spec.After)
	}
	var rows []row
	selection.Each(func(index int, one *goquery.Selection) {
		if index >= after {
			rows = append(rows, row{source: htmlNode{one}, heading: d.dateHeading(one, spec.DateHeaders, variables)})
		}
	})
	return rows, nil
}

// dateHeading finds the nearest heading before the row, climbing out of the row's parents, and runs the spec's filters over its text.
func (d *Driver) dateHeading(one *goquery.Selection, headers *DateHeaders, variables *Variables) string {
	if headers == nil || headers.Selector == "" {
		return ""
	}
	selector, err := render(headers.Selector, variables)
	if err != nil {
		return ""
	}
	for level := one; level.Length() > 0; level = level.Parent() {
		for earlier := level.Prev(); earlier.Length() > 0; earlier = earlier.Prev() {
			heading := earlier
			if !earlier.Is(selector) {
				heading = earlier.Find(selector).First()
			}
			if heading.Length() == 0 {
				continue
			}
			text, err := applyFilters(strings.TrimSpace(heading.Text()), headers.Filters, filterContext{variables: variables, now: d.clock.Now()})
			if err != nil {
				return ""
			}
			return strings.TrimSpace(text)
		}
	}
	return ""
}

func (d *Driver) jsonRows(decoded any, selector string, spec Rows) []row {
	root := jsonNode{value: decoded}
	base, conditions := splitConditions(selector)
	selected, ok := root.path(base)
	if !ok {
		return nil
	}
	var rows []row
	add := func(element any, parents []any) {
		candidate := jsonNode{value: element, parents: parents}
		if !holds(candidate, conditions) {
			return
		}
		if spec.Attribute != "" {
			inner, ok := candidate.path(spec.Attribute)
			if !ok {
				return
			}
			if list, isList := inner.([]any); isList {
				for _, one := range list {
					rows = append(rows, row{source: jsonNode{value: one, parents: append(slices.Clone(parents), element)}})
				}
				return
			}
			element = inner
		}
		if list, isList := element.([]any); isList && spec.Multiple {
			for _, one := range list {
				rows = append(rows, row{source: jsonNode{value: one, parents: parents}})
			}
			return
		}
		rows = append(rows, row{source: jsonNode{value: element, parents: parents}})
	}
	parents := []any{decoded}
	switch typed := selected.(type) {
	case []any:
		for _, element := range typed {
			add(element, parents)
		}
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			add(typed[key], append(slices.Clone(parents), typed))
		}
	}
	return rows
}

// item reads one row's fields in order, each able to name the ones before it, and builds the release.
func (d *Driver) item(source row, variables *Variables) (indexer.RawItem, bool, error) {
	rowVars := *variables
	rowVars.Result = map[string]string{}
	context := filterContext{variables: &rowVars, now: d.clock.Now()}
	for _, field := range d.spec.Search.Fields {
		name, modifier, _ := strings.Cut(field.Name, "|")
		value, found, err := readField(source.source, field, context)
		if err != nil {
			return indexer.RawItem{}, false, err
		}
		if !found {
			continue
		}
		if modifier == "append" {
			value = rowVars.Result[name] + value
		}
		rowVars.Result[name] = value
	}
	result := rowVars.Result
	if result["date"] == "" {
		result["date"] = source.heading
	}
	title := strings.TrimSpace(result["title"])
	if title == "" {
		return indexer.RawItem{}, false, nil
	}
	item := indexer.RawItem{Title: title}
	details := d.absolute(result["details"])
	link := d.absolute(result["download"])
	magnet := result["magnet"]
	if strings.HasPrefix(link, "magnet:") {
		magnet, link = link, ""
	}
	if magnet == "" && result["infohash"] != "" && link == "" {
		magnet = indexer.Magnet(result["infohash"], title, nil)
	}
	switch {
	case link != "":
		item.Link = link
	case magnet != "":
		item.Link = magnet
	case details != "":
		item.Link = details
	default:
		return indexer.RawItem{}, false, nil
	}
	item.GUID = cmpOr(details, item.Link)
	item.InfoHash = strings.ToLower(result["infohash"])
	item.SizeBytes = parseSize(result["size"])
	item.Seeders = parseCount(result["seeders"])
	item.Peers = item.Seeders + parseCount(result["leechers"])
	item.PublishedAt = parseDate(result["date"], d.clock.Now())
	if id, ok := d.spec.categoryOf(result["category"]); ok {
		item.Categories = []int{id}
	} else if id, ok := d.spec.categoryOf(result["categorydesc"]); ok {
		item.Categories = []int{id}
	} else {
		item.Categories = []int{8000}
	}
	return item, true, nil
}

func cmpOr(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// absolute makes a link from a page absolute; a magnet link and an empty value stay as they are.
func (d *Driver) absolute(link string) string {
	link = strings.TrimSpace(link)
	if link == "" || strings.HasPrefix(link, "magnet:") {
		return link
	}
	resolved, err := d.resolve(link)
	if err != nil {
		return link
	}
	return resolved
}

// encoder is the encoder of the character set the site expects requests in; nil for UTF-8.
func (d *Driver) encoder() *encoding.Encoder {
	name := strings.ToLower(d.spec.Encoding)
	if name == "" || name == "utf-8" || name == "utf8" {
		return nil
	}
	found, err := htmlindex.Get(name)
	if err != nil {
		return nil
	}
	return found.NewEncoder()
}

// encodeValues writes a form or query as the site expects it: each key and value in its character set, then escaped.
func (d *Driver) encodeValues(values url.Values) string {
	encoder := d.encoder()
	if encoder == nil {
		return values.Encode()
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	escape := func(text string) string {
		if converted, err := encoder.String(text); err == nil {
			text = converted
		}
		return url.QueryEscape(text)
	}
	var out strings.Builder
	for _, key := range keys {
		for _, value := range values[key] {
			if out.Len() > 0 {
				out.WriteByte('&')
			}
			out.WriteString(escape(key) + "=" + escape(value))
		}
	}
	return out.String()
}
