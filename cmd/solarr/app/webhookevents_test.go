package app

import (
	"slices"
	"testing"
)

func TestHealthChanges(t *testing.T) {
	raised, cleared := healthChanges(map[string]string{"a": "x", "b": "y"}, map[string]string{"b": "y", "c": "z", "d": "w"})
	if !slices.Equal(raised, []string{"c", "d"}) || !slices.Equal(cleared, []string{"a"}) {
		t.Errorf("raised %v cleared %v", raised, cleared)
	}
	raised, cleared = healthChanges(map[string]string{"a": "x"}, map[string]string{"a": "changed message"})
	if len(raised) != 0 || len(cleared) != 0 {
		t.Errorf("a changed message is no change: %v %v", raised, cleared)
	}
}
