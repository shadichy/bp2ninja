package generator

import (
	"path/filepath"
	"strings"

	"bp2ninja/pkg/eval"
	"bp2ninja/pkg/ninja"
)

func (g *Generator) generateCcBinary(mod *eval.EvaluatedModule) ([]string, error) {
	objs, err := g.compileCcSources(mod)
	if err != nil || len(objs) == 0 {
		return nil, err
	}

	target := filepath.Join(g.binDir(), mod.Name)
	ldflags, libs := g.resolveLinkerArgs(mod)

	vars := map[string]string{}
	if len(ldflags) > 0 {
		vars["ldflags"] = strings.Join(ldflags, " ")
	}
	if len(libs) > 0 {
		vars["libs"] = strings.Join(libs, " ")
	}

	err = g.nw.Build(ninja.BuildEdge{
		Outputs:   []string{target},
		Rule:      "link_binary",
		Inputs:    objs,
		Variables: vars,
	})
	return []string{target}, err
}

func (g *Generator) generateCcLibrary(mod *eval.EvaluatedModule) ([]string, error) {
	if mod.Type == "cc_library_headers" {
		// Header-only library: emit phony target for dependency references
		phonyTarget := filepath.Join(g.opts.OutDir, "headers", mod.Name)
		var genDeps []string
		genHeaders := append(mod.GetStringList("generated_headers"), mod.GetStringList("export_generated_headers")...)
		for _, gh := range genHeaders {
			if outs, ok := g.moduleOutputs[gh]; ok {
				genDeps = append(genDeps, outs...)
			} else if ghMod, ok := g.moduleMap[gh]; ok && ghMod.Type == "genrule" {
				for _, out := range ghMod.GetStringList("out") {
					genDeps = append(genDeps, filepath.Join(g.opts.OutDir, "gen", gh, out))
				}
			}
		}
		err := g.nw.Build(ninja.BuildEdge{
			Outputs:   []string{phonyTarget},
			Rule:      "phony",
			Implicits: dedup(genDeps),
		})
		return []string{phonyTarget}, err
	}

	objs, err := g.compileCcSources(mod)
	if err != nil || len(objs) == 0 {
		return nil, err
	}

	var targets []string
	buildShared := mod.Type == "cc_library" || mod.Type == "cc_library_shared"
	buildStatic := mod.Type == "cc_library" || mod.Type == "cc_library_static"

	ldflags, libs := g.resolveLinkerArgs(mod)
	vars := map[string]string{}
	if len(ldflags) > 0 {
		vars["ldflags"] = strings.Join(ldflags, " ")
	}
	if len(libs) > 0 {
		vars["libs"] = strings.Join(libs, " ")
	}

	if buildShared {
		sharedTarget := filepath.Join(g.libDir(), mod.Name+".so")
		if err := g.nw.Build(ninja.BuildEdge{
			Outputs:   []string{sharedTarget},
			Rule:      "link_shared",
			Inputs:    objs,
			Variables: vars,
		}); err != nil {
			return nil, err
		}
		targets = append(targets, sharedTarget)
	}

	if buildStatic {
		staticTarget := filepath.Join(g.libDir(), mod.Name+".a")
		if err := g.nw.Build(ninja.BuildEdge{
			Outputs: []string{staticTarget},
			Rule:    "archive_static",
			Inputs:  objs,
		}); err != nil {
			return nil, err
		}
		targets = append(targets, staticTarget)
	}

	return targets, nil
}

func (g *Generator) generateCcTest(mod *eval.EvaluatedModule) ([]string, error) {
	objs, err := g.compileCcSources(mod)
	if err != nil || len(objs) == 0 {
		return nil, err
	}

	target := filepath.Join(g.opts.OutDir, "tests", mod.Name)
	ldflags, libs := g.resolveLinkerArgs(mod)
	libs = append(libs, "-lgtest", "-lgtest_main")

	vars := map[string]string{}
	if len(ldflags) > 0 {
		vars["ldflags"] = strings.Join(ldflags, " ")
	}
	vars["libs"] = strings.Join(libs, " ")

	err = g.nw.Build(ninja.BuildEdge{
		Outputs:   []string{target},
		Rule:      "link_binary",
		Inputs:    objs,
		Variables: vars,
	})
	return []string{target}, err
}

