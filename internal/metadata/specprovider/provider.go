package specprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/extract"
)

const (
	maxAnswerBytes = 8 << 20
	requestTimeout = 15 * time.Second
)

// Code says why an operation failed, as data the interface words.
type Code string

// The error codes; a 401 or 403 is unauthorised and never opens another definition's breaker.
const (
	CodeNotFound     Code = "not_found"
	CodeUnauthorised Code = "unauthorised"
	CodeRateLimited  Code = "rate_limited"
	CodeBlocked      Code = "blocked"
	CodeBadAnswer    Code = "bad_answer"
)

// Error is a failed operation; Selector names the field that could not be read when the answer was the problem.
type Error struct {
	Code     Code
	Selector string
	Err      error
}

func (e *Error) Error() string {
	text := "specprovider: " + string(e.Code)
	if e.Selector != "" {
		text += " at " + e.Selector
	}
	if e.Err != nil {
		text += ": " + e.Err.Error()
	}
	return text
}

// Unwrap answers the cause.
func (e *Error) Unwrap() error { return e.Err }

// Doer is the httpx client as the engine needs it.
type Doer interface {
	Do(ctx context.Context, request *http.Request) (*http.Response, error)
}

// Query is what a search is asked with; nothing else of the library is ever given to a definition.
type Query struct {
	Title, Year, Language string
	IDs                   map[string]string
}

// Provider runs one definition.
type Provider struct {
	def      Definition
	http     Doer
	clock    clock.Clock
	settings func() map[string]string
	answers  answerCache
}

// New builds the provider of a definition; settings answers the owner's values at call time.
func New(def Definition, doer Doer, c clock.Clock, settings func() map[string]string) *Provider {
	return &Provider{def: def, http: doer, clock: c, settings: settings}
}

// Namespace is the id namespace the provider's own ids are written under.
func (p *Provider) Namespace() string { return p.def.IDs.Own }

// Definition is what the provider was built from.
func (p *Provider) Definition() Definition { return p.def }

// Search lists the records matching the query.
func (p *Provider) Search(ctx context.Context, query Query) ([]Record, error) {
	if p.def.Search == nil {
		return nil, &Error{Code: CodeNotFound, Err: errors.New("no search")}
	}
	variables := p.variables()
	variables.Keywords = query.Title
	variables.Query = map[string]string{"Title": query.Title, "Year": query.Year, "Language": query.Language}
	return p.cached(ctx, "search", "search\x00"+query.Title+"\x00"+query.Year+"\x00"+query.Language, func() ([]Record, error) {
		return p.run(ctx, p.def.Search, variables, true)
	})
}

// Item reads one record by this provider's own id.
func (p *Provider) Item(ctx context.Context, id string) (Record, error) {
	return p.one(ctx, p.def.Item, id)
}

// ByID reads one record by another provider's id namespace.
func (p *Provider) ByID(ctx context.Context, namespace, id string) (Record, error) {
	return p.one(ctx, p.def.ByID[namespace], id)
}

func (p *Provider) one(ctx context.Context, op *Operation, id string) (Record, error) {
	if op == nil {
		return Record{}, &Error{Code: CodeNotFound, Err: errors.New("not supported")}
	}
	variables := p.variables()
	variables.ID = id
	records, err := p.cached(ctx, "item", fmt.Sprintf("item\x00%p\x00%s", op, id), func() ([]Record, error) {
		return p.run(ctx, op, variables, false)
	})
	if err != nil {
		return Record{}, err
	}
	if len(records) == 0 {
		return Record{}, &Error{Code: CodeNotFound}
	}
	return records[0], nil
}

func (p *Provider) variables() *extract.Variables {
	variables := extract.NewVariables(p.clock.Now())
	variables.Settings = map[string]string{}
	if p.settings != nil {
		for name, value := range p.settings() {
			variables.Settings[name] = value
		}
	}
	variables.Step = map[string]map[string]string{}
	return variables
}

// answer is one decoded response.
type answer struct {
	kind string
	json any
	html *goquery.Document
}

func (a answer) root() extract.Node {
	if a.kind == "html" {
		return extract.HTMLNode{Selection: a.html.Selection}
	}
	return extract.JSONNode{Value: a.json}
}

