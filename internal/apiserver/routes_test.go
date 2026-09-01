package apiserver_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/goccy/go-yaml"

	"github.com/makearr/MediaNexus/internal/apiserver"
)

// The generated ServerInterface already makes a spec operation with no handler
// a compile error. This covers the direction a compiler cannot see: a route
// registered on the router that the specification does not describe.
//
// That direction matters more than it looks. An endpoint missing from the spec
// is missing from the generated TypeScript client, so the UI cannot call it and
// nobody finds out until someone asks why the feature does not work. It is also
// how an endpoint quietly escapes review: the spec is where the API is agreed.
func TestRoutesMatchTheSpec(t *testing.T) {
	specified := operationsInSpec(t)
	registered := operationsOnRouter(t)

	for _, operation := range registered {
		if !slices.Contains(specified, operation) {
			t.Errorf("%s is routed but not in api/openapi.yaml; add it to the spec so it "+
				"reaches the generated client", operation)
		}
	}
	for _, operation := range specified {
		if !slices.Contains(registered, operation) {
			t.Errorf("%s is in api/openapi.yaml but not routed; the spec promises an "+
				"endpoint that answers 404", operation)
		}
	}
}

// operationsInSpec reads the document rather than an embedded copy of it, so the
// test fails when the checked-in YAML is wrong -- not when a generated mirror of
// it is stale.
func operationsInSpec(t *testing.T) []string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatalf("read the spec: %v", err)
	}

	var document struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatalf("parse the spec: %v", err)
	}
	if len(document.Paths) == 0 {
		t.Fatal("the spec declares no paths; the test would pass vacuously")
	}

	var operations []string
	for path, item := range document.Paths {
		for method := range item {
			if !isHTTPMethod(method) {
				continue // summary, description, parameters shared across methods
			}
			operations = append(operations, format(method, apiserver.APIPrefix+path))
		}
	}
	sort.Strings(operations)
	return operations
}

// operationsOnRouter walks what chi will actually serve, rather than a list
// someone remembered to update.
func operationsOnRouter(t *testing.T) []string {
	t.Helper()

	handler := newServer(t, fakeChecker{}).Handler()
	routes, ok := handler.(chi.Routes)
	if !ok {
		t.Fatalf("Handler() is %T, which cannot be walked for routes", handler)
	}

	var operations []string
	if err := chi.Walk(routes, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		operations = append(operations, format(method, strings.TrimSuffix(route, "/")))
		return nil
	}); err != nil {
		t.Fatalf("walk the routes: %v", err)
	}
	if len(operations) == 0 {
		t.Fatal("no routes were walked; the test would pass vacuously")
	}
	sort.Strings(operations)
	return operations
}

func isHTTPMethod(name string) bool {
	switch strings.ToUpper(name) {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

func format(method, path string) string {
	return fmt.Sprintf("%s %s", strings.ToUpper(method), path)
}
