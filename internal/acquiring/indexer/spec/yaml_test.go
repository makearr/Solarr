package spec

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func canonical(t *testing.T, one Spec) []byte {
	t.Helper()
	raw, err := json.Marshal(one)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestEveryEmbeddedSpecSurvivesYAMLAndBack(t *testing.T) {
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	for _, one := range all {
		data, err := one.YAML()
		if err != nil {
			t.Fatalf("%s: %v", one.ID, err)
		}
		back, err := FromYAML(data)
		if err != nil {
			t.Fatalf("%s: %v", one.ID, err)
		}
		if !bytes.Equal(canonical(t, one), canonical(t, back)) {
			t.Fatalf("%s changed on the way through YAML", one.ID)
		}
	}
}

func TestACustomSpecWithAnUnknownFieldIsRefused(t *testing.T) {
	_, err := FromYAML([]byte("schema: 1\nkind: indexer\nkey: example-site\nengine: spec\nname: Example\nscript: run()\n"))
	if err == nil || !strings.Contains(err.Error(), "script") {
		t.Errorf("error = %v, want one naming the unknown field", err)
	}
}

func TestACustomSpecReplacesTheEmbeddedOfTheSameIDAndOthersAreAdded(t *testing.T) {
	embedded := []Spec{{ID: "alpha-site", Name: "Alpha"}, {ID: "beta-site", Name: "Beta"}}
	set := newSpecs(embedded, []Spec{{ID: "beta-site", Name: "Beta mirror"}, {ID: "gamma-site", Name: "Gamma"}})
	got := []string{}
	for _, one := range set.All() {
		got = append(got, one.ID+"="+one.Name)
	}
	want := "alpha-site=Alpha,beta-site=Beta mirror,gamma-site=Gamma"
	if strings.Join(got, ",") != want || !set.Overridden("beta-site") || set.Overridden("gamma-site") || !set.Custom("gamma-site") || set.Custom("alpha-site") {
		t.Errorf("set = %v, overridden beta=%v", got, set.Overridden("beta-site"))
	}
}