func (p *Provider) run(ctx context.Context, op *Operation, variables *extract.Variables, many bool) ([]Record, error) {
	budget := MaxRequests
	for _, step := range op.Steps {
		found, err := p.fetch(ctx, step.Request, step.Response, variables, &budget)
		if err != nil {
			return nil, err
		}
		values := map[string]string{}
		for name, field := range step.Fields {
			value, ok, err := p.read(found.root(), field, name, variables)
			if err != nil {
				return nil, err
			}
			if ok {
				values[name] = value
			}
		}
		variables.Step[step.Name] = values
	}
	if op.Request == nil {
		return nil, &Error{Code: CodeBadAnswer, Err: errors.New("the operation has no final request")}
	}
	found, err := p.fetch(ctx, *op.Request, op.Response, variables, &budget)
	if err != nil {
		return nil, err
	}
	var rows []extract.Node
	if op.Rows != nil {
		if rows, err = p.rows(found, op.Rows.Selector, variables); err != nil {
			return nil, err
		}
	} else {
		rows = []extract.Node{found.root()}
	}
	var records []Record
	for _, row := range rows {
		record, err := p.record(row, op, variables)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
		if !many {
			break
		}
	}
	return records, nil
}

func (p *Provider) rows(found answer, selector string, variables *extract.Variables) ([]extract.Node, error) {
	selector, err := extract.Render(selector, variables)
	if err != nil {
		return nil, &Error{Code: CodeBadAnswer, Selector: selector, Err: err}
	}
	if found.kind == "html" {
		var rows []extract.Node
		found.html.Find(selector).Each(func(_ int, one *goquery.Selection) { rows = append(rows, extract.HTMLNode{Selection: one}) })
		return rows, nil
	}
	root := extract.JSONNode{Value: found.json}
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
		rows = append(rows, extract.JSONNode{Value: element, Parents: []any{found.json}})
	}
	return rows, nil
}

// fetch makes one request, bounded by the operation's request budget, the timeout and the answer size.
func (p *Provider) fetch(ctx context.Context, spec Request, response string, variables *extract.Variables, budget *int) (answer, error) {
	if *budget <= 0 {
		return answer{}, &Error{Code: CodeBadAnswer, Err: fmt.Errorf("more than %d requests", MaxRequests)}
	}
	*budget--
	request, err := p.build(ctx, spec, variables)
	if err != nil {
		return answer{}, err
	}
	if !slices.Contains(p.def.Hosts, strings.ToLower(request.URL.Hostname())) {
		return answer{}, &Error{Code: CodeBlocked, Err: fmt.Errorf("host %q is not declared", request.URL.Hostname())}
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	reply, err := p.http.Do(ctx, request.WithContext(ctx))
	if err != nil {
		// The cause is kept without the address: a query may carry a secret.
		return answer{}, &Error{Code: CodeBlocked, Err: errors.New(redactAddress(err))}
	}
	defer func() { _ = reply.Body.Close() }()
	switch {
	case reply.StatusCode == http.StatusNotFound:
		return answer{}, &Error{Code: CodeNotFound}
	case reply.StatusCode == http.StatusUnauthorized || reply.StatusCode == http.StatusForbidden:
		return answer{}, &Error{Code: CodeUnauthorised}
	case reply.StatusCode == http.StatusTooManyRequests:
		return answer{}, &Error{Code: CodeRateLimited}
	case reply.StatusCode < 200 || reply.StatusCode > 299:
		return answer{}, &Error{Code: CodeBadAnswer, Err: fmt.Errorf("status %d", reply.StatusCode)}
	}
	body, err := io.ReadAll(io.LimitReader(reply.Body, maxAnswerBytes+1))
	if err != nil {
		return answer{}, &Error{Code: CodeBadAnswer, Err: errors.New("the answer could not be read")}
	}
	if len(body) > maxAnswerBytes {
		return answer{}, &Error{Code: CodeBadAnswer, Err: fmt.Errorf("the answer is over %d bytes", maxAnswerBytes)}
	}
	return decode(body, cmpResponse(response))
}

func cmpResponse(response string) string {
	if response == "" {
		return "json"
	}
	return response
}

// redactAddress is an error's text without the URL net/http puts in it.
func redactAddress(err error) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err.Error()
	}
	return err.Error()
}

func decode(body []byte, kind string) (answer, error) {
	switch kind {
	case "html":
		doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
		if err != nil {
			return answer{}, &Error{Code: CodeBadAnswer, Err: errors.New("not html")}
		}
		return answer{kind: kind, html: doc}, nil
	case "xml":
		value, err := xmlValue(body)
		if err != nil {
			return answer{}, &Error{Code: CodeBadAnswer, Err: errors.New("not xml")}
		}
		return answer{kind: "json", json: value}, nil
	}
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return answer{}, &Error{Code: CodeBadAnswer, Err: errors.New("not json")}
	}
	return answer{kind: "json", json: value}, nil
}

