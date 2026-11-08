package main

import (
	"path/filepath"
	"testing"
)

func TestSourceParsingAndModuleHelpers(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "go.mod", "module example.com/demo\n")
	writeFixture(t, root, "bad.go", "package demo\nfunc {\n")
	if _, _, err := parseSourceFile(filepath.Join(root, "missing.go"), root, &Summary{}, &Report{}); err != nil {
		t.Fatal("missing files should become warnings, not errors")
	}
	report := Report{}
	_, ok, err := parseSourceFile(filepath.Join(root, "bad.go"), root, &Summary{}, &report)
	if err != nil || ok || len(report.Warnings) != 1 {
		t.Fatalf("expected parse warning, got ok=%v err=%v warnings=%v", ok, err, report.Warnings)
	}
	if isGoSourcePath("x.go") != true || isGoSourcePath("x.txt") {
		t.Fatal("unexpected Go path classification")
	}
	module, moduleRoot := locateModule(root)
	if moduleName(root) != "example.com/demo" || module != "example.com/demo" || moduleRoot != root {
		t.Fatal("module helpers failed")
	}
	if moduleName(t.TempDir()) != "" {
		t.Fatal("invalid module handling failed")
	}
	if _, ok := parseModuleLine("module"); ok {
		t.Fatal("invalid module line should fail")
	}
	if got, ok := parseModuleLine(`module "example.com/x"`); !ok || got != "example.com/x" {
		t.Fatalf("quoted module parsing failed: %q %v", got, ok)
	}
	if moduleSearchRoot(filepath.Join(root, "bad.go")) != root {
		t.Fatal("module search root for file is wrong")
	}
	direct, err := sourceFiles(filepath.Join(root, "bad.go"), false, &Summary{})
	if err != nil || len(direct) != 1 {
		t.Fatalf("direct source path failed: %v %v", direct, err)
	}
	if lineCount(nil) != 0 || lineCount([]byte("one")) != 1 {
		t.Fatal("line count edge cases failed")
	}
	if minInt(1, 2) != 1 || minInt(2, 1) != 1 {
		t.Fatal("minInt failed")
	}
}
