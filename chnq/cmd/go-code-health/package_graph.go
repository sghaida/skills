package main

import "strings"

func withinModule(imp, module string) bool {
	return module != "" && strings.HasPrefix(imp, module+"/")
}

type packageHealth struct {
	incoming int
	depth    int
	cycle    bool
}

func analyzePackageGraph(files []*sourceFile, module string) map[string]packageHealth {
	edges := map[string]map[string]bool{}
	for _, file := range files {
		if edges[file.packagePath] == nil {
			edges[file.packagePath] = map[string]bool{}
		}
		for _, imp := range file.imports {
			if withinModule(imp, module) {
				target := strings.TrimPrefix(imp, module+"/")
				edges[file.packagePath][target] = true
			}
		}
	}
	health := initializePackageHealth(edges)
	recordIncomingDependencies(edges, health)
	calculatePackageDepths(edges, health)
	return health
}

func initializePackageHealth(edges map[string]map[string]bool) map[string]packageHealth {
	health := map[string]packageHealth{}
	for packagePath := range edges {
		health[packagePath] = packageHealth{}
	}
	return health
}

func recordIncomingDependencies(edges map[string]map[string]bool, health map[string]packageHealth) {
	for _, targets := range edges {
		for target := range targets {
			value := health[target]
			value.incoming++
			health[target] = value
		}
	}
}

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

func markPackageCycle(stack []string, health map[string]packageHealth) {
	for _, name := range stack {
		value := health[name]
		value.cycle = true
		health[name] = value
	}
}

func assignPackageDependencies(files []*sourceFile, module string) {
	deps := map[string]map[string]bool{}
	for _, file := range files {
		if deps[file.packagePath] == nil {
			deps[file.packagePath] = map[string]bool{}
		}
		for _, imp := range file.imports {
			if withinModule(imp, module) {
				deps[file.packagePath][imp] = true
				file.internalImports++
			}
		}
	}
	for _, file := range files {
		file.packageFanOut = len(deps[file.packagePath])
	}
}
