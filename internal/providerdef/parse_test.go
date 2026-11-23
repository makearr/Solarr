package providerdef

import (
	"reflect"
	"strings"
	"testing"
)

const goodDescriptor = `schema: 1
kind: metadata
key: sample-books
name: Sample Books
engine: builtin
description: one sentence
pace:
  per_minute: 20
`

func TestParseReportsEveryProblemCodeWithItsPosition(t *testing.T) {
	cases := []struct {
		name string
		file string
		body string
		kind Kind
		want []Problem
	}{
		{"unknown field", "sample-books.yaml", goodDescriptor + "surprise: 1\n", KindMetadata,
			[]Problem{{Severity: SeverityError, Code: CodeUnknownField, Line: 9, Column: 1}}},
		{"duplicate key", "sample-books.yaml", goodDescriptor + "name: Again\n", KindMetadata,
			[]Problem{{Severity: SeverityError, Code: CodeDuplicateKey, Line: 9, Column: 1}}},
		{"wrong type", "sample-books.yaml", strings.Replace(goodDescriptor, "per_minute: 20", "per_minute: many", 1), KindMetadata,
			[]Problem{{Severity: SeverityError, Code: CodeWrongType, Line: 8, Column: 15}}},
		{"key mismatch", "other.yaml", goodDescriptor, KindMetadata, []Problem{{Severity: SeverityError, Code: CodeKeyMismatch, Field: "key"}}},
		{"kind mismatch", "sample-books.yaml", goodDescriptor, KindIndexer, []Problem{{Severity: SeverityError, Code: CodeKindMismatch, Field: "kind"}}},
		{"newer schema", "sample-books.yaml", strings.Replace(goodDescriptor, "schema: 1", "schema: 2", 1), KindMetadata,
			[]Problem{{Severity: SeverityError, Code: CodeNewerSchema, Field: "schema"}}},
		{"anchor", "sample-books.yaml", goodDescriptor + "media_types: &types [book]\n", KindMetadata,
			[]Problem{{Severity: SeverityError, Code: CodeAliasRefused, Line: 9, Column: 14}}},
		{"two documents", "sample-books.yaml", goodDescriptor + "---\nschema: 1\n", KindMetadata,
			[]Problem{{Severity: SeverityError, Code: CodeMultipleDocuments, Line: 9}}},
		{"too large", "sample-books.yaml", goodDescriptor + "# " + strings.Repeat("x", MaxFileBytes), KindMetadata,
			[]Problem{{Severity: SeverityError, Code: CodeTooLarge}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, got := Parse(c.kind, c.file, []byte(c.body), SourceCustom)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("problems = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestParseAcceptsADescriptor(t *testing.T) {
	def, problems := Parse(KindMetadata, "sample-books.yaml", []byte(goodDescriptor), SourceCustom)
	if len(problems) != 0 || def.Key != "sample-books" || def.Pace.PerMinute != 20 || !def.EnabledByDefault() || def.Hash == "" {
		t.Errorf("def = %+v, problems = %+v", def, problems)
	}
}
