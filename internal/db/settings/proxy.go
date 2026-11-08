package settings

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/settingsdb"
	"github.com/makearr/Solarr/internal/platform/config"
)

// ProxyKind is how requests go through a proxy.
type ProxyKind string

// The kinds of proxy an indexer can use.
const (
	ProxyHTTP         ProxyKind = "http"
	ProxySOCKS5       ProxyKind = "socks5"
	ProxyFlareSolverr ProxyKind = "flaresolverr"
)

// Proxy is one entry of the proxy list; an indexer names one by id.
type Proxy struct {
	ID   int64
	Name string
	Kind ProxyKind
	URL  string
	// SourceArrID and SourceExternalID say which Prowlarr proxy it follows; zero for one added by hand.
	SourceArrID, SourceExternalID int64
	CreatedAt, UpdatedAt          int64
}

// ProxyRequest is what a proxy is created or changed with.
type ProxyRequest struct {
	Name string
	Kind ProxyKind
	URL  string
	// SourceArrID and SourceExternalID are set by the Prowlarr sync only.
	SourceArrID, SourceExternalID int64
}

func validateProxy(request ProxyRequest) config.Problems {
	var problems config.Problems
	if strings.TrimSpace(request.Name) == "" {
		problems = append(problems, config.Problem{Path: "name", Expectation: "not empty"})
	}
	scheme := map[ProxyKind][]string{ProxyHTTP: {"http", "https"}, ProxySOCKS5: {"socks5", "socks5h"}, ProxyFlareSolverr: {"http", "https"}}
	allowed, known := scheme[request.Kind]
	if !known {
		return append(problems, config.Problem{Path: "kind", Value: string(request.Kind), Expectation: "one of http, socks5, flaresolverr"})
	}
	parsed, err := url.Parse(strings.TrimSpace(request.URL))
	if err != nil || parsed.Host == "" || !containsString(allowed, parsed.Scheme) {
		problems = append(problems, config.Problem{Path: "url", Value: request.URL, Expectation: "an address starting " + strings.Join(allowed, ":// or ") + "://"})
	}
	return problems
}

func containsString(list []string, value string) bool {
	for _, one := range list {
		if one == value {
			return true
		}
	}
	return false
}

// Proxies reads the proxy list.
func (c *Cache) Proxies(ctx context.Context) ([]Proxy, error) {
	rows, err := c.queries.ListProxies(ctx)
	if err != nil {
		return nil, fmt.Errorf("settings: list proxies: %w", err)
	}
	proxies := make([]Proxy, len(rows))
	for index, row := range rows {
		proxies[index] = convertProxy(row)
	}
	return proxies, nil
}

// GetProxy answers one proxy by id.
func (c *Cache) GetProxy(ctx context.Context, id int64) (Proxy, error) {
	row, err := c.queries.GetProxy(ctx, id)
	if err != nil {
		return Proxy{}, fmt.Errorf("settings: get proxy %d: %w", id, err)
	}
	return convertProxy(row), nil
}

// CreateProxy adds a proxy.
func (c *Cache) CreateProxy(ctx context.Context, request ProxyRequest, at int64) (Proxy, error) {
	if problems := validateProxy(request); len(problems) > 0 {
		return Proxy{}, problems
	}
	var created settingsdb.Proxy
	err := c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		created, err = c.queries.WithTx(tx).CreateProxy(ctx, settingsdb.CreateProxyParams{
			Name: strings.TrimSpace(request.Name), Kind: string(request.Kind), Url: strings.TrimSpace(request.URL),
			SourceArrID: nullPositive(request.SourceArrID), SourceExternalID: nullPositive(request.SourceExternalID),
			CreatedAt: at, UpdatedAt: at,
		})
		return err
	})
	if err != nil {
		return Proxy{}, fmt.Errorf("settings: create proxy: %w", err)
	}
	return convertProxy(created), nil
}

// UpdateProxy changes a proxy's name, kind and address.
func (c *Cache) UpdateProxy(ctx context.Context, id int64, request ProxyRequest, at int64) (Proxy, error) {
	if _, err := c.GetProxy(ctx, id); err != nil {
		return Proxy{}, err
	}
	if problems := validateProxy(request); len(problems) > 0 {
		return Proxy{}, problems
	}
	err := c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return c.queries.WithTx(tx).UpdateProxy(ctx, settingsdb.UpdateProxyParams{
			Name: strings.TrimSpace(request.Name), Kind: string(request.Kind), Url: strings.TrimSpace(request.URL), UpdatedAt: at, ID: id,
		})
	})
	if err != nil {
		return Proxy{}, fmt.Errorf("settings: update proxy %d: %w", id, err)
	}
	return c.GetProxy(ctx, id)
}

// ErrProxyInUse refuses deleting a proxy an indexer uses: the reference is by id, with no foreign key to stop it.
var ErrProxyInUse = fmt.Errorf("settings: an indexer uses this proxy")

// DeleteProxy removes a proxy no indexer uses.
func (c *Cache) DeleteProxy(ctx context.Context, id int64) error {
	indexers, err := c.Indexers(ctx)
	if err != nil {
		return err
	}
	for _, found := range indexers {
		if used := found.ProxyID(); used != nil && *used == id {
			return fmt.Errorf("%w: %s", ErrProxyInUse, found.Name)
		}
	}
	err = c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return c.queries.WithTx(tx).DeleteProxy(ctx, id)
	})
	if err != nil {
		return fmt.Errorf("settings: delete proxy %d: %w", id, err)
	}
	return nil
}

// ResolveEgress is the indexer's egress with its chosen proxy applied: an http or socks5 proxy becomes the proxy URL,
// FlareSolverr its solver address. A proxy that no longer exists leaves the egress as the indexer stored it.
func (c *Cache) ResolveEgress(ctx context.Context, found Indexer) IndexerEgress {
	egress := found.Egress()
	id := found.ProxyID()
	if id == nil {
		return egress
	}
	chosen, err := c.GetProxy(ctx, *id)
	if err != nil {
		return egress
	}
	if chosen.Kind == ProxyFlareSolverr {
		egress.FlareSolverrURL = chosen.URL
	} else {
		egress.ProxyURL = chosen.URL
	}
	return egress
}

func convertProxy(row settingsdb.Proxy) Proxy {
	return Proxy{
		ID: row.ID, Name: row.Name, Kind: ProxyKind(row.Kind), URL: row.Url,
		SourceArrID: row.SourceArrID.Int64, SourceExternalID: row.SourceExternalID.Int64,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func nullPositive(value int64) sql.NullInt64 { return sql.NullInt64{Int64: value, Valid: value > 0} }
