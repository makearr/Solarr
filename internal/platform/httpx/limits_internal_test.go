package httpx

import (
	"testing"
	"time"
)

func TestConfig_LimitsForPrefersTheNameThenTheLongestPrefixThenTheDefaults(t *testing.T) {
	config := Config{
		Defaults:  Limits{MaxConcurrent: 4},
		Providers: map[string]Limits{"indexer:7": {MinInterval: time.Second}},
		Prefixes: map[string]Limits{
			"indexer:":      {MinInterval: 500 * time.Millisecond},
			"indexer:slow:": {MinInterval: 3 * time.Second},
		},
	}
	want := map[string]Limits{
		"indexer:7":      {MinInterval: time.Second, MaxConcurrent: 4},
		"indexer:8":      {MinInterval: 500 * time.Millisecond, MaxConcurrent: 4},
		"indexer:slow:1": {MinInterval: 3 * time.Second, MaxConcurrent: 4},
		"elsewhere":      {MaxConcurrent: 4},
	}
	for provider, expected := range want {
		if got := config.limitsFor(provider); got != expected {
			t.Errorf("limitsFor(%q) = %+v, want %+v", provider, got, expected)
		}
	}
}
