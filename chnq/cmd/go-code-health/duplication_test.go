package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDuplicationConfigurationAndReportParsing(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, ".go-code-health.json", `{"duplication_engine":"internal"}`)
	if engine, err := loadDuplicationEngine(root); err != nil || engine != duplicationEngineInternal {
		t.Fatalf("engine load failed: %q %v", engine, err)
	}
	writeFixture(t, root, ".go-code-health.json", `{"duplication_engine":"unknown"}`)
	if _, err := loadDuplicationEngine(root); err == nil {
		t.Fatal("expected invalid engine error")
	}
	writeFixture(t, root, ".go-code-health.json", "{")
	if _, err := loadDuplicationEngine(root); err == nil {
		t.Fatal("expected invalid config error")
	}
	if _, err := readJSCPDReport(filepath.Join(root, "missing")); err == nil {
		t.Fatal("expected missing jscpd report error")
	}
	var encoded jscpdReport
	if err := json.Unmarshal([]byte(`{"statistics":{"total":{"tokens":10,"duplicatedTokens":2,"percentageTokens":20}}}`), &encoded); err != nil {
		t.Fatal(err)
	}
	summary := Summary{}
	applyJSCPDReport(encoded, nil, &summary)
	if summary.DuplicateTokens != 2 || summary.DuplicationPercent != 20 {
		t.Fatalf("unexpected jscpd summary: %+v", summary)
	}
}

func TestJSCPDExecutionAndSourceMapping(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "jscpd")
	writeFixture(t, root, "jscpd", `#!/bin/sh
out=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--output" ]; then shift; out="$1"; fi
  shift
done
printf '%s' '{"statistics":{"total":{"tokens":20,"duplicatedTokens":4,"percentageTokens":20},"formats":{"go":{"sources":{"pkg/a.go":{"tokens":10,"duplicatedTokens":2,"percentageTokens":20}}}}}}' > "$out/jscpd-report.json"
`)
	if err := os.Chmod(script, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	files := []*sourceFile{{path: "pkg/a.go"}}
	summary := Summary{}
	if err := detectWithJSCPDRS(root, files, &summary); err != nil {
		t.Fatal(err)
	}
	if summary.DuplicateTokens != 4 || files[0].externalDuplicateTokens != 2 {
		t.Fatalf("jscpd mapping failed: %+v %+v", summary, files[0])
	}
	if err := runJSCPD("/bin/false", root, root); err == nil {
		t.Fatal("expected jscpd execution failure")
	}
	failScript := filepath.Join(root, "jscpd-fail")
	writeFixture(t, root, "jscpd-fail", "#!/bin/sh\necho failure >&2\nexit 1\n")
	if err := os.Chmod(failScript, 0755); err != nil {
		t.Fatal(err)
	}
	if err := runJSCPD(failScript, root, root); err == nil || !strings.Contains(err.Error(), "failure") {
		t.Fatal("expected stderr from failed jscpd")
	}
	applySourceStatistic("unmatched.go", jscpdStatistic{Tokens: 1}, files)
	if _, err := locateJSCPD(); err != nil {
		t.Fatalf("fake jscpd should be discoverable: %v", err)
	}
}

func TestJSCPDErrors(t *testing.T) {
	root := t.TempDir()
	if err := detectWithJSCPDRS(root, nil, &Summary{}); err == nil {
		t.Fatal("expected missing jscpd error")
	}
	reportDir := t.TempDir()
	writeFixture(t, reportDir, "jscpd-report.json", "{")
	if _, err := readJSCPDReport(reportDir); err == nil {
		t.Fatal("expected malformed report error")
	}
	writeFixture(t, reportDir, "jscpd-report.json", `{"statistics":{"total":{"tokens":1}}}`)
	if _, err := readJSCPDReport(reportDir); err != nil {
		t.Fatal("valid report should parse")
	}
	t.Setenv("PATH", "")
	t.Setenv("HOME", "")
	if _, err := locateJSCPD(); err == nil {
		t.Fatal("expected jscpd lookup failure")
	}
}
