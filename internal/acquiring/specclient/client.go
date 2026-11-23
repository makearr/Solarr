package specclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/extract"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

const (
	maxAnswerBytes = 8 << 20
	requestTimeout = 30 * time.Second
)

// Doer is the httpx client as the engine needs it.
type Doer interface {
	Do(ctx context.Context, request *http.Request) (*http.Response, error)
}

// ErrNotJSON is an answer that is not JSON: usually a login page or a plain-text refusal of the secret.
var ErrNotJSON = errors.New("specclient: the answer is not JSON")

// Client runs one definition against one owner's address.
type Client struct {
	def     Definition
	http    Doer
	clock   clock.Clock
	base    *url.URL
	user    string
	secret  string
	baseErr error
	mu      sync.Mutex
	cookies []*http.Cookie
}

var _ download.UsenetClient = (*Client)(nil)
var _ download.ByReference = (*Client)(nil)

// New builds the client of a definition; credentials is the secret, or "user:password" for basic auth and a user-named kind.
func New(def Definition, doer Doer, c clock.Clock, baseURL, credentials string) *Client {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err == nil && (parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https")) {
		err = errors.New("the address is not an http address")
	}
	client := &Client{def: def, http: doer, clock: c, base: parsed, secret: credentials, baseErr: err}
	if def.UserName || def.Auth.Type == "basic" {
		client.user, client.secret, _ = strings.Cut(credentials, ":")
	}
	return client
}

func (c *Client) variables(query map[string]string, id string) *extract.Variables {
	variables := extract.NewVariables(c.clock.Now())
	variables.Settings = map[string]string{"secret": c.secret, "user": c.user}
	variables.Step = map[string]map[string]string{}
	for name, value := range query {
		variables.Query[name] = value
	}
	variables.ID = id
	return variables
}

// build makes the request: the path under the owner's address, query values left out when empty, the secret by the
// definition's auth, and a check that the result still points at the owner's host.
func (c *Client) build(ctx context.Context, spec Request, variables *extract.Variables, file []byte) (*http.Request, error) {
	if c.baseErr != nil {
		return nil, c.baseErr
	}
	path, err := extract.Render(spec.Path, variables)
	if err != nil {
		return nil, err
	}
	target, err := url.Parse(c.base.String() + path)
	if err != nil || target.Host != c.base.Host || target.Scheme != c.base.Scheme {
		return nil, errors.New("specclient: the request would leave the client's address")
	}
	values := target.Query()
	for name, text := range spec.Query {
		value, err := extract.Render(text, variables)
		if err != nil {
			return nil, err
		}
		if value != "" {
			values.Set(name, value)
		}
	}
	if c.def.Auth.Type == "query" {
		values.Set(c.def.Auth.Name, c.secret)
	}
	target.RawQuery = values.Encode()
	var body io.Reader
	contentType := ""
	method := spec.Method
	switch {
	case spec.FileField != "":
		name, err := extract.Render(spec.FileName, variables)
		if err != nil {
			return nil, err
		}
		var buffer bytes.Buffer
		form := multipart.NewWriter(&buffer)
		part, err := form.CreateFormFile(spec.FileField, name)
		if err != nil {
			return nil, err
		}
		if _, err := part.Write(file); err != nil {
			return nil, err
		}
		if err := form.Close(); err != nil {
			return nil, err
		}
		body, contentType = &buffer, form.FormDataContentType()
		if method == "" {
			method = http.MethodPost
		}
	case spec.Body != "":
		text, err := extract.Render(spec.Body, variables)
		if err != nil {
			return nil, err
		}
		body, contentType = strings.NewReader(text), map[string]string{"form": "application/x-www-form-urlencoded"}[spec.BodyType]
		if contentType == "" {
			contentType = "application/json"
		}
	}
	if method == "" {
		method = http.MethodGet
	}
	request, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return nil, errors.New("specclient: the request could not be built")
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	for name, text := range spec.Headers {
		value, err := extract.Render(text, variables)
		if err != nil {
			return nil, err
		}
		request.Header.Set(name, value)
	}
	switch c.def.Auth.Type {
	case "header":
		request.Header.Set(c.def.Auth.Name, c.def.Auth.Prefix+c.secret)
	case "basic":
		request.SetBasicAuth(c.user, c.secret)
	case "login":
		c.mu.Lock()
		for _, cookie := range c.cookies {
			request.AddCookie(cookie)
		}
		c.mu.Unlock()
	}
	return request, nil
}

