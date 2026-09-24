package naming_test

import (
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/moving/naming"
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
	rendered, warnings := naming.Render(segments, naming.Context{"Track Title": "Geninon Vemum"}, naming.DefaultOptions)
	if rendered != "Geninon Vemum" || len(warnings) != 0 {
		t.Errorf("rendered = %q warnings=%v, want \"Geninon Vemum\" and none", rendered, warnings)
	}
}

func TestCompile_ZeroPadding(t *testing.T) {
	segments := mustCompile(t, "{track:00}")
	rendered, _ := naming.Render(segments, naming.Context{"track": "2"}, naming.DefaultOptions)
	if rendered != "02" {
		t.Errorf("rendered = %q, want \"02\"", rendered)
	}
}

func TestCompile_ZeroPaddingOnNonNumberWarns(t *testing.T) {
	segments := mustCompile(t, "{track:00}")
	rendered, warnings := naming.Render(segments, naming.Context{"track": "not-a-number"}, naming.DefaultOptions)
	if rendered != "not-a-number" {
		t.Errorf("rendered = %q, want the raw value kept as a fallback", rendered)
	}
	if len(warnings) != 1 {
		t.Fatalf("len(warnings) = %d, want 1", len(warnings))
	}
}

func TestCompile_ConditionalBlockEmittedWhenPresent(t *testing.T) {
	segments := mustCompile(t, "Title[ - {Edition}]")
	rendered, _ := naming.Render(segments, naming.Context{"Edition": "Deluxe"}, naming.DefaultOptions)
	if rendered != "Title - Deluxe" {
		t.Errorf("rendered = %q, want \"Title - Deluxe\"", rendered)
	}
}

func TestCompile_ConditionalBlockOmittedWhenAbsent(t *testing.T) {
	segments := mustCompile(t, "Title[ - {Edition}]")
	rendered, _ := naming.Render(segments, naming.Context{}, naming.DefaultOptions)
	if rendered != "Title" {
		t.Errorf("rendered = %q, want \"Title\" (conditional dropped entirely)", rendered)
	}
}

func TestCompile_FallbackChainFirstNonEmptyWins(t *testing.T) {
	segments := mustCompile(t, "{Album Title|Work Title}")

	rendered, _ := naming.Render(segments, naming.Context{"Album Title": "Tender Prey"}, naming.DefaultOptions)
	if rendered != "Tender Prey" {
		t.Errorf("rendered = %q, want \"Tender Prey\" (first alternative present)", rendered)
	}

	rendered, _ = naming.Render(segments, naming.Context{"Work Title": "Fallback Title"}, naming.DefaultOptions)
	if rendered != "Fallback Title" {
		t.Errorf("rendered = %q, want \"Fallback Title\" (first alternative absent)", rendered)
	}
}

func TestCompile_Truncation(t *testing.T) {
	segments := mustCompile(t, "{Track Title:.10}")
	rendered, _ := naming.Render(segments, naming.Context{"Track Title": "A Very Long Track Title Indeed"}, naming.DefaultOptions)
	if rendered != "A Very Lon" {
		t.Errorf("rendered = %q, want the first 10 characters", rendered)
	}
}

func TestCompile_TruncationLeavesShortValuesAlone(t *testing.T) {
	segments := mustCompile(t, "{Track Title:.60}")
	rendered, _ := naming.Render(segments, naming.Context{"Track Title": "Short"}, naming.DefaultOptions)
	if rendered != "Short" {
		t.Errorf("rendered = %q, want \"Short\" unchanged", rendered)
	}
}

func TestCompile_LiteralBraceEscape(t *testing.T) {
	segments := mustCompile(t, "{{literal}}")
	rendered, _ := naming.Render(segments, naming.Context{}, naming.DefaultOptions)
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

// An artist's own slash must never become a folder the template did not declare.
func TestRender_ASlashInAValueIsReplacedOrRemoved(t *testing.T) {
	segments := mustCompile(t, "{Artist Name}")
	ctx := naming.Context{"Artist Name": "XT/QK"}
	if rendered, _ := naming.Render(segments, ctx, naming.DefaultOptions); rendered != "XT+QK" {
		t.Errorf("replacing: rendered = %q, want \"XT+QK\"", rendered)
	}
	if rendered, _ := naming.Render(segments, ctx, naming.Options{}); rendered != "XTQK" {
		t.Errorf("not replacing: rendered = %q, want \"XTQK\"", rendered)
	}
}

func TestRender_SpellingVariants(t *testing.T) {
	ctx := naming.Context{"Artist Name": "Nick Cave", "track": "5"}
	for spec, want := range map[string]string{
		"{Artist Name}": "Nick Cave",
		"{Artist.Name}": "Nick.Cave",
		"{Artist_Name}": "Nick_Cave",
		"{Artist-Name}": "Nick-Cave",
		"{ARTIST NAME}": "NICK CAVE",
		"{artist name}": "nick cave",
		"{artist.name}": "nick.cave",
		"{track:00}":    "05",
		"{Track:00}":    "05",
	} {
		rendered, _ := naming.Render(mustCompile(t, spec), ctx, naming.DefaultOptions)
		if rendered != want {
			t.Errorf("%s rendered %q, want %q", spec, rendered, want)
		}
	}
}

func TestValidateTokens_IgnoresCaseAndSpelling(t *testing.T) {
	known := map[string]bool{"Artist Name": true}
	for _, spec := range []string{"{ARTIST NAME}", "{Artist.Name}", "{artist_name}"} {
		if err := naming.ValidateTokens(mustCompile(t, spec), known); err != nil {
			t.Errorf("%s: %v", spec, err)
		}
	}
}
