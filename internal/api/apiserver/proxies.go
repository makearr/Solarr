package apiserver

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/settings"
)

func proxyView(proxy settings.Proxy) gen.Proxy {
	view := gen.Proxy{Id: proxy.ID, Name: proxy.Name, Kind: gen.ProxyKind(proxy.Kind), Url: proxy.URL}
	if proxy.SourceArrID > 0 {
		view.SourceArrId = &proxy.SourceArrID
	}
	return view
}

func (s *Server) writeProxyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, settings.ErrProxyInUse):
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict, "Proxy in use", err.Error())
	default:
		writeIndexerError(w, s, err, "The proxy has problems", "Nothing was changed.")
	}
}

// ListProxies answers the proxy list. Administrator only.
func (s *Server) ListProxies(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	proxies, err := s.auth.Settings.Proxies(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	views := make([]gen.Proxy, len(proxies))
	for index, proxy := range proxies {
		views[index] = proxyView(proxy)
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

func decodeProxy(w http.ResponseWriter, s *Server, r *http.Request) (settings.ProxyRequest, bool) {
	var body gen.ProxyRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "expected a JSON object with name, kind and url")
		return settings.ProxyRequest{}, false
	}
	return settings.ProxyRequest{Name: body.Name, Kind: settings.ProxyKind(body.Kind), URL: body.Url}, true
}

// AddProxy adds a proxy. Administrator only.
func (s *Server) AddProxy(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	request, ok := decodeProxy(w, s, r)
	if !ok {
		return
	}
	created, err := s.auth.Settings.CreateProxy(r.Context(), request, s.wall.Now().Unix())
	if err != nil {
		s.writeProxyError(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusCreated, proxyView(created))
}

// UpdateProxy changes a proxy. Administrator only.
func (s *Server) UpdateProxy(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	request, ok := decodeProxy(w, s, r)
	if !ok {
		return
	}
	updated, err := s.auth.Settings.UpdateProxy(r.Context(), id, request, s.wall.Now().Unix())
	if err != nil {
		s.writeProxyError(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, proxyView(updated))
}

// DeleteProxy removes a proxy no indexer uses. Administrator only.
func (s *Server) DeleteProxy(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if err := s.auth.Settings.DeleteProxy(r.Context(), id); err != nil {
		s.writeProxyError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) routeUpdateProxy(w http.ResponseWriter, r *http.Request) {
	if id, err := parsePathID(w, s, r, "id", "a proxy id must be a number"); err == nil {
		s.UpdateProxy(w, r, id)
	}
}

func (s *Server) routeDeleteProxy(w http.ResponseWriter, r *http.Request) {
	if id, err := parsePathID(w, s, r, "id", "a proxy id must be a number"); err == nil {
		s.DeleteProxy(w, r, id)
	}
}
