package apiserver

import (
	"reflect"
	"testing"
)

func TestParseLinkedIDs_KeepsOnlyCompletePairs(t *testing.T) {
	got := parseLinkedIDs("a:b=rg-1,c=,=x,plain,d=rg-2")
	want := []LinkedID{{Provider: "a:b", Prefix: "rg-1"}, {Provider: "d", Prefix: "rg-2"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseLinkedIDs = %+v, want %+v", got, want)
	}
	if got := parseLinkedIDs(""); got != nil {
		t.Errorf("parseLinkedIDs(\"\") = %+v, want none", got)
	}
}
