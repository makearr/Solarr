package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// schedulerDir is where ADR-59's task registry declares every periodic
// action (cmd/medianexus/app.buildTaskRegistry).
const schedulerDir = "cmd/medianexus/app"

// TestScheduledTasks_EveryEnqueueMethodIsRegistered is Invariant 90: "every
// periodic action is registered ... an unregistered schedule fails the
// build." A (*process) method matching Task.Enqueue's own exact shape --
// func(context.Context) (jobs.EnqueueResult, error) -- is a periodic action
// by construction (that shape has no other caller in this codebase); it
// must appear as some Task's Enqueue *or* RunFull field in buildTaskRegistry,
// or this fails. RunFull shares the exact same shape (todo 4.2's full-rescan
// trigger, library scan's own second entry point) and is just as real a
// registration as Enqueue -- a method reachable only through it must not
// read as orphaned.
func TestScheduledTasks_EveryEnqueueMethodIsRegistered(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, schedulerDir)

	declared := declaredEnqueueMethods(t, dir)
	if len(declared) == 0 {
		t.Fatal("found no (*process) methods matching Task.Enqueue's shape at all -- " +
			"the AST walk itself is broken, not that every task vanished")
	}

	registered := registeredTaskEnqueueFuncs(t, dir)
	for _, name := range declared {
		if !slices.Contains(registered, name) {
			t.Errorf("cmd/medianexus/app.(*process).%s matches ADR-59's schedulable shape "+
				"(func(context.Context) (jobs.EnqueueResult, error) on *process) but is not "+
				"registered as a Task in buildTaskRegistry (Invariant 90)", name)
		}
	}
}

// declaredEnqueueMethods finds every (p *process) enqueueX method whose
// signature exactly matches Task.Enqueue's own shape. A method whose name
// starts with "enqueue" but has a different shape (a different receiver, a
// different signature) is not a candidate and is not flagged -- matching
// the real shape, not just the name prefix, is what keeps this precise.
func declaredEnqueueMethods(t *testing.T, dir string) []string {
	t.Helper()
	var names []string
	forEachGoFile(t, dir, func(file *ast.File) {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || len(fn.Recv.List) != 1 {
				continue
			}
			if !strings.HasPrefix(fn.Name.Name, "enqueue") {
				continue
			}
			if !isProcessPointerReceiver(fn.Recv.List[0].Type) {
				continue
			}
			if isTaskEnqueueShape(fn.Type) {
				names = append(names, fn.Name.Name)
			}
		}
	})
	sort.Strings(names)
	return names
}

// registeredTaskEnqueueFuncs finds every `Enqueue: p.enqueueX` or
// `RunFull: p.enqueueX` field inside a jobs.Task{...} composite literal,
// anywhere in dir -- deliberately not limited to one file, so the
// registration can live wherever it is clearest.
func registeredTaskEnqueueFuncs(t *testing.T, dir string) []string {
	t.Helper()
	var names []string
	forEachGoFile(t, dir, func(file *ast.File) {
		ast.Inspect(file, func(n ast.Node) bool {
			composite, ok := n.(*ast.CompositeLit)
			if !ok || !isJobsTaskType(composite.Type) {
				return true
			}
			for _, element := range composite.Elts {
				kv, ok := element.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok || (key.Name != "Enqueue" && key.Name != "RunFull") {
					continue
				}
				selector, ok := kv.Value.(*ast.SelectorExpr)
				if !ok {
					continue
				}
				if receiver, ok := selector.X.(*ast.Ident); ok && receiver.Name == "p" {
					names = append(names, selector.Sel.Name)
				}
			}
			return true
		})
	})
	return names
}

func isProcessPointerReceiver(expr ast.Expr) bool {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	ident, ok := star.X.(*ast.Ident)
	return ok && ident.Name == "process"
}

// isTaskEnqueueShape matches func(context.Context) (jobs.EnqueueResult, error).
func isTaskEnqueueShape(fn *ast.FuncType) bool {
	if fn.Params == nil || len(fn.Params.List) != 1 {
		return false
	}
	if !isSelector(fn.Params.List[0].Type, "context", "Context") {
		return false
	}
	if fn.Results == nil || len(fn.Results.List) != 2 {
		return false
	}
	if !isSelector(fn.Results.List[0].Type, "jobs", "EnqueueResult") {
		return false
	}
	errIdent, ok := fn.Results.List[1].Type.(*ast.Ident)
	return ok && errIdent.Name == "error"
}

func isJobsTaskType(expr ast.Expr) bool {
	return isSelector(expr, "jobs", "Task")
}

func isSelector(expr ast.Expr, pkg, name string) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	return ok && ident.Name == pkg && selector.Sel.Name == name
}

// forEachGoFile parses every non-test .go file directly in dir (no
// recursion needed: cmd/medianexus/app is one package, not a tree) and calls fn
// with each parsed file.
func forEachGoFile(t *testing.T, dir string, fn func(*ast.File)) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		fn(parsed)
	}
}