// answer is a decoded reply: a JSON-shaped value (xml is read into one) or an html document.
type answer struct {
	value any
	doc   *goquery.Document
}

func (a answer) node() extract.Node {
	if a.doc != nil {
		return extract.HTMLNode{Selection: a.doc.Selection}
	}
	return extract.JSONNode{Value: a.value}
}

var errUnauthorised = errors.New("specclient: the client refused the session")

// exchange makes one request and answers the body and the cookies set; the address is never part of an error, since a query may carry the secret.
func (c *Client) exchange(ctx context.Context, spec Request, variables *extract.Variables, file []byte) ([]byte, []*http.Cookie, error) {
	request, err := c.build(ctx, spec, variables, file)
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	reply, err := c.http.Do(ctx, request.WithContext(ctx))
	if err != nil {
		var refused *httpx.StatusError
		if errors.As(err, &refused) && (refused.Status == http.StatusUnauthorized || refused.Status == http.StatusForbidden) {
			return nil, nil, fmt.Errorf("%w: %s", errUnauthorised, httpx.Explain(err))
		}
		return nil, nil, fmt.Errorf("specclient: %s", httpx.Explain(err))
	}
	defer func() { _ = reply.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(reply.Body, maxAnswerBytes+1))
	if err != nil || len(body) > maxAnswerBytes {
		return nil, nil, errors.New("specclient: the answer could not be read within its size limit")
	}
	if reply.StatusCode < 200 || reply.StatusCode > 299 {
		return nil, nil, fmt.Errorf("specclient: status %d", reply.StatusCode)
	}
	return body, reply.Cookies(), nil
}

// login opens the session of a login auth; force drops the one held first. Other auths have nothing to open.
func (c *Client) login(ctx context.Context, force bool) error {
	if c.def.Auth.Type != "login" {
		return nil
	}
	c.mu.Lock()
	held := len(c.cookies) > 0
	if force {
		c.cookies = nil
	}
	c.mu.Unlock()
	if held && !force {
		return nil
	}
	_, cookies, err := c.exchange(ctx, *c.def.Auth.Login, c.variables(nil, ""), nil)
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}
	if len(cookies) == 0 {
		return errors.New("specclient: login: the client opened no session, so the user or the secret was refused")
	}
	c.mu.Lock()
	c.cookies = cookies
	c.mu.Unlock()
	return nil
}

// call makes one request and decodes the answer as the request says; a login that expired is made again once.
func (c *Client) call(ctx context.Context, spec Request, variables *extract.Variables, file []byte) (answer, error) {
	if err := c.login(ctx, false); err != nil {
		return answer{}, err
	}
	body, _, err := c.exchange(ctx, spec, variables, file)
	if errors.Is(err, errUnauthorised) && c.def.Auth.Type == "login" {
		if err = c.login(ctx, true); err != nil {
			return answer{}, err
		}
		body, _, err = c.exchange(ctx, spec, variables, file)
	}
	if err != nil {
		return answer{}, err
	}
	switch spec.Answer {
	case "html":
		doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
		if err != nil {
			return answer{}, fmt.Errorf("specclient: the answer is not html (it begins %q)", snippet(body))
		}
		return answer{doc: doc}, nil
	case "xml":
		value, err := extract.XMLValue(body)
		if err != nil {
			return answer{}, fmt.Errorf("specclient: the answer is not xml (it begins %q)", snippet(body))
		}
		return answer{value: value}, nil
	}
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return answer{}, fmt.Errorf("%w (it begins %q)", ErrNotJSON, snippet(body))
	}
	return answer{value: decoded}, nil
}

func snippet(body []byte) string {
	text := strings.Join(strings.Fields(string(body)), " ")
	if len(text) > 60 {
		text = text[:60]
	}
	return text
}

