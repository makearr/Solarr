package apiserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/config"
)

// LibrarySuggestion is the library a work belongs in by the libraries' rules, and why (plan 10-libraries L5).
type LibrarySuggestion struct {
	CategoryID   int64
	Name, Reason string
}

// LibraryCheck is a library's works that belong elsewhere and the kinds of library its works ask for.
type LibraryCheck struct {
	Misplaced    []MisplacedWork
	MissingKinds []MissingKind
}

// MisplacedWork is one work whose rules point to another library.
type MisplacedWork struct {
	WorkID, FromCategoryID int64
	Title, MediaType       string
	Suggestion             LibrarySuggestion
}

// MissingKind is a kind of library that does not exist yet, and how many of the library's works it would hold.
type MissingKind struct {
	MediaType, Kind, Label string
	Works                  int64
}

// LibrarySuggester checks a library's works and names a type's kinds' rules. Defined here, implemented by the caller.
type LibrarySuggester interface {
	Check(ctx context.Context, categoryID int64) (LibraryCheck, error)
	KindRules(mediaType, kind string) []settings.LibraryRule
}

func (s *Server) routeGetLibraryRules(w http.ResponseWriter, r *http.Request) {
	if id, ok := s.workIDParam(w, r); ok {
		s.GetLibraryRules(w, r, id)
	}
}

func (s *Server) routeSetLibraryRules(w http.ResponseWriter, r *http.Request) {
	if id, ok := s.workIDParam(w, r); ok {
		s.SetLibraryRules(w, r, id)
	}
}

func (s *Server) routeCheckLibrary(w http.ResponseWriter, r *http.Request) {
	if id, ok := s.workIDParam(w, r); ok {
		s.CheckLibrary(w, r, id)
	}
}

// GetLibraryRules answers a library's rules: its own, else its kind's.
func (s *Server) GetLibraryRules(w http.ResponseWriter, r *http.Request, id int64) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if _, err := access.Category(id, settings.PermissionRead); err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, s.libraryRulesView(id))
}

// SetLibraryRules states a library's own rules, or returns it to its kind's; administrator only.
func (s *Server) SetLibraryRules(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	var body gen.SetLibraryRulesJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "the request body could not be read")
		return
	}
	var rules []settings.LibraryRule
	if body.Rules != nil {
		rules = []settings.LibraryRule{}
		for _, rule := range *body.Rules {
			conditions := make([]settings.RuleCondition, len(rule.Conditions))
			for i, condition := range rule.Conditions {
				conditions[i] = settings.RuleCondition{Field: settings.RuleField(condition.Field), Value: derefOr(condition.Value, "")}
			}
			rules = append(rules, settings.LibraryRule{Conditions: conditions})
		}
	}
	library := s.auth.Settings.Snapshot().LibraryOf(id)
	if _, err := s.auth.Settings.SetCategoryRules(r.Context(), library, rules); err != nil {
		var problems config.Problems
		if errors.As(err, &problems) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "The rules have problems", problems.Report("the rules", "Nothing was changed."))
			return
		}
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, s.libraryRulesView(id))
}

// libraryRulesView is the rules in force for the library id belongs to.
func (s *Server) libraryRulesView(id int64) gen.LibraryRules {
	categories := s.auth.Settings.Snapshot().LibraryCategories(id)
	view := gen.LibraryRules{Rules: []gen.LibraryRule{}, KindRules: []gen.LibraryRule{}}
	if len(categories) == 0 {
		return view
	}
	head := categories[0]
	if s.librarySuggester != nil {
		view.KindRules = rulesView(s.librarySuggester.KindRules(head.MediaType, head.Kind))
	}
	view.Own = head.Rules != nil
	view.Rules = view.KindRules
	if view.Own {
		view.Rules = rulesView(head.Rules)
	}
	return view
}

func rulesView(rules []settings.LibraryRule) []gen.LibraryRule {
	views := make([]gen.LibraryRule, len(rules))
	for i, rule := range rules {
		views[i].Conditions = make([]gen.RuleCondition, len(rule.Conditions))
		for j, condition := range rule.Conditions {
			views[i].Conditions[j] = gen.RuleCondition{Field: gen.RuleConditionField(condition.Field), Value: strPtr(condition.Value)}
		}
	}
	return views
}

// CheckLibrary answers which of a library's works belong elsewhere, to the libraries the caller may add to.
func (s *Server) CheckLibrary(w http.ResponseWriter, r *http.Request, id int64) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if _, err := access.Category(id, settings.PermissionRead); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.librarySuggester == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "checking a library is not available")
		return
	}
	check, err := s.librarySuggester.Check(r.Context(), id)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	view := gen.LibraryCheck{Misplaced: []gen.MisplacedWork{}, MissingKinds: []gen.MissingKind{}}
	for _, work := range check.Misplaced {
		if _, err := access.Category(work.Suggestion.CategoryID, settings.PermissionRequest); err != nil {
			continue
		}
		view.Misplaced = append(view.Misplaced, gen.MisplacedWork{
			WorkId: work.WorkID, Title: work.Title, MediaType: work.MediaType, FromCategoryId: work.FromCategoryID,
			Suggestion: suggestionView(work.Suggestion),
		})
	}
	for _, kind := range check.MissingKinds {
		view.MissingKinds = append(view.MissingKinds, gen.MissingKind{MediaType: kind.MediaType, Kind: kind.Kind, Label: kind.Label, Works: kind.Works})
	}
	writeJSON(w, s.logger, http.StatusOK, view)
}

func suggestionView(suggestion LibrarySuggestion) gen.LibrarySuggestion {
	return gen.LibrarySuggestion{CategoryId: suggestion.CategoryID, Name: suggestion.Name, Reason: suggestion.Reason}
}
