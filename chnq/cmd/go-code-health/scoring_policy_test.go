package main

import (
	"encoding/json"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRecursionDetection(t *testing.T) {
	tests := []struct {
		name string
		code string
		want int
	}{
		{"tail", "func f(n int) int { if n == 0 { return 0 }; return f(n-1) }", 1},
		{"non-tail", "func f(n int) int { if n == 0 { return 1 }; return n*f(n-1) }", 1},
		{"multiple", "func f(n int) int { if n < 2 { return n }; return f(n-1)+f(n-2) }", 2},
		{"void", "func f(n int) { if n > 0 { f(n-1) } }", 1},
		{"parenthesized", "func f() { (f)() }", 1},
		{"generic", "func f[T any](v T) { f[T](v) }", 1},
		{"generic-multiple", "func f[T, U any](t T, u U) { f[T, U](t,u) }", 1},
		{"local-shadow", "func f(n int) int { f := func(x int) int { return x }; return f(n) }", 0},
		{"parameter-shadow", "func f(f func()) { f() }", 0},
		{"block-shadow", "func f() { { f := func() {}; f() }; f() }", 1},
		{"short-initializer", "func f() func() { f := f(); f(); return f }", 1},
		{"var-initializer", "func f() func() { var f = f(); f(); return f }", 1},
		{"var-group", "func f() func() int { var (f = f(); other = f()); _ = other; return f }", 1},
		{"named-result", "func f() (f func()) { f(); return }", 0},
		{"type-parameter", "func f[f ~int]() { _ = f(0) }", 0},
		{"local-type", "func f() { { type f int; _ = f(0) }; f() }", 1},
		{"local-generic-type", "func f() { type G[f ~int] [len([1]f{f(0)})]int; _ = G[int]{} }", 0},
		{"local-generic-type-scope", "func f() { type G[f ~int] [len([1]f{f(0)})]int; _ = G[int]{}; f() }", 1},
		{"local-generic-alias", "func f() { type G[f ~int] = [len([1]f{f(0)})]int; _ = G[int]{}; f() }", 1},
		{"local-generic-constraint", "func f() { type G[P ~[len([1]f{f(0)})]int, f ~int] struct{}; _ = G[[1]int, int]{} }", 0},
		{"local-generic-group", "func f() { type (G[f ~int] [len([1]f{f(0)})]int; H func()); _ = G[int]{}; _ = H(f); f() }", 1},
		{"local-const", "func f() { { const f = 1; _ = f }; f() }", 1},
		{"if-scope", "func f() func() { if f := f(); f != nil { f() } else { f() }; return f() }", 2},
		{"for-scope", "func f() func() { for f := f(); f != nil; f = nil { f() }; return f() }", 2},
		{"range-scope", "func f() []func() { for _, f := range f() { f() }; return f() }", 2},
		{"switch-scope", "func f() func() { switch f := f(); { default: f() }; return f() }", 2},
		{"switch-clause-scope", "func f() { switch { case true: f := func() {}; f(); default: f() }; f() }", 2},
		{"select-clause-scope", "func f() { ch := make(chan func()); select { case f := <-ch: f(); default: f() }; f() }", 2},
		{"type-switch-scope", "func f(x any) { switch f := x.(type) { case func(): f(); default: _ = f }; f(x) }", 1},
		{"closure-excluded", "func f() { _ = func() { f() } }", 0},
		{"mutual", "func f() { g() }; func g() { f() }", 0},
		{"async", "func f() { go f() }", 0},
		{"async-argument", "func f(n int) int { go g(f(n)); return n }; func g(n int) {}", 1},
		{"method", "type T struct{}; func (r *T) f() { r.f() }", 1},
		{"method-parenthesized", "type T struct{}; func (r T) f() { (r).f() }", 1},
		{"method-dereferenced", "type T struct{}; func (r *T) f() { (*r).f() }", 1},
		{"method-addressed", "type T struct{}; func (r T) f() { (&r).f() }", 1},
		{"method-nested-dereference", "type T struct{}; func (r T) f() { (*(&r)).f() }", 1},
		{"method-nested-address", "type T struct{}; func (r *T) f() { (&(*r)).f() }", 1},
		{"method-other-dereferenced", "type T struct{}; func (r *T) f(other *T) { (*other).f(other) }", 0},
		{"method-other-addressed", "type T struct{}; func (r T) f(other T) { (&other).f(other) }", 0},
		{"method-dereferenced-shadow", "type T struct{}; func (r *T) f() { { r := &T{}; (*r).f() }; (*r).f() }", 1},
		{"method-addressed-shadow", "type T struct{}; func (r T) f() { { r := T{}; (&r).f() }; (&r).f() }", 1},
		{"method-dereferenced-reassigned", "type T struct{}; func (r *T) f(other *T) { r = other; (*r).f(other) }", 0},
		{"method-addressed-reassigned", "type T struct{}; func (r T) f(other T) { r = other; (&r).f(other) }", 0},
		{"method-other-unary", "type T int; func (r T) f() { (-r).f() }", 0},
		{"method-other-receiver", "type T struct{}; func (r T) f(other T) { other.f() }", 0},
		{"method-shadow", "type T struct{}; func (r T) f() { { r := T{}; r.f() } }", 0},
		{"method-shadow-then-self", "type T struct{}; func (r T) f() { { r := T{}; r.f() }; r.f() }", 1},
		{"method-shadow-assignment", "type T struct{}; func (r T) f() { { r := T{}; r = T{}; r.f() }; r.f() }", 1},
		{"method-shadow-initializer", "type T struct{}; func (r T) f() T { { r := r.f(); r.f() }; return r }", 1},
		{"method-if-shadow", "type T struct{}; func (r T) f() { if r := (T{}); true { r.f() }; r.f() }", 1},
		{"method-range-shadow", "type T struct{}; func (r T) f() { for _, r := range []T{} { r.f() }; r.f() }", 1},
		{"method-closure-shadow", "type T struct{}; func (r T) f() { _ = func(r T) { r = T{} }; r.f() }", 1},
		{"method-closure-local-shadow", "type T struct{}; func (r T) f() { _ = func() { r := T{}; r = T{} }; r.f() }", 1},
		{"method-closure-result-shadow", "type T struct{}; func (r T) f() { _ = func() (r T) { r = T{}; return }; r.f() }", 1},
		{"method-closure-reassignment", "type T struct{}; func (r T) f() { _ = func() { r = T{} }; r.f() }", 0},
		{"method-short-reassignment", "type T struct{}; func (r T) f() { r, x := T{}, 1; _ = x; r.f() }", 0},
		{"method-range-reassignment", "type T struct{}; func (r T) f() { for _, r = range []T{} {}; r.f() }", 0},
		{"method-range-key-reassignment", "type T int; func (r T) f() { for r = range map[T]bool{} {}; r.f() }", 0},
		{"method-increment", "type T int; func (r T) f() { r++; r.f() }", 0},
		{"method-type-switch-shadow", "type T struct{}; func (r T) f(x any) { switch r := x.(type) { case T: r = T{}; r.f(nil) }; r.f(x) }", 1},
		{"method-reassigned", "type T struct{}; func (r T) f(other T) { r = other; r.f() }", 0},
		{"method-vs-function", "type T struct{}; func (r T) f() { f() }; func f() {}", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, err := scanGo("fixture.go", "fixture.go", []byte("package demo\n"+tt.code))
			if err != nil {
				t.Fatal(err)
			}
			if got := file.funcs[0].report.RecursiveCalls; got != tt.want {
				t.Fatalf("recursive call count = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestRecursionPolicyAfterBindingResolution(t *testing.T) {
	tests := []struct {
		name  string
		code  string
		calls int
		rule  string
	}{
		{"dereference", "type T struct{}; func (r *T) f() { (*r).f() }", 1, "undocumented-recursion"},
		{"address", "type T struct{}; func (r T) f() { (&r).f() }", 1, "undocumented-recursion"},
		{"documented-dereference", "type T struct{}\n// chnq:recursion Bounded tree traversal.\nfunc (r *T) f() { (*r).f() }", 1, ""},
		{"documented-address", "type T struct{}\n// chnq:recursion Bounded tree traversal.\nfunc (r T) f() { (&r).f() }", 1, ""},
		{"conversion", "func f() { type G[f ~int] [len([1]f{f(0)})]int; _ = G[int]{} }", 0, ""},
		{"documented-conversion", "// chnq:recursion Bounded tree traversal.\nfunc f() { type G[f ~int] [len([1]f{f(0)})]int; _ = G[int]{} }", 0, "stale-recursion-directive"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, err := scanGo("fixture.go", "fixture.go", []byte("package demo\n"+tt.code))
			if err != nil {
				t.Fatal(err)
			}
			fn := file.funcs[0].report
			if fn.RecursiveCalls != tt.calls {
				t.Fatalf("recursive call count = %d, want %d", fn.RecursiveCalls, tt.calls)
			}
			policy := scoringPolicy{recursionCheck: true, maxFunctionLines: 80}
			wantComplexity := fn.BranchingComplexity
			if tt.calls > 0 {
				wantComplexity++
			}
			if got := policy.effectiveComplexity(fn); got != wantComplexity {
				t.Fatalf("effective complexity = %d, want %d", got, wantComplexity)
			}
			findings := recursionAndGotoFindings(fn, policy)
			if tt.rule == "" {
				if len(findings) != 0 {
					t.Fatalf("unexpected findings: %+v", findings)
				}
			} else if len(findings) != 1 || findings[0].Rule != tt.rule {
				t.Fatalf("findings = %+v, want %s", findings, tt.rule)
			}
		})
	}
}

func TestGotoOwnership(t *testing.T) {
	source := `package demo
func f() {
start:
	for {
		break
	}
outer:
	for {
		continue outer
	}
	goto start
	_ = func() {
	inner:
		goto inner
	}
}
`
	file, err := scanGo("fixture.go", "fixture.go", []byte(source))
	if err != nil {
		t.Fatal(err)
	}
	if len(file.funcs) != 2 || file.funcs[0].report.GotoCount != 1 || file.funcs[1].report.GotoCount != 1 {
		t.Fatalf("goto ownership incorrect: %+v", snapshotFunctions(file.funcs))
	}
}

func TestRecursionDocumentation(t *testing.T) {
	tests := []struct {
		comment   string
		present   bool
		justified bool
	}{
		{"", false, false},
		{"// chnq:recursion\n", true, false},
		{"// chnq:recursion   \n", true, false},
		{"// chnq:recursion Tree traversal is clearer; depth is bounded.\n", true, true},
		{"// chnq:recursion\n// Tree traversal is clearer; depth is bounded.\n", true, true},
		{"/* chnq:recursion Tree traversal is clearer. */\n", true, true},
		{"/*\n * chnq:recursion\n * Tree traversal is clearer.\n */\n", true, true},
		{"// mention chnq:recursion in prose\n", false, false},
		{"// chnq:recursion-other Not a directive.\n", false, false},
		{"// chnq:recursion\n//\n// Unrelated section.\n", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.comment, func(t *testing.T) {
			file, err := scanGo("fixture.go", "fixture.go", []byte("package demo\n"+tt.comment+"func f() { f() }\n"))
			if err != nil {
				t.Fatal(err)
			}
			fn := file.funcs[0].report
			if fn.RecursionDirective != tt.present || fn.RecursionJustified != tt.justified {
				t.Fatalf("directive flags = %t/%t, want %t/%t", fn.RecursionDirective, fn.RecursionJustified, tt.present, tt.justified)
			}
		})
	}
}

func TestPolicyComplexityAndFindings(t *testing.T) {
	fn := &functionData{report: FunctionReport{
		Name: "f", Line: 3, Lines: 50, BranchingComplexity: 15, GotoCount: 1, RecursiveCalls: 2,
	}}
	tests := []struct {
		name   string
		policy scoringPolicy
		want   int
		rules  []string
	}{
		{"disabled", scoringPolicy{maxFunctionLines: 80}, 15, nil},
		{"goto", scoringPolicy{gotoCheck: true, maxFunctionLines: 80}, 16, []string{"high-complexity", "goto-usage"}},
		{"recursion", scoringPolicy{recursionCheck: true, maxFunctionLines: 80}, 16, []string{"high-complexity", "undocumented-recursion"}},
		{"both", scoringPolicy{gotoCheck: true, recursionCheck: true, maxFunctionLines: 80}, 17, []string{"high-complexity", "goto-usage", "undocumented-recursion"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.policy.effectiveComplexity(fn.report); got != tt.want {
				t.Fatalf("effective complexity = %d, want %d", got, tt.want)
			}
			findings := findingsForFunction(fn, tt.policy)
			var rules []string
			for _, finding := range findings {
				rules = append(rules, finding.Rule)
				if finding.Line != 3 || finding.Severity != "warning" {
					t.Fatalf("unexpected finding location/severity: %+v", finding)
				}
			}
			if !reflect.DeepEqual(rules, tt.rules) {
				t.Fatalf("rules = %v, want %v", rules, tt.rules)
			}
		})
	}
	policy := scoringPolicy{recursionCheck: true, maxFunctionLines: 80}
	fn.report.RecursionDirective, fn.report.RecursionJustified = true, true
	if got := recursionAndGotoFindings(fn.report, policy); len(got) != 0 || policy.effectiveComplexity(fn.report) != 16 {
		t.Fatalf("justification must suppress only the documentation warning: %v", got)
	}
	fn.report.RecursiveCalls = 0
	got := recursionAndGotoFindings(fn.report, policy)
	if len(got) != 1 || got[0].Rule != "stale-recursion-directive" || got[0].Severity != "info" {
		t.Fatalf("expected qualified stale-directive finding, got %v", got)
	}
	fn.report.BranchingComplexity = 25
	fn.report.RecursiveCalls = 1
	if got := findingsForFunction(fn, policy); len(got) != 1 || got[0].Severity != "high" {
		t.Fatalf("effective complexity must drive severity: %v", got)
	}
}

func TestPolicyScoreBoundaries(t *testing.T) {
	fn := &functionData{report: FunctionReport{Name: "f", Lines: 60, BranchingComplexity: 5, GotoCount: 1, RecursiveCalls: 3}}
	file := &sourceFile{funcs: []*functionData{fn}}
	base := scoringPolicy{maxFunctionLines: 80}
	if got := complexityForFile(file, base); got != 10 {
		t.Fatalf("default complexity = %g, want 10", got)
	}
	if got := complexityForFile(file, scoringPolicy{gotoCheck: true, maxFunctionLines: 80}); got != 8 {
		t.Fatalf("goto complexity score = %g, want 8", got)
	}
	if got := complexityForFile(file, scoringPolicy{recursionCheck: true, maxFunctionLines: 80}); got != 8 {
		t.Fatalf("recursion complexity score = %g, want 8", got)
	}
	for _, limit := range []int{1, 60, 61, 80} {
		policy := scoringPolicy{maxFunctionLines: limit}
		for _, lines := range []int{limit / 2, limit - 1, limit, limit + 1, limit + 20} {
			fn.report.Lines = lines
			want := 10 - 0.08*float64(maxInt(0, lines-limit))
			if got := structureForFile(file, policy); math.Abs(got-want) > 1e-9 {
				t.Fatalf("limit %d, lines %d: structure = %g, want %g", limit, lines, got, want)
			}
			findings := findingsForFunction(fn, policy)
			if (len(findings) == 1) != (lines > limit) {
				t.Fatalf("limit %d, lines %d: unexpected findings %v", limit, lines, findings)
			}
		}
	}
}

func TestPolicyConfiguration(t *testing.T) {
	for _, raw := range [][]byte{nil, []byte("{}")} {
		config, err := parseProjectConfig(raw, "policy.json")
		if err != nil || config.GotoCheck || config.RecursionCheck || config.MaxFunctionLines != 80 {
			t.Fatalf("default config %q: %+v, %v", raw, config, err)
		}
	}
	for _, raw := range []string{
		`{"max_function_lines":0}`, `{"max_function_lines":-1}`,
		`{"max_function_lines":1.5}`, `{"goto_check":"true"}`, `{"recursion_check":1}`,
		`{"max_function_lines":null}`, `{"goto_check":null}`, `{"recursion_check":null}`,
	} {
		if _, err := parseProjectConfig([]byte(raw), "policy.json"); err == nil || !strings.Contains(err.Error(), "policy.json") {
			t.Fatalf("expected explicit policy error for %s, got %v", raw, err)
		}
	}
	root := t.TempDir()
	writeFixture(t, root, "f.go", "package demo\nfunc f() { f() }\n")
	writeFixture(t, root, projectConfigFileName, `{"goto_check":true,"recursion_check":true,"max_function_lines":60}`)
	scan, err := resolveScanContext(root, false, false)
	if err != nil || !scan.GotoCheck || !scan.RecursionCheck || scan.MaxFunctionLines != 60 {
		t.Fatalf("resolved policy: %+v, %v", scan, err)
	}
	report, _, err := analyzeScan(scan, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Configuration.GotoCheck || !report.Configuration.RecursionCheck || report.Configuration.MaxFunctionLines != 60 {
		t.Fatalf("report policy: %+v", report.Configuration)
	}
}

func TestPolicySnapshotsAndCacheParity(t *testing.T) {
	p := newParityProject(t)
	p.write("receiver.go", `package demo
type T struct{}
func (r *T) walk() { (*r).walk() }
func (r T) visit() { (&r).visit() }
`)
	p.write("conversion.go", `package demo
func convert() {
	type G[convert ~int] [len([1]convert{convert(0)})]int
	_ = G[int]{}
}
`)
	p.write("f.go", `package demo
// chnq:recursion Recursive traversal is clearer; callers bound depth.
func f(n int) int {
	if n <= 0 { return 0 }
again:
	if n > 10 { n--; goto again }
	return f(n-1)+f(n-2)
}
`)
	initial, _ := p.scanWithCache()
	for _, config := range []string{
		`{"goto_check":true}`,
		`{"recursion_check":true}`,
		`{"goto_check":true,"recursion_check":true,"max_function_lines":4}`,
		`{"goto_check":false,"recursion_check":false,"max_function_lines":80}`,
	} {
		p.write(projectConfigFileName, config)
		report, hit := p.scanWithCache()
		if hit {
			t.Fatalf("policy change must invalidate exact report: %s", config)
		}
		if canonicalReport(t, report) != canonicalReport(t, p.coldScan()) {
			t.Fatalf("restored snapshots differ from cold scan: %s", config)
		}
		if _, hit := p.scanWithCache(); !hit {
			t.Fatalf("unchanged policy should produce exact hit: %s", config)
		}
	}
	final, _ := p.scanWithCache()
	if !reflect.DeepEqual(initial.Dimensions, final.Dimensions) || !reflect.DeepEqual(initial.Files[0].Findings, final.Files[0].Findings) {
		t.Fatal("explicit defaults changed original scores or findings")
	}
	cache, err := p.cacheMgr.ReadCache()
	if err != nil {
		t.Fatal(err)
	}
	fn := cache.Snapshots["f.go"].Functions[0].Report
	if fn.GotoCount != 1 || fn.RecursiveCalls != 2 || !fn.RecursionJustified || fn.EffectiveBranchingComplexity != 0 {
		t.Fatalf("snapshots must preserve raw facts only: %+v", fn)
	}
	encoded, err := json.Marshal(cache.Snapshots["f.go"])
	if err != nil {
		t.Fatal(err)
	}
	var restored FileSnapshot
	if err := json.Unmarshal(encoded, &restored); err != nil || !reflect.DeepEqual(restored, cache.Snapshots["f.go"]) {
		t.Fatalf("snapshot JSON round trip: %v", err)
	}
	for _, fn := range cache.Snapshots["receiver.go"].Functions {
		if fn.Report.RecursiveCalls != 1 {
			t.Fatalf("receiver snapshot must retain recursion: %+v", fn.Report)
		}
	}
	if fn := cache.Snapshots["conversion.go"].Functions[0].Report; fn.RecursiveCalls != 0 {
		t.Fatalf("conversion snapshot must not report recursion: %+v", fn)
	}
	for i := range cache.Snapshots["receiver.go"].Functions {
		cache.Snapshots["receiver.go"].Functions[i].Report.RecursiveCalls = 0
	}
	cache.Snapshots["conversion.go"].Functions[0].Report.RecursiveCalls = 1
	cache.AnalysisRevision = "3"
	cache.ContextHash = "analysis-revision-3"
	data, err := json.Marshal(cache)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, p.root, filepath.Base(p.cacheMgr.cacheFile), string(data))
	if p.cacheMgr.ReusableCache() != nil {
		t.Fatal("old analysis snapshots must not be reused")
	}
	p.assertParity("analysis revision change")
}

func TestPolicyReportAndHotspotExplanations(t *testing.T) {
	fn := &functionData{report: FunctionReport{
		Name: "walk", Line: 2, Lines: 100, BranchingComplexity: 15, GotoCount: 2, RecursiveCalls: 3,
	}}
	file := &sourceFile{
		path: "demo.go", packageName: "demo", lines: 100, funcs: []*functionData{fn},
		cycleScore: 10, interfaceScore: 10, duckTypingScore: 10,
	}
	policy := scoringPolicy{gotoCheck: true, recursionCheck: true, maxFunctionLines: 60}
	report := fileScore(file, false, policy)
	if report.Functions[0].BranchingComplexity != 15 || report.Functions[0].EffectiveBranchingComplexity != 18 {
		t.Fatalf("raw and effective complexity must be separately reported: %+v", report.Functions[0])
	}
	if fn.report.EffectiveBranchingComplexity != 0 {
		t.Fatal("scoring must not mutate snapshot-backed raw facts")
	}
	var text strings.Builder
	writeFileHotspot(&text, 1, report)
	if !strings.Contains(text.String(), "high-complexity") || !strings.Contains(text.String(), "18") {
		t.Fatalf("text report must use effective complexity: %s", text.String())
	}
	explainer := NewHotspotsExplainer(&Report{Files: []FileReport{report}}, "package")
	hotspots := explainer.scopedHotspots()
	if len(hotspots) != 1 {
		t.Fatalf("expected package hotspot, got %v", hotspots)
	}
	measured := explainer.buildMeasuredFindings(&hotspots[0])
	for _, expected := range []string{"goto-usage", "undocumented-recursion", "**18**", "chnq:recursion"} {
		if !strings.Contains(measured, expected) {
			t.Fatalf("hotspot explanation missing %q:\n%s", expected, measured)
		}
	}
}

func TestPolicyDefaultScores(t *testing.T) {
	file := &sourceFile{
		path: "demo.go", lines: 500, duplicateTokens: 25, totalFunctionTokens: 100,
		packageFanOut: 5, cycleScore: 10, interfaceScore: 10, duckTypingScore: 10,
		funcs: []*functionData{{report: FunctionReport{
			Name: "f", Lines: 80, BranchingComplexity: 15, MaxNesting: 4, GotoCount: 2, RecursiveCalls: 2,
		}}},
	}
	report := fileScore(file, true, scoringPolicy{maxFunctionLines: 80})
	if report.Score != 7.3 || *report.Dimensions.Complexity != 6 || *report.Dimensions.Structure != 8.3 ||
		*report.Dimensions.Duplication != 7.5 || *report.Dimensions.Coupling != 8.8 {
		t.Fatalf("unexpected default model values: %+v", report)
	}
	if len(report.Findings) != 1 || report.Findings[0].Rule != "duplicate-blocks" {
		t.Fatalf("default policy changed previous findings: %v", report.Findings)
	}
}

func TestScoringVersionInvalidatesOldModelCache(t *testing.T) {
	p := newParityProject(t)
	p.write("f.go", "package demo\nfunc f() {}\n")
	p.scanWithCache()
	cache, err := p.cacheMgr.ReadCache()
	if err != nil {
		t.Fatal(err)
	}
	cache.ScoringVersion = "go-v0.3.0"
	cache.Report.ScoringVersion = cache.ScoringVersion
	data, err := json.Marshal(cache)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, p.root, filepath.Base(p.cacheMgr.cacheFile), string(data))
	if p.cacheMgr.ValidCache(cache.ContextHash, cache.ManifestHash) != nil || p.cacheMgr.ReusableCache() != nil {
		t.Fatal("old scoring model must invalidate cached reports and snapshots")
	}
	report, hit := p.scanWithCache()
	if hit || report.ScoringVersion != scoringVersion {
		t.Fatalf("expected a fresh current-model report, got hit=%t, version=%q", hit, report.ScoringVersion)
	}
	p.assertParity("scoring version change")
}
