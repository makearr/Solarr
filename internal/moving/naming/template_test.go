package naming_test

import (
	"strings"
	"testing"

	"github.com/makearr/MediaNexus/internal/moving/naming"
)

func mustCompile(t *testing.T, spec string) []naming.Segment {
	t.Helper()
	segments, err := naming.Compile(spec)
	if err != nil {
		t.Fatalf("Compile(%q): %v", spec, err)
	}
	return segments
}

func TestCompile_PlainToken(t *testing.T) {
	segments := mustCompile(t, "{Track Title}")
	rendered, warnings := naming.Render(segments, naming.Context{"Track Title": "Geninon Vemum"}, 0)
	if rendered != "Geninon Vemum" || len(warnings) != 0 {
		t.Errorf("rendered = %q warnings=%v, want \"Geninon Vemum\" and none", rendered, warnings)
	}
}

func TestCompile_ZeroPadding(t *testing.T) {
	segments := mustCompile(t, "{track:00}")
	rendered, _ := naming.Render(segments, naming.Context{"track": "2"}, 0)
	if rendered != "02" {
		t.Errorf("rendered = %q, want \"02\"", rendered)
	}
}

func TestCompile_ZeroPaddingOnNonNumberWarns(t *testing.T) {
	segments := mustCompile(t, "{track:00}")
	rendered, warnings := naming.Render(segments, naming.Context{"track": "not-a-number"}, 0)
	if rendered != "not-a-number" {
		t.Errorf("rendered = %q, want the raw value kept as a fallback", rendered)
	}
	if len(warnings) != 1 {
		t.Fatalf("len(warnings) = %d, want 1", len(warnings))
	}
}

func TestCompile_ConditionalBlockEmittedWhenPresent(t *testing.T) {
	segments := mustCompile(t, "Title[ - {Edition}]")
	rendered, _ := naming.Render(segments, naming.Context{"Edition": "Deluxe"}, 0)
	if rendered != "Title - Deluxe" {
		t.Errorf("rendered = %q, want \"Title - Deluxe\"", rendered)
	}
}

func TestCompile_ConditionalBlockOmittedWhenAbsent(t *testing.T) {
	segments := mustCompile(t, "Title[ - {Edition}]")
	rendered, _ := naming.Render(segments, naming.Context{}, 0)
	if rendered != "Title" {
		t.Errorf("rendered = %q, want \"Title\" (conditional dropped entirely)", rendered)
	}
}

func TestCompile_FallbackChainFirstNonEmptyWins(t *testing.T) {
	segments := mustCompile(t, "{Album Title|Work Title}")

	rendered, _ := naming.Render(segments, naming.Context{"Album Title": "Tender Prey"}, 0)
	if rendered != "Tender Prey" {
		t.Errorf("rendered = %q, want \"Tender Prey\" (first alternative present)", rendered)
	}

	rendered, _ = naming.Render(segments, naming.Context{"Work Title": "Fallback Title"}, 0)
	if rendered != "Fallback Title" {
		t.Errorf("rendered = %q, want \"Fallback Title\" (first alternative absent)", rendered)
	}
}

func TestCompile_Truncation(t *testing.T) {
	segments := mustCompile(t, "{Track Title:.10}")
	rendered, _ := naming.Render(segments, naming.Context{"Track Title": "A Very Long Track Title Indeed"}, 0)
	if rendered != "A Very Lon" {
		t.Errorf("rendered = %q, want the first 10 characters", rendered)
	}
}

func TestCompile_TruncationLeavesShortValuesAlone(t *testing.T) {
	segments := mustCompile(t, "{Track Title:.60}")
	rendered, _ := naming.Render(segments, naming.Context{"Track Title": "Short"}, 0)
	if rendered != "Short" {
		t.Errorf("rendered = %q, want \"Short\" unchanged", rendered)
	}
}

func TestCompile_LiteralBraceEscape(t *testing.T) {
	segments := mustCompile(t, "{{literal}}")
	rendered, _ := naming.Render(segments, naming.Context{}, 0)
	if rendered != "{literal}" {
		t.Errorf("rendered = %q, want \"{literal}\"", rendered)
	}
}

func TestCompile_UnclosedBraceIsACompileError(t *testing.T) {
	if _, err := naming.Compile("{Track Title"); err == nil {
		t.Fatal("Compile: expected an error for an unclosed brace, got nil")
	}
}

func TestCompile_UnclosedBracketIsACompileError(t *testing.T) {
	if _, err := naming.Compile("[ - {Edition}"); err == nil {
		t.Fatal("Compile: expected an error for an unclosed bracket, got nil")
	}
}

func TestCompile_UnrecognisedSpecIsACompileError(t *testing.T) {
	if _, err := naming.Compile("{Track Title:xyz}"); err == nil {
		t.Fatal("Compile: expected an error for an unrecognised spec, got nil")
	}
}

func TestCompile_EmptyTokenNameIsACompileError(t *testing.T) {
	if _, err := naming.Compile("{}"); err == nil {
		t.Fatal("Compile: expected an error for an empty token name, got nil")
	}
}

func TestValidateTokens_RejectsUnknownNames(t *testing.T) {
	segments := mustCompile(t, "{Artist Name} - {Unknown Token}")
	err := naming.ValidateTokens(segments, map[string]bool{"Artist Name": true})
	if err == nil {
		t.Fatal("ValidateTokens: expected an error for an unknown token, got nil")
	}
	if !strings.Contains(err.Error(), "Unknown Token") {
		t.Errorf("error = %v, want it to name the unknown token", err)
	}
}

func TestValidateTokens_AcceptsEveryDeclaredName(t *testing.T) {
	segments := mustCompile(t, "{Artist Name}/{Album Title}[ - {Edition}]")
	known := map[string]bool{"Artist Name": true, "Album Title": true, "Edition": true}
	if err := naming.ValidateTokens(segments, known); err != nil {
		t.Errorf("ValidateTokens: %v, want nil", err)
	}
}

// TestRender_SeparatorSubstitutesAValueContainingASlash pins section 11.8's
// own reversal: an artist name with a literal "/" must not be mistaken for
// a path boundary the template did not declare.
func TestRender_SeparatorSubstitutesAValueContainingASlash(t *testing.T) {
	segments := mustCompile(t, "{Artist Name}")
	rendered, _ := naming.Render(segments, naming.Context{"Artist Name": "XT/QK"}, naming.DefaultSeparator)
	if rendered != "XT+QK" {
		t.Errorf("rendered = %q, want \"XT+QK\"", rendered)
	}
}

func TestRender_SeparatorIsConfigurable(t *testing.T) {
	segments := mustCompile(t, "{Artist Name}")
	rendered, _ := naming.Render(segments, naming.Context{"Artist Name": "XT/QK"}, '_')
	if rendered != "XT_QK" {
		t.Errorf("rendered = %q, want \"XT_QK\" with an explicit '_' separator", rendered)
	}
}
