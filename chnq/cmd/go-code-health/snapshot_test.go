package main

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
)

const snapshotFixture = `package demo

import (
	"fmt"
	"sort"
)

// Greeter greets.
type Greeter interface {
	Greet(name string) string
	Count() int
}

type impl struct{ prefix string }

func (i impl) Greet(name string) string { return i.prefix + name }

func (i impl) Count() int { return len(i.prefix) }

// Exported is documented.
func Exported(values []string) {
	sort.Strings(values)
	for _, value := range values {
		if value != "" {
			fmt.Println(value) // TODO: buffer output
		}
	}
	go func() { panic("boom") }()
}

var _ Greeter = impl{}
`

func parseSnapshotFixture(t *testing.T) *sourceFile {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, root, "demo.go", snapshotFixture)
	file, err := scanGo(filepath.Join(root, "demo.go"), "demo.go", []byte(snapshotFixture))
	if err != nil {
		t.Fatal(err)
	}
	return file
}

func TestSnapshotRoundTripPreservesFileLocalData(t *testing.T) {
	original := parseSnapshotFixture(t)
	snapshot := snapshotFile(original)
	restored := restoreFile(snapshot)
	if !reflect.DeepEqual(snapshotFile(restored), snapshot) {
		t.Fatalf("snapshot round trip lost data:\noriginal: %+v\nrestored: %+v", snapshot, snapshotFile(restored))
	}
	if restored.totalFunctionTokens != original.totalFunctionTokens {
		t.Fatalf("token totals differ: %d != %d", restored.totalFunctionTokens, original.totalFunctionTokens)
	}
	if restored.packagePath != original.packagePath || restored.packageName != original.packageName {
		t.Fatal("package identity lost")
	}
	if len(restored.interfaces) != 1 || !restored.interfaces[0].used || len(restored.interfaces[0].methods) != 2 {
		t.Fatalf("interface metadata lost: %+v", restored.interfaces)
	}
	if len(restored.typeMethods["impl"]) != 2 {
		t.Fatalf("receiver methods lost: %+v", restored.typeMethods)
	}
}

func TestSnapshotResetsDerivedState(t *testing.T) {
	original := parseSnapshotFixture(t)
	original.duplicateTokens = 99
	original.packageFanIn = 3
	original.packageImportCycles = 1
	original.gitChurn = 42
	original.cycleScore = 1
	original.funcs[0].duplicateMarkers = []int{1, 2, 3}
	restored := restoreFile(snapshotFile(original))
	if restored.duplicateTokens != 0 || restored.packageFanIn != 0 || restored.packageImportCycles != 0 ||
		restored.gitChurn != 0 || restored.cycleScore != 0 {
		t.Fatalf("project-derived state must reset: %+v", restored)
	}
	for _, fn := range restored.funcs {
		if fn.duplicateMarkers != nil {
			t.Fatal("duplicate markers must reset")
		}
	}
}

func TestSnapshotJSONRoundTrip(t *testing.T) {
	snapshot := snapshotFile(parseSnapshotFixture(t))
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var decoded FileSnapshot
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, snapshot) {
		t.Fatalf("JSON round trip changed snapshot:\n%+v\n%+v", decoded, snapshot)
	}
}

func TestSnapshotFilesKeyedByPath(t *testing.T) {
	file := parseSnapshotFixture(t)
	snapshots := snapshotFiles([]*sourceFile{file})
	if _, ok := snapshots["demo.go"]; !ok || len(snapshots) != 1 {
		t.Fatalf("expected snapshot keyed by relative path, got %v", snapshots)
	}
}
