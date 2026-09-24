package config_test

import (
	"reflect"
	"testing"

	"github.com/goccy/go-yaml"

	"github.com/makearr/Solarr/internal/platform/config"
)

type restoreShape struct {
	Version    int `yaml:"version"`
	Categories []struct {
		Name  string `yaml:"name"`
		Roots []struct {
			Path string `yaml:"path"`
		} `yaml:"roots"`
		Naming *struct {
			Formats map[string]string `yaml:"formats"`
		} `yaml:"naming"`
	} `yaml:"categories"`
}

// The 2026-09-19 finding: root_folders nested under a category was silently ignored.
func TestUnknownKeysIn_FollowsListsAndPointersAndLeavesMapsFree(t *testing.T) {
	var document any
	source := `
version: 1
categories:
  - name: Music
    root_folders: [{path: /music}]
    roots: [{path: /music, weight: 2}]
    naming: {formats: {anything: goes}}
vversion: 1
`
	if err := yaml.Unmarshal([]byte(source), &document); err != nil {
		t.Fatal(err)
	}
	problems := config.UnknownKeysIn(document, reflect.TypeOf(restoreShape{}))
	var got []string
	for _, problem := range problems {
		got = append(got, problem.Path+"~"+problem.Suggestion)
	}
	want := []string{"categories[0].root_folders~", "categories[0].roots[0].weight~", "vversion~version"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("unknown keys = %v, want %v", got, want)
	}
}
