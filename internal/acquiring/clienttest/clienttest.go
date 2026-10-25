// Package clienttest is the fake server and release files the download clients' tests share: every route answers by
// method and path, and every request is recorded with its body. Tests only; never real network.
package clienttest

import (
	"io"
	"net/http"
	"strconv"
	"strings"
)

// Route is one canned answer.
type Route struct {
	Status int
	Body   string
	Header http.Header
}

// Request is one request the server saw.
type Request struct {
	Method string
	Path   string
	Query  string
	Header http.Header
	Body   string
}

// Server answers by "METHOD /path", or by "/path" for any method; Answer, when set, decides instead.
type Server struct {
	Routes map[string]Route
	Answer func(request Request) (Route, bool)
	Seen   []Request
}

// RoundTrip implements http.RoundTripper.
func (s *Server) RoundTrip(request *http.Request) (*http.Response, error) {
	seen := Request{Method: request.Method, Path: request.URL.Path, Query: request.URL.RawQuery, Header: request.Header.Clone()}
	if request.Body != nil {
		raw, _ := io.ReadAll(request.Body)
		seen.Body = string(raw)
	}
	s.Seen = append(s.Seen, seen)
	answer, found := Route{}, false
	if s.Answer != nil {
		answer, found = s.Answer(seen)
	}
	if !found {
		answer, found = s.Routes[request.Method+" "+request.URL.Path]
	}
	if !found {
		answer, found = s.Routes[request.URL.Path]
	}
	if !found {
		answer = Route{Status: http.StatusNotFound}
	}
	if answer.Status == 0 {
		answer.Status = http.StatusOK
	}
	header := answer.Header
	if header == nil {
		header = make(http.Header)
	}
	return &http.Response{
		StatusCode: answer.Status, Header: header, Body: io.NopCloser(strings.NewReader(answer.Body)), Request: request,
	}, nil
}

// Bodies are the bodies of every request seen whose path is path.
func (s *Server) Bodies(path string) []string {
	var bodies []string
	for _, seen := range s.Seen {
		if seen.Path == path {
			bodies = append(bodies, seen.Body)
		}
	}
	return bodies
}

// TorrentFile is a minimal valid torrent named name.
func TorrentFile(name string) []byte {
	info := "d6:lengthi5e4:name" + strconv.Itoa(len(name)) + ":" + name + "12:piece lengthi16384e6:pieces20:01234567890123456789e"
	return []byte("d8:announce3:url4:info" + info + "e")
}

// NZB is a minimal NZB of one segment.
func NZB() []byte {
	return []byte(`<?xml version="1.0"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb"><file subject="a">` +
		`<segments><segment bytes="1" number="1">a@b</segment></segments></file></nzb>`)
}
