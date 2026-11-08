package main

import "strings"

// withinmodule handles within module
func withinModule(imp, module string) bool {
	return module != "" && strings.HasPrefix(imp, module+"/")
}

// packagehealth represents package health data
type packageHealth struct {
	incoming int
	depth    int
	cycle    bool
}

// analyzepackagegraph handles analyze package graph
func analyzePackageGraph(files []*sourceFile, module string) map[string]packageHealth {
	edges := buildPackageEdges(files, module)
	health := initializePackageHealth(edges)
	recordIncomingDependencies(edges, health)
	calculatePackageDepths(edges, health)
	return health
}

func buildPackageEdges(files []*sourceFile, module string) map[string]map[string]bool {
	edges := map[string]map[string]bool{}
	for _, file := range files {
		if edges[file.packagePath] == nil {
			edges[file.packagePath] = map[string]bool{}
		}
		addPackageEdges(edges[file.packagePath], file.imports, module)
	}
	return edges
}

func addPackageEdges(edges map[string]bool, imports []string, module string) {
	for _, imp := range imports {
		if withinModule(imp, module) {
			target := strings.TrimPrefix(imp, module+"/")
			edges[target] = true
		}
	}
}

// initializepackagehealth handles initialize package health
func initializePackageHealth(edges map[string]map[string]bool) map[string]packageHealth {
	health := map[string]packageHealth{}
	for packagePath := range edges {
		health[packagePath] = packageHealth{}
	}
	return health
}

// recordincomingdependencies handles record incoming dependencies
func recordIncomingDependencies(edges map[string]map[string]bool, health map[string]packageHealth) {
	for _, targets := range edges {
		for target := range targets {
			value := health[target]
			value.incoming++
			health[target] = value
		}
	}
}

// calculatepackagedepths handles calculate package depths
func calculatePackageDepths(edges map[string]map[string]bool, health map[string]packageHealth) {
	state := map[string]int{}
	var visit func(string, []string) int
	visit = func(packagePath string, stack []string) int {
		if state[packagePath] == 1 {
			markPackageCycle(stack, health)
			return 0
		}
		if state[packagePath] == 2 {
			return health[packagePath].depth
		}
		state[packagePath] = 1
		depth := 0
		for target := range edges[packagePath] {
			depth = maxInt(depth, visit(target, append(stack, packagePath)))
		}
		state[packagePath] = 2
		value := health[packagePath]
		value.depth = depth + 1
		health[packagePath] = value
		return value.depth
	}
	for packagePath := range edges {
		visit(packagePath, nil)
	}
}

// markpackagecycle handles mark package cycle
func markPackageCycle(stack []string, health map[string]packageHealth) {
	for _, name := range stack {
		value := health[name]
		value.cycle = true
		health[name] = value
	}
}

// assignpackagedependencies handles assign package dependencies
func assignPackageDependencies(files []*sourceFile, module string) {
	deps := map[string]map[string]bool{}
	initializePackageDependencies(files, deps)
	recordPackageDependencies(files, module, deps)
	assignPackageFanOut(files, deps)
}

// initializepackagedependencies handles initialize package dependencies
func initializePackageDependencies(files []*sourceFile, deps map[string]map[string]bool) {
	for _, file := range files {
		if deps[file.packagePath] == nil {
			deps[file.packagePath] = map[string]bool{}
		}
	}
}

// recordpackagedependencies handles record package dependencies
func recordPackageDependencies(files []*sourceFile, module string, deps map[string]map[string]bool) {
	for _, file := range files {
		for _, imp := range file.imports {
			recordPackageImport(file, module, imp, deps)
		}
	}
}

// recordpackageimport handles record package import
func recordPackageImport(file *sourceFile, module, imp string, deps map[string]map[string]bool) {
	if !withinModule(imp, module) {
		return
	}
	deps[file.packagePath][imp] = true
	file.internalImports++
}

// assignpackagefanout handles assign package fan out
func assignPackageFanOut(files []*sourceFile, deps map[string]map[string]bool) {
	for _, file := range files {
		file.packageFanOut = len(deps[file.packagePath])
	}
}
