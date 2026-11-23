package app

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/providerdef"
)

// A provider compiled in without a descriptor that reads back to itself fails here, which keeps every new provider covered.
func TestEveryCompiledProviderHasADescriptorThatParsesBackToItself(t *testing.T) {
	seen := map[providerdef.Kind]map[string]bool{}
	for kind, files := range compiledDescriptors() {
		seen[kind] = map[string]bool{}
		if len(files) == 0 {
			t.Errorf("%s: no descriptors", kind)
		}
		for _, file := range files {
			if seen[kind][file.Key] {
				t.Errorf("%s/%s: two providers share the key", kind, file.Key)
			}
			seen[kind][file.Key] = true
			data, err := providerdef.Marshal(file)
			if err != nil {
				t.Fatalf("%s/%s: %v", kind, file.Key, err)
			}
			def, problems := providerdef.Parse(kind, file.Key+".yaml", data, providerdef.SourceBuiltin)
			if len(problems) != 0 {
				t.Errorf("%s/%s: %v", kind, file.Key, problems)
				continue
			}
			file.Schema = providerdef.SchemaVersion
			if !reflect.DeepEqual(def.File, file) {
				t.Errorf("%s/%s reads back as %+v, want %+v", kind, file.Key, def.File, file)
			}
		}
	}
}
