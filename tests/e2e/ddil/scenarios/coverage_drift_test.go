//go:build e2e

package scenarios

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// scenarioFuncRe matches the TestScenario<L>_<Name> functions that make
// up the DDIL suite. The capture group is the single-letter identifier
// (A..F) used everywhere in the plan, bead, and TEST_COVERAGE.md.
var scenarioFuncRe = regexp.MustCompile(`^TestScenario([A-Z])_[A-Za-z0-9]+$`)

// coverageDocRe matches the scenario-letter column in the DDIL table in
// TEST_COVERAGE.md (e.g. `| A | ...`). Anchoring on a leading pipe +
// whitespace keeps the pattern from matching prose mentions of "A" or
// "B" elsewhere in the doc.
var coverageDocRe = regexp.MustCompile(`(?m)^\|\s*([A-F])\s*\|`)

// TestCoverageDrift enforces TEST_COVERAGE.md and the scenarios package
// stay in lock-step. It fails if either:
//   - A TestScenario<L>_... function exists in the scenarios package but
//     TEST_COVERAGE.md's DDIL table has no row for letter <L>.
//   - TEST_COVERAGE.md's DDIL table has a row for letter <L> but no
//     TestScenario<L>_... function exists.
//
// Drift surfaces the most common mistake in rolling out Phase 2/3
// hardening: flipping a scenario's Skip to a real assertion without
// updating the coverage table (or vice versa).
//
// Runs unconditionally — including under DDIL_DRY_RUN=1 — because it
// only reads local source and has no cluster dependency.
func TestCoverageDrift(t *testing.T) {
	scenarioDir := callerDir(t)

	codeLetters, err := lettersFromSource(scenarioDir)
	if err != nil {
		t.Fatalf("extract scenario letters from source: %v", err)
	}
	if len(codeLetters) == 0 {
		t.Fatalf("no TestScenario<L>_... functions found in %s — coverage drift check is meaningful only with scenarios present", scenarioDir)
	}

	docPath := filepath.Join(scenarioDir, "..", "..", "TEST_COVERAGE.md")
	docLetters, err := lettersFromDoc(docPath)
	if err != nil {
		t.Fatalf("extract scenario letters from %s: %v", docPath, err)
	}

	missing := setDiff(codeLetters, docLetters)
	extra := setDiff(docLetters, codeLetters)
	if len(missing) == 0 && len(extra) == 0 {
		return
	}
	if len(missing) > 0 {
		t.Errorf("scenarios present in code but missing from %s DDIL table: %v", filepath.Base(docPath), missing)
	}
	if len(extra) > 0 {
		t.Errorf("scenarios present in %s DDIL table but missing TestScenario<L>_... in code: %v", filepath.Base(docPath), extra)
	}
}

// callerDir returns the directory holding this test file. Used to locate
// sibling *_test.go files and to compute the relative path to
// TEST_COVERAGE.md without hard-coding a fragile relative path at the
// test-binary CWD.
func callerDir(t *testing.T) string {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller(0) failed — cannot locate scenarios dir")
	}
	return filepath.Dir(self)
}

// lettersFromSource AST-parses every *_test.go file in dir (except this
// file and main_test.go, which hold no scenarios) and returns the
// deduplicated sorted set of scenario letters declared by top-level
// TestScenario<L>_... functions.
func lettersFromSource(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	seen := make(map[string]struct{})
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, "_test.go") {
			continue
		}
		if name == "coverage_drift_test.go" || name == "main_test.go" {
			continue
		}
		path := filepath.Join(dir, name)
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil {
				continue
			}
			m := scenarioFuncRe.FindStringSubmatch(fn.Name.Name)
			if m == nil {
				continue
			}
			seen[m[1]] = struct{}{}
		}
	}
	return sortedKeys(seen), nil
}

// lettersFromDoc reads TEST_COVERAGE.md and returns the deduplicated
// sorted set of scenario letters its DDIL table declares. Rows are
// matched by coverageDocRe so prose mentions elsewhere in the file do
// not trigger false positives.
func lettersFromDoc(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	for _, m := range coverageDocRe.FindAllStringSubmatch(string(data), -1) {
		seen[m[1]] = struct{}{}
	}
	return sortedKeys(seen), nil
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// setDiff returns elements in a that are not in b. Both inputs must be
// sorted (they always are, coming from sortedKeys); the linear merge
// keeps the drift check O(n+m) rather than O(nm).
func setDiff(a, b []string) []string {
	var out []string
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] < b[j]:
			out = append(out, a[i])
			i++
		case a[i] > b[j]:
			j++
		default:
			i++
			j++
		}
	}
	out = append(out, a[i:]...)
	return out
}
