package generator

import (
	"container/heap"
	"fmt"
	"os"
	"sort"
	"strings"

	"bp2ninja/pkg/eval"
)

type intHeap []int

func (h intHeap) Len() int           { return len(h) }
func (h intHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h intHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *intHeap) Push(x interface{}) {
	*h = append(*h, x.(int))
}
func (h *intHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

// GetModuleDependencies extracts all referenced module names from an evaluated module.
func GetModuleDependencies(mod *eval.EvaluatedModule) []string {
	if mod == nil {
		return nil
	}
	var raw []string

	// C/C++ and general libraries
	raw = append(raw, mod.GetAllStringList("shared_libs")...)
	raw = append(raw, mod.GetAllStringList("static_libs")...)
	raw = append(raw, mod.GetAllStringList("whole_static_libs")...)
	raw = append(raw, mod.GetAllStringList("header_libs")...)
	raw = append(raw, mod.GetAllStringList("export_header_lib_headers")...)
	raw = append(raw, mod.GetAllStringList("export_shared_lib_headers")...)
	raw = append(raw, mod.GetAllStringList("export_static_lib_headers")...)
	raw = append(raw, mod.GetAllStringList("libs")...)
	raw = append(raw, mod.GetAllStringList("jni_libs")...)
	raw = append(raw, mod.GetAllStringList("runtime_libs")...)

	// Codegen & headers
	raw = append(raw, mod.GetAllStringList("generated_headers")...)
	raw = append(raw, mod.GetAllStringList("export_generated_headers")...)
	raw = append(raw, mod.GetAllStringList("generated_sources")...)

	// Tools
	raw = append(raw, mod.GetStringList("tools")...)
	for _, tf := range mod.GetStringList("tool_files") {
		if strings.HasPrefix(tf, ":") {
			raw = append(raw, strings.TrimPrefix(tf, ":"))
		}
	}

	// Srcs references (:label or //path:label)
	for _, s := range mod.GetAllStringList("srcs") {
		if strings.HasPrefix(s, ":") {
			raw = append(raw, strings.TrimPrefix(s, ":"))
		} else if strings.HasPrefix(s, "//") {
			parts := strings.Split(s, ":")
			if len(parts) == 2 {
				raw = append(raw, parts[1])
			}
		}
	}

	seen := make(map[string]bool)
	var res []string
	for _, dep := range raw {
		dep = strings.TrimSpace(dep)
		if dep == "" || seen[dep] {
			continue
		}
		seen[dep] = true
		res = append(res, dep)
	}
	return res
}

// SortModules checks for missing dependencies, detects circular dependencies,
// and topologically sorts modules by their internal dependencies.
// Missing dependencies are reported to both stderr and stdout.
// Circular dependencies emit a warning and fall back to original declaration order.
func (g *Generator) SortModules(modules []*eval.EvaluatedModule) []*eval.EvaluatedModule {
	if len(modules) == 0 {
		return modules
	}

	definedModules := make(map[string]bool)
	for _, mod := range modules {
		if mod.Name != "" {
			definedModules[mod.Name] = true
		}
	}

	// 1. Check for missing dependencies
	missingSet := make(map[string]bool)
	for _, mod := range modules {
		deps := GetModuleDependencies(mod)
		for _, dep := range deps {
			if !definedModules[dep] {
				missingSet[dep] = true
			}
		}
	}

	if len(missingSet) > 0 {
		var missingList []string
		for dep := range missingSet {
			missingList = append(missingList, dep)
		}
		sort.Strings(missingList)
		g.MissingDeps = missingList
		msg := fmt.Sprintf("warning: missing dependencies: %s\n", strings.Join(missingList, ", "))
		fmt.Fprint(os.Stderr, msg)
		fmt.Fprint(os.Stdout, msg)
	}

	if len(modules) == 1 {
		return modules
	}

	// 2. Build internal dependency graph
	n := len(modules)
	nameToIndices := make(map[string][]int)
	for i, mod := range modules {
		if mod.Name != "" {
			nameToIndices[mod.Name] = append(nameToIndices[mod.Name], i)
		}
	}

	adj := make([]map[int]bool, n)
	revAdj := make([]map[int]bool, n)
	for i := 0; i < n; i++ {
		adj[i] = make(map[int]bool)
		revAdj[i] = make(map[int]bool)
	}

	for i, mod := range modules {
		deps := GetModuleDependencies(mod)
		for _, dep := range deps {
			if dep == mod.Name {
				// Self dependency
				adj[i][i] = true
				revAdj[i][i] = true
				continue
			}
			if definedModules[dep] {
				for _, j := range nameToIndices[dep] {
					if j != i {
						// j precedes i
						adj[j][i] = true
						revAdj[i][j] = true
					}
				}
			}
		}
	}

	// 3. Tarjan's SCC algorithm to detect cycles
	indices := make([]int, n)
	lowlink := make([]int, n)
	onStack := make([]bool, n)
	for i := 0; i < n; i++ {
		indices[i] = -1
	}
	index := 0
	var stack []int
	var sccs [][]int

	var strongConnect func(v int)
	strongConnect = func(v int) {
		indices[v] = index
		lowlink[v] = index
		index++
		stack = append(stack, v)
		onStack[v] = true

		targets := make([]int, 0, len(adj[v]))
		for w := range adj[v] {
			targets = append(targets, w)
		}
		sort.Ints(targets)

		for _, w := range targets {
			if indices[w] == -1 {
				strongConnect(w)
				if lowlink[w] < lowlink[v] {
					lowlink[v] = lowlink[w]
				}
			} else if onStack[w] {
				if indices[w] < lowlink[v] {
					lowlink[v] = indices[w]
				}
			}
		}

		if lowlink[v] == indices[v] {
			var scc []int
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				scc = append(scc, w)
				if w == v {
					break
				}
			}
			sccs = append(sccs, scc)
		}
	}

	for i := 0; i < n; i++ {
		if indices[i] == -1 {
			strongConnect(i)
		}
	}

	// 4. Identify cyclic SCCs
	type cyclicGroup struct {
		minIdx int
		names  []string
		nodes  []int
	}
	var cyclicGroups []cyclicGroup

	for _, scc := range sccs {
		isCycle := false
		if len(scc) > 1 {
			isCycle = true
		} else if len(scc) == 1 && adj[scc[0]][scc[0]] {
			isCycle = true
		}

		if isCycle {
			minIdx := scc[0]
			nameMap := make(map[string]bool)
			var cNames []string
			for _, nodeIdx := range scc {
				if nodeIdx < minIdx {
					minIdx = nodeIdx
				}
				name := modules[nodeIdx].Name
				if name != "" && !nameMap[name] {
					nameMap[name] = true
					cNames = append(cNames, name)
				}
			}
			sort.Strings(cNames)
			cyclicGroups = append(cyclicGroups, cyclicGroup{
				minIdx: minIdx,
				names:  cNames,
				nodes:  scc,
			})
		}
	}

	// Sort cyclic groups by original declaration order (minIdx)
	sort.Slice(cyclicGroups, func(i, j int) bool {
		return cyclicGroups[i].minIdx < cyclicGroups[j].minIdx
	})

	for _, cg := range cyclicGroups {
		if len(cg.names) > 0 {
			g.CircularDeps = append(g.CircularDeps, cg.names)
			msg := fmt.Sprintf("warning: circular dependency detected between: %s\n", strings.Join(cg.names, ", "))
			fmt.Fprint(os.Stderr, msg)
			fmt.Fprint(os.Stdout, msg)
		}

		// Fall back to original declaration order for cyclic modules:
		// Delete any internal edge v -> u where v >= u.
		sccSet := make(map[int]bool, len(cg.nodes))
		for _, u := range cg.nodes {
			sccSet[u] = true
		}
		for _, v := range cg.nodes {
			for u := range adj[v] {
				if sccSet[u] && v >= u {
					delete(adj[v], u)
					delete(revAdj[u], v)
				}
			}
		}
	}

	// 5. Kahn's topological sort with Min-Heap
	inDegree := make([]int, n)
	for i := 0; i < n; i++ {
		inDegree[i] = len(revAdj[i])
	}

	h := &intHeap{}
	heap.Init(h)
	for i := 0; i < n; i++ {
		if inDegree[i] == 0 {
			heap.Push(h, i)
		}
	}

	sortedIndices := make([]int, 0, n)
	for h.Len() > 0 {
		curr := heap.Pop(h).(int)
		sortedIndices = append(sortedIndices, curr)

		targets := make([]int, 0, len(adj[curr]))
		for nxt := range adj[curr] {
			targets = append(targets, nxt)
		}
		sort.Ints(targets)

		for _, nxt := range targets {
			inDegree[nxt]--
			if inDegree[nxt] == 0 {
				heap.Push(h, nxt)
			}
		}
	}

	if len(sortedIndices) < n {
		emitted := make(map[int]bool, len(sortedIndices))
		for _, idx := range sortedIndices {
			emitted[idx] = true
		}
		for i := 0; i < n; i++ {
			if !emitted[i] {
				sortedIndices = append(sortedIndices, i)
			}
		}
	}

	sortedModules := make([]*eval.EvaluatedModule, n)
	for i, idx := range sortedIndices {
		sortedModules[i] = modules[idx]
	}
	return sortedModules
}