func (c *Client) readFields(node extract.Node, fields map[string]Field, variables *extract.Variables) (map[string]string, error) {
	values := map[string]string{}
	for name, field := range fields {
		value, ok, err := extract.ReadField(node, field.extractor(name), extract.FilterContext{Variables: variables, Now: c.clock.Now()})
		if errors.Is(err, extract.ErrFieldMissing) {
			return nil, fmt.Errorf("specclient: the answer has no %q", name)
		}
		if err != nil {
			return nil, fmt.Errorf("specclient: field %q could not be read", name)
		}
		if ok && field.BlankWhen != "" && node.Has(field.BlankWhen) {
			value = ""
		}
		if ok {
			values[name] = value
		}
	}
	return values, nil
}

func (c *Client) runSteps(ctx context.Context, steps []Step, variables *extract.Variables) error {
	for _, step := range steps {
		answer, err := c.call(ctx, step.Request, variables, nil)
		if err != nil {
			return fmt.Errorf("%s: %w", step.Name, err)
		}
		values, err := c.readFields(answer.node(), step.Fields, variables)
		if err != nil {
			return fmt.Errorf("%s: %w", step.Name, err)
		}
		variables.Step[step.Name] = values
		if message := values["error"]; message != "" {
			return fmt.Errorf("%s: %s", step.Name, message)
		}
	}
	return nil
}

// Probe proves the address and the secret.
func (c *Client) Probe(ctx context.Context) (download.Probe, error) {
	variables := c.variables(nil, "")
	if err := c.runSteps(ctx, c.def.Probe.Steps, variables); err != nil {
		return download.Probe{}, err
	}
	values, err := c.readFields(extract.JSONNode{Value: map[string]any{}}, c.def.Probe.Fields, variables)
	if err != nil {
		return download.Probe{}, err
	}
	probe := download.Probe{Version: values["version"], DownloadFolder: values["download_folder"]}
	if list, ok := values["categories"]; ok {
		probe.Categories = splitList(list)
	}
	return probe, nil
}

func splitList(text string) []string {
	var list []string
	if json.Unmarshal([]byte(text), &list) == nil {
		return list
	}
	for _, one := range strings.Split(text, ",") {
		if one = strings.TrimSpace(one); one != "" {
			list = append(list, one)
		}
	}
	return list
}

// AddNZB uploads the release file.
func (c *Client) AddNZB(ctx context.Context, request download.AddRequest) (string, error) {
	variables := c.variables(map[string]string{"name": request.Name, "category": request.Category}, "")
	answer, err := c.call(ctx, c.def.Add.Request, variables, request.File)
	if err != nil {
		return "", fmt.Errorf("add: %w", err)
	}
	values, err := c.readFields(answer.node(), c.def.Add.Fields, variables)
	if err != nil {
		return "", fmt.Errorf("add: %w", err)
	}
	if accepted, _ := strconv.ParseBool(values["ok"]); values["ok"] != "" && !accepted || values["ref"] == "" {
		if values["error"] == "" {
			return "", fmt.Errorf("add: %w (the client gave no reason; often it has downloaded it before)", download.ErrRefused)
		}
		return "", fmt.Errorf("add: %w: %s", download.ErrRefused, values["error"])
	}
	return values["ref"], nil
}

// Downloads answers every listing's rows in category.
func (c *Client) Downloads(ctx context.Context, category string) ([]download.Download, error) {
	return c.listAll(ctx, c.def.Downloads.List, map[string]string{"category": category}, category)
}

// DownloadsByRef implements download.ByReference when the definition declares by_ref listings.
func (c *Client) DownloadsByRef(ctx context.Context, clientRefs []string) ([]download.Download, error) {
	if len(c.def.Downloads.ByRef) == 0 {
		return nil, download.ErrByRefUnsupported
	}
	if len(clientRefs) == 0 {
		return nil, nil
	}
	return c.listAll(ctx, c.def.Downloads.ByRef, map[string]string{"refs": strings.Join(clientRefs, ",")}, "")
}

