package main

import (
	"go/parser"
	"go/token"
	"testing"
)

func TestHealthAndDuplicationHelpers(t *testing.T) {
	src := `package demo
// ExportedType docs.
type ExportedType struct{}
var ExportedValue, hidden = 1, 2
func ExportedFunc() { _ = makeCall(); panic("x") }
func makeCall() (int, error) { return 1, nil }
`
	file, err := parser.ParseFile(token.NewFileSet(), "demo.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	parsed := &sourceFile{}
	collectHealthMetrics(file, parsed)
	if parsed.exportedSymbols != 3 || parsed.documentedExportedSymbols != 1 || parsed.panics != 1 {
		t.Fatalf("unexpected health metrics: %+v", parsed)
	}
	if !sameSourcePath("x/pkg/demo.go", "pkg/demo.go") || sameSourcePath("x.go", "pkg/demo.go") {
		t.Fatal("source path comparison failed")
	}
	if classification(9) != "excellent" || classification(7) != "moderate" || classification(3) != "critical" {
		t.Fatal("classification boundaries failed")
	}
}
