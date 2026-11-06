package indexer

import "testing"

func TestNewClient_AcceptsAPIURLAsBase(t *testing.T) {
	for _, base := range []string{"http://prowlarr:9696/4", "http://prowlarr:9696/4/", "http://prowlarr:9696/4/api", "http://prowlarr:9696/4/api/"} {
		got := NewClient(nil, base, "").apiURL("caps", nil)
		if want := "http://prowlarr:9696/4/api?t=caps"; got != want {
			t.Errorf("base %q: url = %q, want %q", base, got, want)
		}
	}
}