// xmlValue reads an XML answer as the JSON-shaped value a selector path walks: attributes as "@name", text as "#text",
// repeated elements as a list.
func xmlValue(body []byte) (any, error) {
	decoder := xml.NewDecoder(bytes.NewReader(body))
	for {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		if start, ok := token.(xml.StartElement); ok {
			value, err := xmlElement(decoder, start)
			return map[string]any{start.Name.Local: value}, err
		}
	}
}

func xmlElement(decoder *xml.Decoder, start xml.StartElement) (any, error) {
	object := map[string]any{}
	for _, attribute := range start.Attr {
		object["@"+attribute.Name.Local] = attribute.Value
	}
	var text strings.Builder
	for {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		switch one := token.(type) {
		case xml.StartElement:
			child, err := xmlElement(decoder, one)
			if err != nil {
				return nil, err
			}
			switch existing := object[one.Name.Local].(type) {
			case nil:
				object[one.Name.Local] = child
			case []any:
				object[one.Name.Local] = append(existing, child)
			default:
				object[one.Name.Local] = []any{existing, child}
			}
		case xml.CharData:
			text.Write(one)
		case xml.EndElement:
			trimmed := strings.TrimSpace(text.String())
			if len(object) == 0 {
				return trimmed, nil
			}
			if trimmed != "" {
				object["#text"] = trimmed
			}
			return object, nil
		}
	}
}

