package archtest

import (
	"go/ast"
	"path/filepath"
	"slices"
	"sort"
	"testing"
)

// settingsPackageDir is where every (*settings.Cache) scalar setting writer
// is declared.
const settingsPackageDir = "internal/db/settings"

// settingsCallerDirs are the API-reachable write paths a caller-supplied key
// can actually flow through -- the only places Invariant 81 ("an
// API-reachable setting with no registration fails") has anything to say
// about. internal/db/settings itself (config.yml bootstrap seeding via
// Seed*) and internal/jobs/cachebudget (a measurement sweep writing its own
// probed facts) are deliberately out of scope: neither takes a caller-
// supplied key.
var settingsCallerDirs = []string{"internal/api/apiserver", "cmd/solarr/adapters"}

// exemptScalarSetters are (*settings.Cache) methods that write a scalar
// instance_setting row but are deliberately not gated by
// settings.Registry.LookupAll -- registry.go's own NewDefaultRegistry doc
// comment gives the reason for each:
//   - SetCacheMeasurement writes a measurement sweep's own probed facts
//     (cache.measured.*), never a policy write ADR-44 governs.
//   - SetScheduleCadences/SetScheduleEnabled/SetScheduleRunAtStartup are gated by jobs.TaskRegistry.
//     Lookup in adapters.Schedule instead -- schedule.<task>.cadence/enabled
//     is dynamic per registered task, already tier-tagged via jobs.Task.Tier.
var exemptScalarSetters = []string{
	"SetCacheMeasurement", "SetScheduleCadences", "SetScheduleEnabled", "SetScheduleRunAtStartup",
}

// TestSettingsRegistry_EveryScalarSetterCallIsGuarded is Invariant 81: "an
// API-reachable setting with no registration fails" -- ADR-26's "cannot be
// written" is enforced today entirely by hand, one settings.Registry.
// LookupAll(...) call added at each write path alongside its
// *settings.Cache setter call. Nothing stopped a future write path from
// adding the setter call and forgetting the guard; this closes that gap
// statically, mirroring TestScheduledTasks_EveryEnqueueMethodIsRegistered's
// own shape (Invariant 90) one door down.
//
// A (*settings.Cache) method is a scalar setter by construction if its body
// reaches queries.SetInstanceSetting, whether directly (SetImportAutoCommit
// Confidence) or through the shared c.setPolicy chokepoint (every other
// one) -- so a newly added setter is picked up automatically, not only one
// added to a hand-kept list here. Only the three setters explicitly
// exempted above (a different, already-established guard, or no caller-
// supplied key at all) are allowed to skip the check.
func TestSettingsRegistry_EveryScalarSetterCallIsGuarded(t *testing.T) {
	root := repoRoot(t)

	setters := declaredScalarSetters(t, filepath.Join(root, settingsPackageDir))
	if len(setters) == 0 {
		t.Fatal("found no (*Cache) methods reaching queries.SetInstanceSetting at all -- " +
			"the AST walk itself is broken, not that every setter vanished")
	}

	governed := make([]string, 0, len(setters))
	for _, name := range setters {
		if !slices.Contains(exemptScalarSetters, name) {
			governed = append(governed, name)
		}
	}

	for _, dir := range settingsCallerDirs {
		for _, violation := range unguardedSetterCalls(t, filepath.Join(root, dir), governed) {
			t.Errorf("%s calls settings.Cache.%s without a Registry.LookupAll (or .Lookup) "+
				"guard in the same function (Invariant 81)", violation.funcName, violation.setter)
		}
	}
}

// declaredScalarSetters finds every exported (c *Cache) method in dir whose
// body calls c.setPolicy(...) or queries.SetInstanceSetting(...), directly
// or from a nested closure (SetNetworkSettings and friends pass one to
// c.write).
func declaredScalarSetters(t *testing.T, dir string) []string {
	t.Helper()
	var names []string
	forEachGoFile(t, dir, func(file *ast.File) {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || len(fn.Recv.List) != 1 || fn.Body == nil {
				continue
			}
			if !isCachePointerReceiver(fn.Recv.List[0].Type) {
				continue
			}
			if reachesSetInstanceSetting(fn.Body) {
				names = append(names, fn.Name.Name)
			}
		}
	})
	sort.Strings(names)
	return names
}

func isCachePointerReceiver(expr ast.Expr) bool {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	ident, ok := star.X.(*ast.Ident)
	return ok && ident.Name == "Cache"
}

// reachesSetInstanceSetting reports whether node contains a call to
// c.setPolicy(...) or queries.SetInstanceSetting(...) anywhere within it,
// including inside a nested function literal.
func reachesSetInstanceSetting(node ast.Node) bool {
	found := false
	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		receiver, ok := selector.X.(*ast.Ident)
		if !ok {
			return true
		}
		if (receiver.Name == "c" && selector.Sel.Name == "setPolicy") ||
			(receiver.Name == "queries" && selector.Sel.Name == "SetInstanceSetting") {
			found = true
		}
		return true
	})
	return found
}

type unguardedSetter struct {
	funcName string
	setter   string
}

// unguardedSetterCalls finds every function declared directly in dir (not
// recursive) that calls one of the named setter methods without also
// calling something named LookupAll or Lookup anywhere in its own body.
// Scoped to a single enclosing function, matching every guarded write path
// today (the LookupAll/Lookup check and the setter call are always in the
// same handler or adapter method).
func unguardedSetterCalls(t *testing.T, dir string, setters []string) []unguardedSetter {
	t.Helper()
	var violations []unguardedSetter
	forEachGoFile(t, dir, func(file *ast.File) {
		ast.Inspect(file, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				return true
			}
			called := calledSetters(fn.Body, setters)
			if len(called) == 0 {
				return false
			}
			if callsRegistryLookup(fn.Body) {
				return false
			}
			for _, setter := range called {
				violations = append(violations, unguardedSetter{funcName: fn.Name.Name, setter: setter})
			}
			return false
		})
	})
	return violations
}

func calledSetters(node ast.Node, setters []string) []string {
	var found []string
	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if slices.Contains(setters, selector.Sel.Name) && !slices.Contains(found, selector.Sel.Name) {
			found = append(found, selector.Sel.Name)
		}
		return true
	})
	return found
}

func callsRegistryLookup(node ast.Node) bool {
	found := false
	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if selector.Sel.Name == "LookupAll" || selector.Sel.Name == "Lookup" {
			found = true
		}
		return true
	})
	return found
}
