package providerdef

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"
)

// Code names what is wrong with a file; the interface words it.
type Code string

// The problem codes.
const (
	CodeSyntax            Code = "syntax"
	CodeUnknownField      Code = "unknown_field"
	CodeDuplicateKey      Code = "duplicate_key"
	CodeWrongType         Code = "wrong_type"
	CodeKeyMismatch       Code = "key_mismatch"
	CodeKindMismatch      Code = "kind_mismatch"
	CodeNewerSchema       Code = "newer_schema"
	CodeAliasRefused      Code = "alias_refused"
	CodeTooLarge          Code = "too_large"
	CodeMultipleDocuments Code = "multiple_documents"
	CodeBadEnvelope       Code = "bad_envelope"
	CodeAbsoluteURL       Code = "absolute_url"
	CodeUndeclaredHost    Code = "undeclared_host"
	CodePaceMissing       Code = "pace_missing"
	CodePaceAboveMax      Code = "pace_above_max"
	CodeIgnoredField      Code = "ignored_field"
)

// Severity is whether a problem stops the file from loading.
type Severity string

// An error refuses the file; a warning loads it.
const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Problem is one thing wrong with a file, structured so the interface words it in the person's language.
type Problem struct {
	Severity Severity
	Code     Code
	Line     int
	Column   int
	// Field is the key the problem is about, when it has one.
	Field string
}

var keyPattern = regexp.MustCompile(`^[a-z0-9_-]{2,40}$`)

// Parse reads one definition file of a kind. The problems are all of them found, not the first; a definition is returned
// only when no problem is an error.
func Parse(kind Kind, fileName string, data []byte, source Source) (Definition, []Problem) {
	if len(data) > MaxFileBytes {
		return Definition{}, []Problem{{Severity: SeverityError, Code: CodeTooLarge}}
	}
	if problems := structure(data); len(problems) > 0 {
		return Definition{}, problems
	}
	var file File
	if err := yaml.Unmarshal(data, &file); err != nil {
		return Definition{}, []Problem{classify(err)}
	}
	problems := envelopeProblems(kind, fileName, file)
	if file.Engine == EngineBuiltin && len(problems) == 0 {
		var strict File
		if err := yaml.UnmarshalWithOptions(data, &strict, yaml.Strict()); err != nil {
			problems = append(problems, classify(err))
		}
	}
	if hasError(problems) {
		return Definition{}, problems
	}
	return Definition{File: file, Source: source, Hash: hashOf(data), Raw: data}, problems
}

func envelopeProblems(kind Kind, fileName string, file File) []Problem {
	var problems []Problem
	bad := func(code Code, field string) {
		problems = append(problems, Problem{Severity: SeverityError, Code: code, Field: field})
	}
	switch {
	case file.Schema > SchemaVersion:
		bad(CodeNewerSchema, "schema")
	case file.Schema < 1:
		bad(CodeBadEnvelope, "schema")
	}
	if file.Kind != kind {
		bad(CodeKindMismatch, "kind")
	}
	if !keyPattern.MatchString(file.Key) {
		bad(CodeBadEnvelope, "key")
	} else if strings.TrimSuffix(fileName, ".yaml") != file.Key {
		bad(CodeKeyMismatch, "key")
	}
	if strings.TrimSpace(file.Name) == "" {
		bad(CodeBadEnvelope, "name")
	}
	switch file.Engine {
	case EngineBuiltin, EngineHTTP, EngineSpec:
	default:
		bad(CodeBadEnvelope, "engine")
	}
	return problems
}

// structure refuses what a strict decode cannot see: several documents, and anchors and aliases.
func structure(data []byte) []Problem {
	parsed, err := parser.ParseBytes(data, 0)
	if err != nil {
		return []Problem{classify(err)}
	}
	if len(parsed.Docs) > 1 {
		return []Problem{{Severity: SeverityError, Code: CodeMultipleDocuments, Line: lineOf(parsed.Docs[1].Start)}}
	}
	var problems []Problem
	for _, doc := range parsed.Docs {
		ast.Walk(visitor{found: func(tk *token.Token) {
			problems = append(problems, Problem{Severity: SeverityError, Code: CodeAliasRefused, Line: lineOf(tk), Column: columnOf(tk)})
		}}, doc.Body)
	}
	return problems
}

type visitor struct{ found func(*token.Token) }

func (v visitor) Visit(node ast.Node) ast.Visitor {
	switch node.(type) {
	case *ast.AnchorNode, *ast.AliasNode:
		v.found(node.GetToken())
	}
	return v
}

type positioned interface {
	GetToken() *token.Token
	GetMessage() string
}

// classify turns a decoder error into a problem with the position it names.
func classify(err error) Problem {
	problem := Problem{Severity: SeverityError, Code: CodeSyntax}
	var at positioned
	if !errors.As(err, &at) {
		return problem
	}
	problem.Line, problem.Column = lineOf(at.GetToken()), columnOf(at.GetToken())
	message := strings.ToLower(at.GetMessage())
	switch {
	case strings.Contains(message, "unknown field"):
		problem.Code = CodeUnknownField
	case strings.Contains(message, "already defined") || strings.Contains(message, "duplicate"):
		problem.Code = CodeDuplicateKey
	case strings.Contains(message, "cannot unmarshal") || strings.Contains(message, "cannot decode"):
		problem.Code = CodeWrongType
	}
	return problem
}

func lineOf(tk *token.Token) int {
	if tk == nil || tk.Position == nil {
		return 0
	}
	return tk.Position.Line
}

func columnOf(tk *token.Token) int {
	if tk == nil || tk.Position == nil {
		return 0
	}
	return tk.Position.Column
}

func hasError(problems []Problem) bool {
	for _, problem := range problems {
		if problem.Severity == SeverityError {
			return true
		}
	}
	return false
}

// String words a problem for a log line; the interface words it through i18n.
func (p Problem) String() string {
	return fmt.Sprintf("%s %s at %d:%d", p.Code, p.Field, p.Line, p.Column)
}
