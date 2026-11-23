package adapters

import (
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/indexer/spec"
)

func TestASpecFileOfTheOwnerIsCustomAndOneReplacingAShippedSpecIsOverridden(t *testing.T) {
	shipped, err := spec.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	replaced := shipped.All()[0].ID
	set := shipped.With([]spec.Spec{{ID: replaced}, {ID: "invented-site"}})
	if got := specOrigin(set, replaced); got != "overridden" {
		t.Errorf("replaced spec = %q", got)
	}
	if got := specOrigin(set, "invented-site"); got != "custom" {
		t.Errorf("new spec = %q", got)
	}
	if got := specOrigin(set, shipped.All()[1].ID); got != "" {
		t.Errorf("shipped spec = %q", got)
	}
}
