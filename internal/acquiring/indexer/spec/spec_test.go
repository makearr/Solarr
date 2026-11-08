package spec

import "testing"

func TestEverySpecLoadsAndMostAreSearchable(t *testing.T) {
	specs, err := All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(specs) < 500 {
		t.Fatalf("%d specs, want the 581 of the converted upstream set", len(specs))
	}
	reasons := map[string]int{}
	searchable := 0
	for _, one := range specs {
		if one.ID == "" || one.Name == "" || len(one.Search.Fields) == 0 {
			t.Errorf("spec %q is missing its id, name or fields", one.ID)
		}
		why := one.Unsupported()
		if len(why) == 0 {
			searchable++
		}
		for _, reason := range why {
			reasons[reason]++
		}
	}
	t.Logf("%d of %d searchable; reasons: %v", searchable, len(specs), reasons)
	if searchable < len(specs)/2 {
		t.Errorf("only %d of %d specs are searchable", searchable, len(specs))
	}
}
