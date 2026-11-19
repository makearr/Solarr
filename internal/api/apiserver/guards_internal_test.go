package apiserver

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode"
)

// openHandlers are the handlers that apply no access check of their own: a public read, a check done in a wrapper, or a
// route that is the account's own. A new handler must either check access or be added here on purpose (SEC-1).
var openHandlers = []string{
	"GetMyCalendarFeed", "CreateMyCalendarFeed", "DeleteMyCalendarFeed", "GetCalendarFeedICS", "ListPersonFollows",
	"ListOrganisationFollows", "GetWorkArtworkOptions", "GetSetupStatus", "CreateFirstAdministrator", "GetLibraryNaming",
	"SetLibraryNaming", "ListMediaTypes", "GetFilePreview", "ListTags", "ListFavorites", "ListWorkFranchises",
	"CreateFranchise", "DeleteFranchise", "SetMetadataProviderKey", "GetArrInstanceSyncOptions", "SetArrInstanceSyncOptions",
	"ListArrInstanceIndexers", "ListArrInstanceDownloadClients", "GetArrInstancePull", "SetArrInstancePull", "GetOrganisation",
	"GetAuthPolicy", "SetAuthPolicy", "GetTransportKey", "GetHealth", "GetReady", "GetAbout", "GetAboutLicense",
	"GetAboutAttribution", "GetFilePlayback", "GetFileSubtitle", "SetTheAudioDBAPIKey", "SetTMDBAPIKey", "SetTVDBAPIKey",
	"ListWorkCandidates", "GetGeneralSettings",
}

func TestHandlersCheckAccessOrAreListedAsOpen(t *testing.T) {
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var unchecked []string
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv == nil || function.Body == nil || !handlerName(function.Name.Name) {
				continue
			}
			if !checksAccess(function.Body) && !slices.Contains(openHandlers, function.Name.Name) {
				unchecked = append(unchecked, function.Name.Name)
			}
		}
	}
	slices.Sort(unchecked)
	if len(unchecked) > 0 {
		t.Errorf("handlers with no access check and not listed as open: %v", unchecked)
	}
}

// checksAccess says the body asks who is calling: the administrator guard, the request's access, or a library check.
func checksAccess(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		if identifier, ok := node.(*ast.Ident); ok {
			name := identifier.Name
			if name == "requireAdministrator" || name == "FromContext" || name == "requireLibrary" || strings.Contains(strings.ToLower(name), "access") {
				found = true
			}
		}
		return !found
	})
	return found
}

func handlerName(name string) bool {
	for _, prefix := range []string{"List", "Get", "Set", "Update", "Create", "Delete", "Patch"} {
		if rest, found := strings.CutPrefix(name, prefix); found && rest != "" && unicode.IsUpper(rune(rest[0])) {
			return true
		}
	}
	return false
}
