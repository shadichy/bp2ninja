package generator

import (
	"os"
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
	buildShared := mod.Type == "cc_library" || mod.Type == "cc_library_shared" ||
		mod.Type == "cc_library_host" || mod.Type == "cc_library_host_shared"
	buildStatic := mod.Type == "cc_library" || mod.Type == "cc_library_static" ||
		mod.Type == "cc_library_host" || mod.Type == "cc_library_host_static"

	ldflags, libs := g.resolveLinkerArgs(mod)
	vars := map[string]string{}
	if len(ldflags) > 0 {
		vars["ldflags"] = strings.Join(ldflags, " ")
	}
	if len(libs) > 0 {
		vars["libs"] = strings.Join(libs, " ")
	}

	libBaseName := mod.Name
	if !strings.HasPrefix(libBaseName, "lib") {
		libBaseName = "lib" + libBaseName
	}

	if buildShared {
		sharedTarget := filepath.Join(g.libDir(), libBaseName+".so")
		if err := g.nw.Build(ninja.BuildEdge{
			Outputs:   []string{sharedTarget},
			Rule:      "link_shared",
			Inputs:    objs,
			Variables: vars,
		}); err != nil {
			return nil, err
		}
		targets = append(targets, sharedTarget)

		if libBaseName != mod.Name {
			aliasTarget := filepath.Join(g.libDir(), mod.Name+".so")
			if err := g.nw.Build(ninja.BuildEdge{
				Outputs: []string{aliasTarget},
				Rule:    "copy",
				Inputs:  []string{sharedTarget},
			}); err != nil {
				return nil, err
			}
			targets = append(targets, aliasTarget)
		}
	}

	if buildStatic {
		staticTarget := filepath.Join(g.libDir(), libBaseName+".a")
		if err := g.nw.Build(ninja.BuildEdge{
			Outputs: []string{staticTarget},
			Rule:    "archive_static",
			Inputs:  objs,
		}); err != nil {
			return nil, err
		}
		targets = append(targets, staticTarget)

		if libBaseName != mod.Name {
			aliasTarget := filepath.Join(g.libDir(), mod.Name+".a")
			if err := g.nw.Build(ninja.BuildEdge{
				Outputs: []string{aliasTarget},
				Rule:    "copy",
				Inputs:  []string{staticTarget},
			}); err != nil {
				return nil, err
			}
			targets = append(targets, aliasTarget)
		}
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
	cflags := append(mod.GetStringList("cflags"), g.opts.ExtraCflags...)
	cppflags := append(mod.GetStringList("cppflags"), g.opts.ExtraCppflags...)
	conlyflags := dedup(mod.GetStringList("conlyflags"))
	cflags = append(cflags, "-fPIC")
	cflags = dedup(cflags)
	cppflags = dedup(cppflags)

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
		incPath := dir
		if mod.Dir != "" && mod.Dir != "." && !filepath.IsAbs(dir) {
			incPath = filepath.Clean(filepath.Join(mod.Dir, dir))
		}
		incs = append(incs, "-I"+incPath)
	}
	for _, dir := range mod.GetStringList("export_include_dirs") {
		incPath := dir
		if mod.Dir != "" && mod.Dir != "." && !filepath.IsAbs(dir) {
			incPath = filepath.Clean(filepath.Join(mod.Dir, dir))
		}
		incs = append(incs, "-I"+incPath)
	}
	for _, dir := range mod.GetStringList("include_dirs") {
		incPath := dir
		if g.opts.TopDir != "" && g.opts.TopDir != "." && !filepath.IsAbs(g.opts.TopDir) {
			incPath = filepath.Join(g.opts.TopDir, dir)
		}
		incs = append(incs, "-I"+incPath)
	}

	// Pull include dirs from referenced header_libs in moduleMap
	for _, hl := range mod.GetStringList("header_libs") {
		if hlMod, ok := g.moduleMap[hl]; ok {
			for _, dir := range hlMod.GetStringList("export_include_dirs") {
				incPath := dir
				if hlMod.Dir != "" && hlMod.Dir != "." && !filepath.IsAbs(dir) {
					incPath = filepath.Clean(filepath.Join(hlMod.Dir, dir))
				}
				incs = append(incs, "-I"+incPath)
			}
			for _, dir := range hlMod.GetStringList("local_include_dirs") {
				incPath := dir
				if hlMod.Dir != "" && hlMod.Dir != "." && !filepath.IsAbs(dir) {
					incPath = filepath.Clean(filepath.Join(hlMod.Dir, dir))
				}
				incs = append(incs, "-I"+incPath)
			}
		}
	}

	// Pull include dirs from referenced shared_libs and static_libs in moduleMap
	for _, lib := range append(mod.GetStringList("shared_libs"), mod.GetStringList("static_libs")...) {
		if libMod, ok := g.moduleMap[lib]; ok {
			for _, dir := range libMod.GetStringList("export_include_dirs") {
				incPath := dir
				if libMod.Dir != "" && libMod.Dir != "." && !filepath.IsAbs(dir) {
					incPath = filepath.Clean(filepath.Join(libMod.Dir, dir))
				}
				incs = append(incs, "-I"+incPath)
			}
		}
	}

	// Generated headers directories
	genHeaders := append(mod.GetStringList("generated_headers"), mod.GetStringList("export_generated_headers")...)
	for _, gh := range genHeaders {
		incs = append(incs, "-I"+filepath.Join(g.opts.OutDir, "gen", gh))
	}

	// Fallback to local ./include if present
	localInc := "include"
	if mod.Dir != "" && mod.Dir != "." {
		localInc = filepath.Join(mod.Dir, "include")
	}
	if fi, err := os.Stat(filepath.Join(g.opts.BpDir, localInc)); err == nil && fi.IsDir() {
		incs = append(incs, "-I"+localInc)
	}

	// Auto-detect include dirs for referenced external dependencies from TopDir (ANDROID_BUILD_TOP)
	topModules := g.GetTopDirModules()
	var neededDeps []string
	for _, hl := range mod.GetStringList("header_libs") {
		if _, ok := g.moduleMap[hl]; !ok {
			neededDeps = append(neededDeps, hl, strings.TrimSuffix(hl, "_headers"))
		}
	}
	for _, sl := range mod.GetStringList("shared_libs") {
		if _, ok := g.moduleMap[sl]; !ok {
			neededDeps = append(neededDeps, sl, strings.TrimPrefix(sl, "lib"))
		}
	}
	for _, stl := range append(mod.GetStringList("static_libs"), mod.GetStringList("whole_static_libs")...) {
		if _, ok := g.moduleMap[stl]; !ok {
			neededDeps = append(neededDeps, stl, strings.TrimPrefix(stl, "lib"))
		}
	}

	for _, dep := range neededDeps {
		if dir, ok := topModules[dep]; ok {
			incDir := filepath.Join(dir, "include")
			if fi, err := os.Stat(incDir); err == nil && fi.IsDir() {
				incs = append(incs, "-I"+incDir)
			}
			incVndk := filepath.Join(dir, "include_vndk")
			if fi, err := os.Stat(incVndk); err == nil && fi.IsDir() {
				incs = append(incs, "-I"+incVndk)
			}
		}
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
	ldflags = append(mod.GetStringList("ldflags"), g.opts.ExtraLdflags...)

	if g.opts.PrebuiltLibDir != "" {
		ldflags = append(ldflags, "-L"+g.opts.PrebuiltLibDir)
	} else {
		ldflags = append(ldflags, "-L"+g.libDir())
	}

	// Auto-detect library paths for dependencies from TopDir (ANDROID_BUILD_TOP)
	topModules := g.GetTopDirModules()
	allLibs := append(mod.GetStringList("shared_libs"), mod.GetStringList("static_libs")...)
	allLibs = append(allLibs, mod.GetStringList("whole_static_libs")...)
	for _, lib := range allLibs {
		candidates := []string{lib, "lib" + lib, strings.TrimPrefix(lib, "lib")}
		for _, cand := range candidates {
			if dir, ok := topModules[cand]; ok {
				for _, sub := range []string{"lib64", "lib", "out/lib64", "out/lib"} {
					lpath := filepath.Join(dir, sub)
					if fi, err := os.Stat(lpath); err == nil && fi.IsDir() {
						ldflags = append(ldflags, "-L"+lpath)
					}
				}
				break
			}
		}
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

	for _, lib := range mod.GetStringList("whole_static_libs") {
		clean := strings.TrimPrefix(lib, "lib")
		libs = append(libs, "-Wl,--whole-archive", "-l"+clean, "-Wl,--no-whole-archive")
	}

	return dedup(ldflags), libs
}
