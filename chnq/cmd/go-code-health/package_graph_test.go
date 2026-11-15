package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

// testcyclicpackagedepths checks deterministic bounds and exact cycle membership
func TestCyclicPackageDepths(t *testing.T) {
	tests := []struct {
		name   string
		edges  map[string]map[string]bool
		depths map[string]int
		cycles map[string]bool
	}{
		{
			name: "acyclic diamond",
			edges: map[string]map[string]bool{
				"a": {"b": true, "c": true}, "b": {"d": true}, "c": {"d": true}, "d": {},
			},
			depths: map[string]int{"a": 3, "b": 2, "c": 2, "d": 1},
		},
		{
			name: "overlapping cycles",
			edges: map[string]map[string]bool{
				"a": {"b": true, "c": true}, "b": {"a": true}, "c": {"a": true},
			},
			depths: map[string]int{"a": 3, "b": 3, "c": 3},
			cycles: map[string]bool{"a": true, "b": true, "c": true},
		},
		{
			name: "cycle member reached through a completed branch",
			edges: map[string]map[string]bool{
				"a": {"b": true, "c": true}, "b": {"a": true}, "c": {"b": true},
			},
			depths: map[string]int{"a": 3, "b": 3, "c": 3},
			cycles: map[string]bool{"a": true, "b": true, "c": true},
		},
		{
			name: "cycle with entry and exit",
			edges: map[string]map[string]bool{
				"entry": {"a": true}, "a": {"b": true}, "b": {"a": true, "tail": true},
				"tail": {"leaf": true}, "leaf": {},
			},
			depths: map[string]int{"entry": 5, "a": 4, "b": 4, "tail": 2, "leaf": 1},
			cycles: map[string]bool{"a": true, "b": true},
		},
		{
			name:   "self import",
			edges:  map[string]map[string]bool{"a": {"a": true}, "leaf": {}},
			depths: map[string]int{"a": 1, "leaf": 1},
			cycles: map[string]bool{"a": true},
		},
		{
			name: "conservative hub cycle",
			edges: map[string]map[string]bool{
				"a": {"b": true, "c": true, "d": true}, "b": {"a": true}, "c": {"a": true}, "d": {"a": true},
			},
			depths: map[string]int{"a": 4, "b": 4, "c": 4, "d": 4},
			cycles: map[string]bool{"a": true, "b": true, "c": true, "d": true},
		},
		{
			name:   "referenced leaf outside analyzed files",
			edges:  map[string]map[string]bool{"a": {"missing": true}},
			depths: map[string]int{"a": 2, "missing": 1},
		},
		{name: "empty", edges: map[string]map[string]bool{}, depths: map[string]int{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for range 128 {
				health := initializePackageHealth(tc.edges)
				recordIncomingDependencies(tc.edges, health)
				calculatePackageDepths(tc.edges, health)
				if len(health) != len(tc.depths) {
					t.Fatalf("unexpected packages: %+v", health)
				}
				for path, depth := range tc.depths {
					if health[path].depth != depth || health[path].cycle != tc.cycles[path] {
						t.Fatalf("%s: expected depth=%d cycle=%t, got %+v", path, depth, tc.cycles[path], health[path])
					}
				}
			}
		})
	}
}

// testcyclicdepthpolicyverdict verifies stable gate failures for the reported cyclic graph
func TestCyclicDepthPolicyVerdict(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "go.mod", "module example.com/cycle\n")
	writeFixture(t, root, "a/a.go", "package a\nimport (_ \"example.com/cycle/b\"; _ \"example.com/cycle/c\")\n")
	writeFixture(t, root, "b/b.go", "package b\nimport _ \"example.com/cycle/a\"\n")
	writeFixture(t, root, "c/c.go", "package c\nimport _ \"example.com/cycle/a\"\n")
	writeFixture(t, root, projectConfigFileName,
		`{"version":1,"minimum_score":0,"health_thresholds":{"maximum_dependency_depth":2}}`)
	var baseline *PolicyResult
	for i := range 24 {
		args := []string{"--path", root, "--check", "--format", "json"}
		if i%2 == 0 {
			args = append(args, "--no-cache")
		}
		code, output, diagnostics := runHealthCLI(t, args...)
		if code != 3 {
			t.Fatalf("cyclic graph changed gate verdict: %d %s", code, diagnostics)
		}
		var report Report
		if err := json.Unmarshal([]byte(output), &report); err != nil {
			t.Fatal(err)
		}
		if report.Summary.Health.ImportCycles != 3 {
			t.Fatalf("cycle membership was lost: %+v", report.Summary.Health)
		}
		for _, pkg := range report.Packages {
			if pkg.DependencyDepth != 3 {
				t.Fatalf("unstable cyclic depth: %+v", report.Packages)
			}
		}
		if baseline != nil && !reflect.DeepEqual(baseline, report.Policy) {
			t.Fatalf("cached and fresh policy checks differ: %+v vs %+v", baseline, report.Policy)
		}
		baseline = report.Policy
	}
}
