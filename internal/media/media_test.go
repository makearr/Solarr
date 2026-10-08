package media_test

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/moving/naming"
)

type plain struct{ key media.Key }

func (p plain) Descriptor() media.Descriptor {
	return media.Descriptor{Key: p.key, Label: string(p.key)}
}

type named struct{ plain }

func (named) NamingScheme() naming.Scheme                  { return naming.Scheme{} }
func (named) DefaultNamingProfile() settings.NamingProfile { return settings.NamingProfile{} }
func (named) ValidateNaming(settings.NamingProfile) error  { return nil }
func (named) NamingExamples(settings.NamingProfile) []media.NamingExample {
	return []media.NamingExample{{Label: "sample", Path: "a/b"}}
}

func TestNewRegistry_KeepsRegistrationOrder(t *testing.T) {
	registry, err := media.NewRegistry(plain{"second"}, plain{"first"})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	want := []media.Descriptor{{Key: "second", Label: "second"}, {Key: "first", Label: "first"}}
	if got := registry.Descriptors(); !reflect.DeepEqual(got, want) {
		t.Errorf("Descriptors() = %+v, want %+v", got, want)
	}
}

func TestNewRegistry_RefusesARepeatedKey(t *testing.T) {
	if _, err := media.NewRegistry(plain{"same"}, plain{"same"}); err == nil {
		t.Error("NewRegistry accepted one key twice")
	}
}

func TestNewRegistry_RefusesAModuleWithoutAKey(t *testing.T) {
	if _, err := media.NewRegistry(plain{""}); err == nil {
		t.Error("NewRegistry accepted a module with no key")
	}
}

func TestKnown_IsTrueOnlyForARegisteredType(t *testing.T) {
	registry, err := media.NewRegistry(plain{"here"})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if got := [2]bool{registry.Known("here"), registry.Known("absent")}; got != [2]bool{true, false} {
		t.Errorf("Known(here), Known(absent) = %v, want [true false]", got)
	}
}

func TestCapability_AnswersOnlyAModuleThatImplementsIt(t *testing.T) {
	registry, err := media.NewRegistry(plain{"bare"}, named{plain{"with"}})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	_, bare := media.Capability[media.Naming](registry, "bare")
	_, absent := media.Capability[media.Naming](registry, "absent")
	capability, with := media.Capability[media.Naming](registry, "with")
	if got := [3]bool{bare, absent, with}; got != [3]bool{false, false, true} {
		t.Fatalf("has naming (bare, absent, with) = %v, want [false false true]", got)
	}
	want := []media.NamingExample{{Label: "sample", Path: "a/b"}}
	if got := capability.NamingExamples(settings.NamingProfile{}); !reflect.DeepEqual(got, want) {
		t.Errorf("NamingExamples() = %+v, want %+v", got, want)
	}
}

type faceted struct {
	plain
	facets []media.Facet
}

func (f faceted) Descriptor() media.Descriptor {
	descriptor := f.plain.Descriptor()
	descriptor.Facets = f.facets
	return descriptor
}

// A facet reads what exists: a known source, a column the work has, an enum with its choices.
func TestNewRegistry_RefusesAFacetCoreCannotRead(t *testing.T) {
	good := media.Facet{Key: "year", Entity: model.LevelEntityWork, Kind: media.FacetNumber, Source: media.FacetSource{Kind: media.SourceColumn, Name: "year"}}
	if _, err := media.NewRegistry(faceted{plain{"ok"}, []media.Facet{good}}); err != nil {
		t.Fatalf("a year column was refused: %v", err)
	}
	for name, facet := range map[string]media.Facet{
		"a column the work lacks": {Key: "x", Entity: model.LevelEntityWork, Kind: media.FacetText, Source: media.FacetSource{Kind: media.SourceColumn, Name: "colour"}},
		"an unknown source":       {Key: "x", Entity: model.LevelEntityWork, Kind: media.FacetText, Source: media.FacetSource{Kind: "table", Name: "x"}},
		"an enum without choices": {Key: "x", Entity: model.LevelEntityWork, Kind: media.FacetEnum, Source: media.FacetSource{Kind: media.SourceField, Name: "status"}},
		"a field without a name":  {Key: "x", Entity: model.LevelEntityWork, Kind: media.FacetText, Source: media.FacetSource{Kind: media.SourceField}},
	} {
		if _, err := media.NewRegistry(faceted{plain{"bad"}, []media.Facet{facet}}); err == nil {
			t.Errorf("%s was registered", name)
		}
	}
}
