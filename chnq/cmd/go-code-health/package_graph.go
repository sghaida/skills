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

// calculatepackagedepths uses weighted strongly connected components to bound cyclic depth
func calculatePackageDepths(edges map[string]map[string]bool, health map[string]packageHealth) {
	components := packageComponents(edges)
	graph, weights := packageComponentGraph(edges, components)
	depths := packageComponentDepths(graph, weights)
	for packagePath, component := range components {
		value := health[packagePath]
		value.depth = depths[component]
		value.cycle = weights[component] > 1 || edges[packagePath][packagePath]
		health[packagePath] = value
	}
}

// packagefinishorder records dependency-first traversal order for component discovery
func packageFinishOrder(edges map[string]map[string]bool) []string {
	seen := make(map[string]bool)
	order := make([]string, 0, len(edges))
	var visit func(string)
	visit = func(path string) {
		if seen[path] {
			return
		}
		seen[path] = true
		for target := range edges[path] {
			visit(target)
		}
		order = append(order, path)
	}
	for path := range edges {
		visit(path)
	}
	return order
}

// packagecomponents finds strongly connected groups with a reverse graph traversal
func packageComponents(edges map[string]map[string]bool) map[string]int {
	reverse := make(map[string][]string)
	for source, targets := range edges {
		for target := range targets {
			reverse[target] = append(reverse[target], source)
		}
	}
	components := make(map[string]int)
	var assign func(string, int)
	assign = func(path string, component int) {
		if _, exists := components[path]; exists {
			return
		}
		components[path] = component
		for _, source := range reverse[path] {
			assign(source, component)
		}
	}
	order := packageFinishOrder(edges)
	for i := len(order) - 1; i >= 0; i-- {
		assign(order[i], i)
	}
	return components
}

// packagecomponentgraph collapses cycles and weights each component by its package count
func packageComponentGraph(edges map[string]map[string]bool, components map[string]int) (map[int]map[int]bool, map[int]int) {
	graph := make(map[int]map[int]bool)
	weights := make(map[int]int)
	for path, component := range components {
		weights[component]++
		if graph[component] == nil {
			graph[component] = make(map[int]bool)
		}
		for target := range edges[path] {
			if other := components[target]; other != component {
				graph[component][other] = true
			}
		}
	}
	return graph, weights
}

// packagecomponentdepths computes the longest weighted path in the acyclic component graph
func packageComponentDepths(graph map[int]map[int]bool, weights map[int]int) map[int]int {
	depths := make(map[int]int)
	var visit func(int) int
	visit = func(component int) int {
		if depth := depths[component]; depth > 0 {
			return depth
		}
		depth := 0
		for target := range graph[component] {
			depth = max(depth, visit(target))
		}
		depths[component] = weights[component] + depth
		return depths[component]
	}
	for component := range graph {
		visit(component)
	}
	return depths
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