func (g *Generator) compileCcSources(mod *eval.EvaluatedModule) ([]string, error) {
	srcs := g.ResolveSrcs(mod)
	if len(srcs) == 0 {
		return nil, nil
	}

	includes := dedup(g.resolveIncludeDirs(mod))
	cflags := dedup(mod.GetStringList("cflags"))
	cppflags := dedup(mod.GetStringList("cppflags"))
	conlyflags := dedup(mod.GetStringList("conlyflags"))

	// Order-only dependencies for generated headers
	var orderOnlyDeps []string
	genHeaders := append(mod.GetStringList("generated_headers"), mod.GetStringList("export_generated_headers")...)
	for _, gh := range genHeaders {
		if outs, ok := g.moduleOutputs[gh]; ok {
			orderOnlyDeps = append(orderOnlyDeps, outs...)
		} else if ghMod, ok := g.moduleMap[gh]; ok && ghMod.Type == "genrule" {
			for _, out := range ghMod.GetStringList("out") {
				orderOnlyDeps = append(orderOnlyDeps, filepath.Join(g.opts.OutDir, "gen", gh, out))
			}
		}
	}
	orderOnlyDeps = dedup(orderOnlyDeps)

	objDir := g.objDir(mod.Name)
	var objs []string

	for _, src := range srcs {
		ext := filepath.Ext(src)
		rule := "cc_compile"
		flags := append([]string{}, cflags...)

		switch ext {
		case ".cpp", ".cc", ".cxx":
			rule = "cxx_compile"
			flags = append(flags, cppflags...)
		case ".c":
			rule = "cc_compile"
			flags = append(flags, conlyflags...)
		case ".s", ".S":
			rule = "cc_compile"
		default:
			// Not a C/C++ source (e.g. proto, aidl, etc.)
			continue
		}
		flags = dedup(flags)

		baseName := strings.TrimSuffix(src, ext)
		objFile := filepath.Join(objDir, baseName+".o")
		objs = append(objs, objFile)

		vars := map[string]string{}
		if len(flags) > 0 {
			vars["cflags"] = strings.Join(flags, " ")
		}
		if len(includes) > 0 {
			vars["includes"] = strings.Join(includes, " ")
		}

		if err := g.nw.Build(ninja.BuildEdge{
			Outputs:   []string{objFile},
			Rule:      rule,
			Inputs:    []string{src},
			OrderOnly: orderOnlyDeps,
			Variables: vars,
		}); err != nil {
			return nil, err
		}
	}

	return objs, nil
}

func (g *Generator) resolveIncludeDirs(mod *eval.EvaluatedModule) []string {
	var incs []string

	for _, dir := range mod.GetStringList("local_include_dirs") {
		incs = append(incs, "-I"+dir)
	}
	for _, dir := range mod.GetStringList("export_include_dirs") {
		incs = append(incs, "-I"+dir)
	}
	for _, dir := range mod.GetStringList("include_dirs") {
		incs = append(incs, "-I"+filepath.Join(g.opts.TopDir, dir))
	}

	// Generated headers directories
	genHeaders := append(mod.GetStringList("generated_headers"), mod.GetStringList("export_generated_headers")...)
	for _, gh := range genHeaders {
		incs = append(incs, "-I"+filepath.Join(g.opts.OutDir, "gen", gh))
	}

	if g.opts.SysrootDir != "" && !strings.Contains(g.opts.ClangPath, "-clang") {
		incs = append(incs, "-isystem "+filepath.Join(g.opts.SysrootDir, "usr", "include"))
	}
	return dedup(incs)
}

func dedup(items []string) []string {
	seen := make(map[string]bool)
	var res []string
	for _, item := range items {
		if !seen[item] {
			seen[item] = true
			res = append(res, item)
		}
	}
	return res
}

// resolveLinkerArgs implements the dependency-check bypass:
// shared_libs and static_libs are mapped directly to -l flags and prebuilt paths.
func (g *Generator) resolveLinkerArgs(mod *eval.EvaluatedModule) (ldflags []string, libs []string) {
	ldflags = mod.GetStringList("ldflags")

	if g.opts.PrebuiltLibDir != "" {
		ldflags = append(ldflags, "-L"+g.opts.PrebuiltLibDir)
	} else {
		ldflags = append(ldflags, "-L"+g.libDir())
	}

	// Shared library dependencies mapped directly without source verification
	for _, lib := range mod.GetStringList("shared_libs") {
		clean := strings.TrimPrefix(lib, "lib")
		libs = append(libs, "-l"+clean)
	}

	// Static library dependencies
	for _, lib := range mod.GetStringList("static_libs") {
		clean := strings.TrimPrefix(lib, "lib")
		libs = append(libs, "-l"+clean)
	}

	// Whole static libraries
	for _, lib := range mod.GetStringList("whole_static_libs") {
		clean := strings.TrimPrefix(lib, "lib")
		libs = append(libs, "-Wl,--whole-archive", "-l"+clean, "-Wl,--no-whole-archive")
	}

	return ldflags, libs
}