func (c *Client) listAll(ctx context.Context, listings []Listing, query map[string]string, category string) ([]download.Download, error) {
	var downloads []download.Download
	for _, listing := range listings {
		variables := c.variables(query, "")
		answer, err := c.call(ctx, listing.Request, variables, nil)
		if err != nil {
			return nil, fmt.Errorf("downloads: %w", err)
		}
		rows, err := rowsOf(answer, listing.Rows)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			values, err := c.readFields(row, listing.Fields, variables)
			if err != nil {
				return nil, fmt.Errorf("downloads: %w", err)
			}
			if listing.FilterCategory && category != "" && !strings.EqualFold(values["category"], category) {
				continue
			}
			downloads = append(downloads, listing.download(values))
		}
	}
	return downloads, nil
}

func rowsOf(from answer, selector string) ([]extract.Node, error) {
	if from.doc != nil {
		var rows []extract.Node
		from.doc.Find(selector).Each(func(_ int, one *goquery.Selection) { rows = append(rows, extract.HTMLNode{Selection: one}) })
		return rows, nil
	}
	root := extract.JSONNode{Value: from.value}
	picked, ok := root.Path(selector)
	if !ok {
		return nil, nil
	}
	list, isList := picked.([]any)
	if !isList {
		list = []any{picked}
	}
	rows := make([]extract.Node, 0, len(list))
	for _, element := range list {
		rows = append(rows, extract.JSONNode{Value: element, Parents: []any{root.Value}})
	}
	return rows, nil
}

func (l Listing) download(values map[string]string) download.Download {
	state := download.State(l.DefaultState)
	for status, mapped := range l.States {
		if strings.EqualFold(status, values["status"]) {
			state = download.State(mapped)
			break
		}
	}
	entry := download.Download{
		ClientRef: values["ref"], Name: values["name"], Category: values["category"], State: state,
		SizeBytes: whole(values["size"]), SizeLeftBytes: whole(values["left"]), Error: values["message"],
	}
	if state == download.StateCompleted {
		entry.Path = values["path"]
	}
	return entry
}

func whole(text string) int64 {
	number, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	if err != nil || number < 0 {
		return 0
	}
	return number
}

// QueueStatus answers the client's totals.
func (c *Client) QueueStatus(ctx context.Context) (download.QueueStatus, error) {
	variables := c.variables(nil, "")
	answer, err := c.call(ctx, c.def.QueueStatus.Request, variables, nil)
	if err != nil {
		return download.QueueStatus{}, fmt.Errorf("queue: %w", err)
	}
	values, err := c.readFields(answer.node(), c.def.QueueStatus.Fields, variables)
	if err != nil {
		return download.QueueStatus{}, fmt.Errorf("queue: %w", err)
	}
	paused, _ := strconv.ParseBool(values["paused"])
	return download.QueueStatus{Paused: paused, SpeedBytesPerSec: whole(values["speed"]), Items: int(whole(values["items"])), SizeLeftBytes: whole(values["left"])}, nil
}

func (c *Client) act(ctx context.Context, action *Action, query map[string]string, id string) error {
	variables := c.variables(query, id)
	return c.runSteps(ctx, action.Steps, variables)
}

// Remove takes the download out of the client.
func (c *Client) Remove(ctx context.Context, clientRef string, deleteFiles bool) error {
	query := map[string]string{"delete_files": ""}
	if deleteFiles {
		query["delete_files"] = "1"
	}
	return c.act(ctx, c.def.Remove, query, clientRef)
}

// Pause pauses the named downloads.
func (c *Client) Pause(ctx context.Context, clientRefs []string) error {
	return c.act(ctx, c.def.Pause, map[string]string{"refs": strings.Join(clientRefs, ",")}, firstOf(clientRefs))
}

// Resume resumes the named downloads.
func (c *Client) Resume(ctx context.Context, clientRefs []string) error {
	return c.act(ctx, c.def.Resume, map[string]string{"refs": strings.Join(clientRefs, ",")}, firstOf(clientRefs))
}

func firstOf(refs []string) string {
	if len(refs) == 0 {
		return ""
	}
	return refs[0]
}
