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

	"github.com/makearr/MediaNexus/internal/api/apiserver"
)

// The compile-time assertion catches a spec operation with no handler; this
// catches the other direction. See documentation/api-generation.md.
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

// Reads the checked-in document, not a generated mirror of it.
func operationsInSpec(t *testing.T) []string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "api", "openapi.yaml"))
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

// Walks what chi actually serves, not a list someone remembered to update.
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
