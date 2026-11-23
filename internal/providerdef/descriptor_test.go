package providerdef

import (
	"slices"
	"testing"
)

func compiledMetadata(t *testing.T) Definition {
	t.Helper()
	data, err := Marshal(File{Kind: KindMetadata, Key: "sample-books", Name: "Sample Books", Engine: EngineBuiltin, Description: "books",
		Pace: &Pace{PerMinute: 30, MaxPerMinute: 60}, MediaTypes: []string{"book", "audiobook"}, NeedsKey: true, Carries: []string{"covers"}, Published: "60 a minute"})
	if err != nil {
		t.Fatal(err)
	}
	def, problems := Parse(KindMetadata, "sample-books.yaml", data, SourceBuiltin)
	if len(problems) != 0 {
		t.Fatalf("built-in descriptor does not parse back: %v", problems)
	}
	return def
}

func customOver(t *testing.T, body string) Definition {
	t.Helper()
	data := []byte("schema: 1\nkind: metadata\nkey: sample-books\nname: Sample Books\nengine: builtin\n" + body)
	def, problems := Parse(KindMetadata, "sample-books.yaml", data, SourceCustom)
	if hasError(problems) {
		t.Fatalf("custom: %v", problems)
	}
	return def
}

func TestADescriptorParsesBackToItsValues(t *testing.T) {
	def := compiledMetadata(t)
	if def.Pace.MaxPerMinute != 60 || !def.NeedsKey || !slices.Equal(def.MediaTypes, []string{"book", "audiobook"}) || def.Carries[0] != "covers" {
		t.Errorf("definition = %+v", def.File)
	}
}

func TestACustomDescriptorTunesTheEnabledPaceAndNarrowedMediaTypes(t *testing.T) {
	catalog, reports := BuildCatalog([]Definition{compiledMetadata(t)}, []Definition{customOver(t, "enabled: false\npace:\n  per_minute: 10\nmedia_types: [book]\n")})
	tuning := catalog.Tuning(KindMetadata, "sample-books")
	if len(reports) != 0 || tuning.Enabled == nil || *tuning.Enabled || tuning.PerMinute != 10 || !slices.Equal(tuning.MediaTypes, []string{"book"}) || !catalog.Hidden(KindMetadata, "sample-books") {
		t.Errorf("tuning = %+v reports = %+v", tuning, reports)
	}
}

func TestAPaceAboveTheCompiledMaximumIsRefusedAndTheBuiltInStays(t *testing.T) {
	catalog, reports := BuildCatalog([]Definition{compiledMetadata(t)}, []Definition{customOver(t, "pace:\n  per_minute: 600\n")})
	if len(reports) != 1 || reports[0].Problems[0].Code != CodePaceAboveMax || catalog.Tuning(KindMetadata, "sample-books").PerMinute != 0 {
		t.Errorf("reports = %+v", reports)
	}
}

func TestAChangedDescribingFieldWarnsAndStillLoads(t *testing.T) {
	catalog, reports := BuildCatalog([]Definition{compiledMetadata(t)}, []Definition{customOver(t, "needs_key: false\npublished: unlimited\nenabled: false\n")})
	got := []Code{}
	for _, report := range reports {
		for _, problem := range report.Problems {
			got = append(got, problem.Code)
		}
	}
	if !slices.Equal(got, []Code{CodeIgnoredField}) || !catalog.Hidden(KindMetadata, "sample-books") {
		t.Errorf("problems = %v", got)
	}
}

func TestMediaTypesMayOnlyNarrow(t *testing.T) {
	_, reports := BuildCatalog([]Definition{compiledMetadata(t)}, []Definition{customOver(t, "media_types: [book, tv]\n")})
	if len(reports) != 1 || reports[0].Problems[0].Field != "media_types" {
		t.Errorf("reports = %+v", reports)
	}
}