func (p *Provider) build(ctx context.Context, spec Request, variables *extract.Variables) (*http.Request, error) {
	address, err := extract.Render(spec.Path, variables)
	if err != nil {
		return nil, &Error{Code: CodeBadAnswer, Err: err}
	}
	parsed, err := url.Parse(address)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return nil, &Error{Code: CodeBlocked, Err: errors.New("the address is not an http address")}
	}
	values := parsed.Query()
	for _, name := range sortedKeys(spec.Query) {
		value, err := extract.Render(spec.Query[name], variables)
		if err != nil {
			return nil, &Error{Code: CodeBadAnswer, Err: err}
		}
		values.Set(name, value)
	}
	parsed.RawQuery = values.Encode()
	var body io.Reader
	if spec.Body != "" {
		text, err := extract.Render(spec.Body, variables)
		if err != nil {
			return nil, &Error{Code: CodeBadAnswer, Err: err}
		}
		body = strings.NewReader(text)
	}
	method := spec.Method
	if method == "" {
		method = http.MethodGet
	}
	request, err := http.NewRequestWithContext(ctx, method, parsed.String(), body)
	if err != nil {
		return nil, &Error{Code: CodeBadAnswer, Err: errors.New("the request could not be built")}
	}
	for _, headers := range []map[string]string{p.def.Headers, spec.Headers} {
		for _, name := range sortedKeys(headers) {
			value, err := extract.Render(headers[name], variables)
			if err != nil {
				return nil, &Error{Code: CodeBadAnswer, Err: err}
			}
			request.Header.Set(name, value)
		}
	}
	if spec.Body != "" && request.Header.Get("Content-Type") == "" {
		request.Header.Set("Content-Type", map[string]string{"form": "application/x-www-form-urlencoded"}[spec.BodyType])
		if request.Header.Get("Content-Type") == "" {
			request.Header.Set("Content-Type", "application/json")
		}
	}
	for _, name := range sortedKeys(p.def.Cookies) {
		value, err := extract.Render(p.def.Cookies[name], variables)
		if err != nil {
			return nil, &Error{Code: CodeBadAnswer, Err: err}
		}
		request.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	return request, nil
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// read reads one field of a node; a missing required field is a bad answer naming the selector.
func (p *Provider) read(node extract.Node, field Field, name string, variables *extract.Variables) (string, bool, error) {
	value, ok, err := extract.ReadField(node, field.extractor(name), extract.FilterContext{Variables: variables, Now: p.clock.Now()})
	if errors.Is(err, extract.ErrFieldMissing) {
		return "", false, nil
	}
	if err != nil {
		return "", false, &Error{Code: CodeBadAnswer, Selector: field.Selector, Err: errors.New("the field could not be read")}
	}
	return value, ok, nil
}

func (p *Provider) record(row extract.Node, op *Operation, variables *extract.Variables) (Record, error) {
	record := Record{IDs: map[string]string{}}
	for _, name := range sortedKeys(op.Fields) {
		field := op.Fields[name]
		value, ok, err := p.read(row, field, name, variables)
		if err != nil {
			return Record{}, err
		}
		if !ok {
			continue
		}
		if len(field.Map) > 0 {
			mapped, known := field.Map[value]
			if !known {
				record.Dropped++
				continue
			}
			value = mapped
		}
		p.assign(&record, name, value, field)
	}
	var err error
	if record.Parts, err = readSection(p, row, op.Parts, variables, partOf); err != nil {
		return Record{}, err
	}
	if record.People, err = readSection(p, row, op.People, variables, personOf); err != nil {
		return Record{}, err
	}
	if record.Images, err = readSection(p, row, op.Images, variables, imageOf); err != nil {
		return Record{}, err
	}
	if record.Organisations, err = readSection(p, row, op.Organisations, variables, organisationOf); err != nil {
		return Record{}, err
	}
	for i := range record.Parts {
		if record.Parts[i].IDs == nil {
			record.Parts[i].IDs = map[string]string{}
		}
	}
	return record, nil
}

func (p *Provider) assign(record *Record, name, value string, field Field) {
	list := func() []string {
		var out []string
		for _, one := range strings.Split(value, field.Split) {
			if one = strings.TrimSpace(one); one != "" {
				out = append(out, one)
			}
		}
		return out
	}
	number := func() int { n, _ := strconv.Atoi(strings.TrimSpace(value)); return n }
	base, namespace, _ := strings.Cut(name, ".")
	switch base {
	case "id":
		if namespace == "" {
			namespace = p.def.IDs.Own
		}
		record.IDs[namespace] = value
	case "title":
		record.Title = value
	case "original_title":
		record.OriginalTitle = value
	case "sort_title":
		record.SortTitle = value
	case "alt_titles":
		record.AltTitles = append(record.AltTitles, listOrOne(field, value, list)...)
	case "overview":
		record.Overview = value
	case "year":
		record.Year = number()
	case "date":
		record.Date = value
	case "end_date":
		record.EndDate = value
	case "status":
		record.Status = value
	case "language":
		record.Language = value
	case "country":
		record.Country = value
	case "genres":
		record.Genres = append(record.Genres, listOrOne(field, value, list)...)
	case "tags":
		record.Tags = append(record.Tags, listOrOne(field, value, list)...)
	case "adult":
		record.Adult = value == "true" || value == "1"
	case "age_rating":
		record.AgeRating = value
	case "rating":
		record.Rating.Value, _ = strconv.ParseFloat(strings.TrimSpace(value), 64)
	case "votes":
		record.Rating.Votes = number()
	case "page_count":
		record.PageCount = number()
	case "runtime_seconds":
		record.RuntimeSeconds = number()
	case "links":
		record.Links = append(record.Links, listOrOne(field, value, list)...)
	}
}

func listOrOne(field Field, value string, split func() []string) []string {
	if field.Split != "" {
		return split()
	}
	return []string{value}
}

func readSection[T any](p *Provider, row extract.Node, section *Section, variables *extract.Variables, build func(map[string]string) T) ([]T, error) {
	if section == nil {
		return nil, nil
	}
	// A section reads from the item's own answer, so its rows are found from the row the item was read from.
	var rows []extract.Node
	switch node := row.(type) {
	case extract.HTMLNode:
		node.Selection.Find(section.Rows.Selector).Each(func(_ int, one *goquery.Selection) { rows = append(rows, extract.HTMLNode{Selection: one}) })
	case extract.JSONNode:
		if picked, ok := node.Path(section.Rows.Selector); ok {
			list, isList := picked.([]any)
			if !isList {
				list = []any{picked}
			}
			for _, element := range list {
				rows = append(rows, extract.JSONNode{Value: element, Parents: append(slices.Clone(node.Parents), node.Value)})
			}
		}
	}
	var out []T
	for _, one := range rows {
		values := map[string]string{}
		for _, name := range sortedKeys(section.Fields) {
			field := section.Fields[name]
			value, ok, err := p.read(one, field, name, variables)
			if err != nil {
				return nil, err
			}
			if mapped, known := field.Map[value]; len(field.Map) > 0 && ok {
				if !known {
					continue
				}
				value = mapped
			}
			if ok {
				values[name] = value
			}
		}
		out = append(out, build(values))
	}
	return out, nil
}

func partOf(values map[string]string) Part {
	number, _ := strconv.ParseFloat(strings.TrimSpace(values["number"]), 64)
	part := Part{Kind: values["kind"], Number: number, Title: values["title"], Date: values["date"], Overview: values["overview"], IDs: map[string]string{}}
	if id := values["id"]; id != "" {
		part.IDs["own"] = id
	}
	return part
}

func personOf(values map[string]string) Person {
	return Person{Name: values["name"], Role: values["role"], ID: values["id"]}
}

func imageOf(values map[string]string) Image {
	width, _ := strconv.Atoi(values["width"])
	height, _ := strconv.Atoi(values["height"])
	return Image{Kind: values["kind"], URL: values["url"], Language: values["language"], Width: width, Height: height}
}

func organisationOf(values map[string]string) Organisation {
	return Organisation{Name: values["name"], Role: values["role"]}
}
