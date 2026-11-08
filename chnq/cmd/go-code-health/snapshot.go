package main

import "sort"

// FunctionSnapshot holds file-local metrics for one function, including the
// exact token stream the internal duplication detector compares.
type FunctionSnapshot struct {
	Report FunctionReport `json:"report"`
	Tokens []string       `json:"tokens"`
}

// InterfaceSnapshot holds one interface declaration with its method set and
// the file-local usage flag computed at parse time.
type InterfaceSnapshot struct {
	Name    string   `json:"name"`
	Methods []string `json:"methods"`
	Line    int      `json:"line"`
	Used    bool     `json:"used"`
}

// FileSnapshot holds every metric intrinsic to one source file's contents.
// Project-derived data (duplication coverage, package graph metrics, Git
// churn, interface matching, and scores) is deliberately absent and must be
// recomputed from the complete project model on every scan.
type FileSnapshot struct {
	Path                      string              `json:"path"`
	PackagePath               string              `json:"package_path"`
	PackageName               string              `json:"package_name"`
	Lines                     int                 `json:"lines"`
	Imports                   []string            `json:"imports,omitempty"`
	Functions                 []FunctionSnapshot  `json:"functions,omitempty"`
	Interfaces                []InterfaceSnapshot `json:"interfaces,omitempty"`
	TypeMethods               map[string][]string `json:"type_methods,omitempty"`
	ExportedSymbols           int                 `json:"exported_symbols,omitempty"`
	DocumentedExportedSymbols int                 `json:"documented_exported_symbols,omitempty"`
	IgnoredReturnValues       int                 `json:"ignored_return_values,omitempty"`
	Panics                    int                 `json:"panics,omitempty"`
	TODOs                     int                 `json:"todos,omitempty"`
	FIXMEs                    int                 `json:"fixmes,omitempty"`
	Goroutines                int                 `json:"goroutines,omitempty"`
	ChannelOperations         int                 `json:"channel_operations,omitempty"`
}

// snapshotFile converts a parsed source file into its serializable
// file-local snapshot.
func snapshotFile(f *sourceFile) FileSnapshot {
	return FileSnapshot{
		Path:                      f.path,
		PackagePath:               f.packagePath,
		PackageName:               f.packageName,
		Lines:                     f.lines,
		Imports:                   append([]string(nil), f.imports...),
		Functions:                 snapshotFunctions(f.funcs),
		Interfaces:                snapshotInterfaces(f.interfaces),
		TypeMethods:               snapshotTypeMethods(f.typeMethods),
		ExportedSymbols:           f.exportedSymbols,
		DocumentedExportedSymbols: f.documentedExportedSymbols,
		IgnoredReturnValues:       f.ignoredReturnValues,
		Panics:                    f.panics,
		TODOs:                     f.todos,
		FIXMEs:                    f.fixmes,
		Goroutines:                f.goroutines,
		ChannelOperations:         f.channelOperations,
	}
}

// restoreFile rebuilds a source file from a snapshot with every
// project-derived field reset, so global passes start from a clean state.
func restoreFile(s FileSnapshot) *sourceFile {
	f := &sourceFile{
		path:                      s.Path,
		packagePath:               s.PackagePath,
		packageName:               s.PackageName,
		lines:                     s.Lines,
		imports:                   append([]string(nil), s.Imports...),
		exportedSymbols:           s.ExportedSymbols,
		documentedExportedSymbols: s.DocumentedExportedSymbols,
		ignoredReturnValues:       s.IgnoredReturnValues,
		panics:                    s.Panics,
		todos:                     s.TODOs,
		fixmes:                    s.FIXMEs,
		goroutines:                s.Goroutines,
		channelOperations:         s.ChannelOperations,
		interfaces:                restoreInterfaces(s),
		typeMethods:               restoreTypeMethods(s.TypeMethods),
	}
	restoreFunctions(f, s.Functions)
	return f
}

// snapshotFunctions captures function reports and exact token streams.
func snapshotFunctions(funcs []*functionData) []FunctionSnapshot {
	snapshots := make([]FunctionSnapshot, 0, len(funcs))
	for _, fn := range funcs {
		snapshots = append(snapshots, FunctionSnapshot{
			Report: fn.report,
			Tokens: append([]string(nil), fn.tokens...),
		})
	}
	return snapshots
}

// restoreFunctions rebuilds function data and the file-local token total the
// parser accumulates, leaving duplicate markers unset.
func restoreFunctions(f *sourceFile, snapshots []FunctionSnapshot) {
	f.funcs = make([]*functionData, 0, len(snapshots))
	for _, snapshot := range snapshots {
		f.funcs = append(f.funcs, &functionData{
			report: snapshot.Report,
			tokens: append([]string(nil), snapshot.Tokens...),
		})
		f.totalFunctionTokens += len(snapshot.Tokens)
	}
}

// snapshotInterfaces converts interface metadata to a deterministic form.
func snapshotInterfaces(interfaces []interfaceInfo) []InterfaceSnapshot {
	snapshots := make([]InterfaceSnapshot, 0, len(interfaces))
	for _, info := range interfaces {
		snapshots = append(snapshots, InterfaceSnapshot{
			Name:    info.name,
			Methods: sortedKeys(info.methods),
			Line:    info.line,
			Used:    info.used,
		})
	}
	return snapshots
}

// restoreInterfaces rebuilds interface metadata bound to the file's package.
func restoreInterfaces(s FileSnapshot) []interfaceInfo {
	interfaces := make([]interfaceInfo, 0, len(s.Interfaces))
	for _, snapshot := range s.Interfaces {
		interfaces = append(interfaces, interfaceInfo{
			name:        snapshot.Name,
			methods:     methodSet(snapshot.Methods),
			line:        snapshot.Line,
			packagePath: s.PackagePath,
			used:        snapshot.Used,
		})
	}
	return interfaces
}

// snapshotTypeMethods converts receiver method sets to sorted slices.
func snapshotTypeMethods(typeMethods map[string]map[string]bool) map[string][]string {
	if len(typeMethods) == 0 {
		return nil
	}
	snapshot := make(map[string][]string, len(typeMethods))
	for name, methods := range typeMethods {
		snapshot[name] = sortedKeys(methods)
	}
	return snapshot
}

// restoreTypeMethods rebuilds receiver method sets from sorted slices.
func restoreTypeMethods(snapshot map[string][]string) map[string]map[string]bool {
	typeMethods := make(map[string]map[string]bool, len(snapshot))
	for name, methods := range snapshot {
		typeMethods[name] = methodSet(methods)
	}
	return typeMethods
}

// sortedKeys returns map keys in deterministic order.
func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// methodSet converts a method list back into a set.
func methodSet(methods []string) map[string]bool {
	set := make(map[string]bool, len(methods))
	for _, method := range methods {
		set[method] = true
	}
	return set
}

// snapshotFiles converts parsed files into snapshots keyed by relative path.
func snapshotFiles(files []*sourceFile) map[string]FileSnapshot {
	snapshots := make(map[string]FileSnapshot, len(files))
	for _, file := range files {
		snapshots[file.path] = snapshotFile(file)
	}
	return snapshots
}
