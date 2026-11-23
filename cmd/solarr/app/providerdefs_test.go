package app

import (
	"testing"

	"github.com/makearr/Solarr/internal/providerdef"
)

func customSpecDefinition(key string) providerdef.Definition {
	raw := []byte("schema: 1\nkind: indexer\nkey: " + key + "\nname: Example\nengine: spec\nlinks:\n  - https://tracker.example.org/\n")
	def, _ := providerdef.Parse(providerdef.KindIndexer, key+".yaml", raw, providerdef.SourceCustom)
	return def
}

func TestACustomSpecIsUsedOnlyOnceApprovedForItsHosts(t *testing.T) {
	def := customSpecDefinition("example-site")
	var reported []string
	problem := func(key string, _ error) { reported = append(reported, key) }
	if got := approvedSpecs([]providerdef.Definition{def}, nil, problem); len(got) != 0 {
		t.Errorf("unapproved spec used: %v", got)
	}
	approved := map[string]providerdef.State{"example-site": {ApprovedHash: def.Hash, ApprovedHosts: def.NormalHosts()}}
	got := approvedSpecs([]providerdef.Definition{def}, approved, problem)
	if len(got) != 1 || got[0].ID != "example-site" || got[0].Links[0] != "https://tracker.example.org/" {
		t.Errorf("approved specs = %+v", got)
	}
	changedHosts := map[string]providerdef.State{"example-site": {ApprovedHash: def.Hash, ApprovedHosts: []string{"elsewhere.example.com"}}}
	if got := approvedSpecs([]providerdef.Definition{def}, changedHosts, problem); len(got) != 0 {
		t.Errorf("spec with a changed host list used: %v", got)
	}
	if len(reported) != 0 {
		t.Errorf("reported = %v", reported)
	}
}
