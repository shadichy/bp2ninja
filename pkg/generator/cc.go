package generator

import (
	"os"
	"path/filepath"
	"strings"

	"bp2ninja/pkg/eval"
	"bp2ninja/pkg/ninja"
)

// shellJoin joins argv for interpolation into a ninja rule Command (run via
// /bin/sh): quote elements containing shell metacharacters. Ninja `$`
// escaping is handled by the writer; here we only protect sh syntax.
func shellJoin(args []string) string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if strings.ContainsAny(a, " \t\n\"'><()$`&|;*?[]{}~#") {
			out = append(out, "'"+strings.ReplaceAll(a, "'", "'\\''")+"'")
		} else {
			out = append(out, a)
		}
	}
	return strings.Join(out, " ")
}

func (g *Generator) generateCcBinary(mod *eval.EvaluatedModule) ([]string, error) {
	objs, err := g.compileCcSources(mod)
	if err != nil || len(objs) == 0 {
		return nil, err
	}

	target := filepath.Join(g.binDir(), mod.Name)
	ldflags, libs := g.resolveLinkerArgs(mod)

	vars := map[string]string{}
	if len(ldflags) > 0 {
		vars["ldflags"] = shellJoin(ldflags)
	}
	if len(libs) > 0 {
		vars["libs"] = shellJoin(libs)
	}

	implicits := g.collectLinkImplicits(mod)

	err = g.nw.Build(ninja.BuildEdge{
		Outputs:   []string{target},
		Rule:      "link_binary",
		Inputs:    objs,
		Implicits: implicits,
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

	sharedMod := mod.WithVariant("shared")
	staticMod := mod.WithVariant("static")

	buildShared := (mod.Type == "cc_library" || mod.Type == "cc_library_shared" ||
		mod.Type == "cc_library_host" || mod.Type == "cc_library_host_shared") && sharedMod.IsEnabled()
	buildStatic := (mod.Type == "cc_library" || mod.Type == "cc_library_static" ||
		mod.Type == "cc_library_host" || mod.Type == "cc_library_host_static") && staticMod.IsEnabled()

	if !buildShared && !buildStatic {
		return nil, nil
	}

	sharedSrcs := g.ResolveSrcs(sharedMod)
	staticSrcs := g.ResolveSrcs(staticMod)

	sameSrcs := slicesEqual(sharedSrcs, staticSrcs)
	sameCflags := slicesEqual(sharedMod.GetStringList("cflags"), staticMod.GetStringList("cflags"))

	var sharedObjs []string
	var staticObjs []string

	if buildShared && buildStatic && sameSrcs && sameCflags {
		// Sources and cflags are identical: compile once to out/obj/<mod.Name>/
		objs, err := g.compileCcSourcesWithSuffix(sharedMod, "")
		if err != nil {
			return nil, err
		}
		sharedObjs = objs
		staticObjs = objs
	} else {
		// Differentiated sources or flags (e.g. bionic libc) or single-variant:
		if buildShared {
			suffix := ""
			if buildStatic && (!sameSrcs || !sameCflags) {
				suffix = ".shared"
			}
			objs, err := g.compileCcSourcesWithSuffix(sharedMod, suffix)
			if err != nil {
				return nil, err
			}
			sharedObjs = objs
		}
		if buildStatic {
			suffix := ""
			if buildShared && (!sameSrcs || !sameCflags) {
				suffix = ".static"
			}
			objs, err := g.compileCcSourcesWithSuffix(staticMod, suffix)
			if err != nil {
				return nil, err
			}
			staticObjs = objs
		}
	}

	var targets []string

	libBaseName := mod.Name
	if !strings.HasPrefix(libBaseName, "lib") {
		libBaseName = "lib" + libBaseName
	}

	if buildShared {
		sharedTarget := filepath.Join(g.libDir(), libBaseName+".so")
		if len(sharedObjs) > 0 {
			ldflags, libs := g.resolveLinkerArgs(sharedMod)
			vars := map[string]string{}
			if len(ldflags) > 0 {
				vars["ldflags"] = shellJoin(ldflags)
			}
			if len(libs) > 0 {
				vars["libs"] = shellJoin(libs)
			}

			implicits := g.collectLinkImplicits(sharedMod)

			if err := g.nw.Build(ninja.BuildEdge{
				Outputs:   []string{sharedTarget},
				Rule:      "link_shared",
				Inputs:    sharedObjs,
				Implicits: implicits,
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
		} else if g.opts.AllowMissingDeps {
			g.emitPhonyIfNeeded(sharedTarget)
			targets = append(targets, sharedTarget)
			if libBaseName != mod.Name {
				aliasTarget := filepath.Join(g.libDir(), mod.Name+".so")
				g.emitPhonyIfNeeded(aliasTarget)
				targets = append(targets, aliasTarget)
			}
		}
	}

	if buildStatic {
		staticTarget := filepath.Join(g.libDir(), libBaseName+".a")
		if len(staticObjs) > 0 {
			implicits := g.collectLinkImplicits(staticMod)

			if err := g.nw.Build(ninja.BuildEdge{
				Outputs:   []string{staticTarget},
				Rule:      "archive_static",
				Inputs:    staticObjs,
				Implicits: implicits,
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
		} else if g.opts.AllowMissingDeps {
			g.emitPhonyIfNeeded(staticTarget)
			targets = append(targets, staticTarget)
			if libBaseName != mod.Name {
				aliasTarget := filepath.Join(g.libDir(), mod.Name+".a")
				g.emitPhonyIfNeeded(aliasTarget)
				targets = append(targets, aliasTarget)
			}
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
		vars["ldflags"] = shellJoin(ldflags)
	}
	vars["libs"] = shellJoin(libs)

	implicits := g.collectLinkImplicits(mod)

	err = g.nw.Build(ninja.BuildEdge{
		Outputs:   []string{target},
		Rule:      "link_binary",
		Inputs:    objs,
		Implicits: implicits,
		Variables: vars,
	})
	return []string{target}, err
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (g *Generator) compileCcSources(mod *eval.EvaluatedModule) ([]string, error) {
	return g.compileCcSourcesWithSuffix(mod, "")
}

func (g *Generator) compileCcSourcesWithSuffix(mod *eval.EvaluatedModule, suffix string) ([]string, error) {
	srcs := g.ResolveSrcs(mod)
	if len(srcs) == 0 {
		return nil, nil
	}

	includes := dedup(g.resolveIncludeDirs(mod))
	cflags := append(mod.GetStringList("cflags"), g.opts.ExtraCflags...)
	cppflags := append(mod.GetStringList("cppflags"), g.opts.ExtraCppflags...)
	conlyflags := dedup(mod.GetStringList("conlyflags"))
	cflags = append(cflags, "-fPIC")

	// Apply C / C++ standard properties (c_std, cpp_std)
	if cStd := mod.GetString("c_std"); cStd != "" {
		conlyflags = append(conlyflags, "-std="+cStd)
	}
	if cppStd := mod.GetString("cpp_std"); cppStd != "" {
		if cppStd == "experimental" {
			cppflags = append(cppflags, "-std=gnu++20")
		} else {
			cppflags = append(cppflags, "-std="+cppStd)
		}
	}

	cflags = dedup(cflags)
	cppflags = dedup(cppflags)
	conlyflags = dedup(conlyflags)

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

	objDir := g.objDir(mod.Name + suffix)
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
		case ".asm":
			rule = "asm_nasm"
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
			vars["cflags"] = shellJoin(flags)
		}
		if rule == "asm_nasm" {
			af := append([]string{}, mod.GetStringList("asflags")...)
			af = append(af, "-I"+filepath.Dir(src))
			for _, inc := range includes {
				if len(inc) > 2 && inc[:2] == "-I" {
					af = append(af, inc)
				} else {
					af = append(af, "-I"+inc)
				}
			}
			if len(af) > 0 {
				vars["asflags"] = shellJoin(af)
			}
		}
		if len(includes) > 0 {
			vars["includes"] = shellJoin(includes)
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

	// Always include the module root directory itself (Soong default for #include <local_header.h>)
	modDir := "."
	if mod.Dir != "" && mod.Dir != "." {
		modDir = mod.Dir
	}
	incs = append(incs, "-I"+modDir)

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

	// Pull include dirs from referenced header_libs in moduleMap (including transitive export_header_lib_headers)
	visitedHl := make(map[string]bool)
	var queueHl []string
	queueHl = append(queueHl, mod.GetAllStringList("header_libs")...)
	for len(queueHl) > 0 {
		hl := queueHl[0]
		queueHl = queueHl[1:]
		if visitedHl[hl] {
			continue
		}
		visitedHl[hl] = true
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
			queueHl = append(queueHl, hlMod.GetStringList("export_header_lib_headers")...)
		}
	}

	// Pull include dirs from referenced shared_libs and static_libs in moduleMap
	allLibs := append(mod.GetAllStringList("shared_libs"), mod.GetAllStringList("static_libs")...)
	allLibs = append(allLibs, mod.GetAllStringList("whole_static_libs")...)
	for _, lib := range allLibs {
		if libMod, ok := g.moduleMap[lib]; ok {
			for _, dir := range libMod.GetStringList("export_include_dirs") {
				incPath := dir
				if libMod.Dir != "" && libMod.Dir != "." && !filepath.IsAbs(dir) {
					incPath = filepath.Clean(filepath.Join(libMod.Dir, dir))
				}
				incs = append(incs, "-I"+incPath)
			}
			for _, hl := range libMod.GetStringList("export_header_lib_headers") {
				if !visitedHl[hl] {
					queueHl = append(queueHl, hl)
				}
			}
		}
	}

	// Process any transitive header_libs enqueued from shared/static libs
	for len(queueHl) > 0 {
		hl := queueHl[0]
		queueHl = queueHl[1:]
		if visitedHl[hl] {
			continue
		}
		visitedHl[hl] = true
		if hlMod, ok := g.moduleMap[hl]; ok {
			for _, dir := range hlMod.GetStringList("export_include_dirs") {
				incPath := dir
				if hlMod.Dir != "" && hlMod.Dir != "." && !filepath.IsAbs(dir) {
					incPath = filepath.Clean(filepath.Join(hlMod.Dir, dir))
				}
				incs = append(incs, "-I"+incPath)
			}
			queueHl = append(queueHl, hlMod.GetStringList("export_header_lib_headers")...)
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
	for _, hl := range mod.GetAllStringList("header_libs") {
		if _, ok := g.moduleMap[hl]; !ok {
			neededDeps = append(neededDeps, hl, strings.TrimSuffix(hl, "_headers"))
		}
	}
	for _, sl := range mod.GetAllStringList("shared_libs") {
		if _, ok := g.moduleMap[sl]; !ok {
			neededDeps = append(neededDeps, sl, strings.TrimPrefix(sl, "lib"))
		}
	}
	for _, stl := range append(mod.GetAllStringList("static_libs"), mod.GetAllStringList("whole_static_libs")...) {
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

var compoundFlags = map[string]bool{
	"-isystem":           true,
	"-idirafter":         true,
	"-iquote":            true,
	"-include":           true,
	"-iprefix":           true,
	"-iwithprefix":       true,
	"-iwithprefixbefore": true,
	"-imacros":           true,
	"-MF":                true,
	"-MQ":                true,
	"-MT":                true,
	"-target":            true,
	"--target":           true,
	"--sysroot":          true,
	"-sysroot":           true,
	"-Xclang":            true,
	"-Xlinker":           true,
	"-Wl,-rpath":         true,
	"-install_name":      true,
}

func dedup(items []string) []string {
	seen := make(map[string]bool)
	var res []string
	for i := 0; i < len(items); i++ {
		item := items[i]
		if compoundFlags[item] && i+1 < len(items) {
			pair := item + " " + items[i+1]
			if !seen[pair] {
				seen[pair] = true
				res = append(res, item, items[i+1])
			}
			i++
		} else {
			if !seen[item] {
				seen[item] = true
				res = append(res, item)
			}
		}
	}
	return res
}

func (g *Generator) getModuleTargetOutputs(lib string, preferExt string) []string {
	if outs, ok := g.moduleOutputs[lib]; ok && len(outs) > 0 {
		if preferExt == ".so" || preferExt == ".a" {
			var matched []string
			for _, out := range outs {
				if strings.HasSuffix(out, preferExt) {
					matched = append(matched, out)
				}
			}
			if len(matched) > 0 {
				return matched
			}
		}
		return outs
	}

	// Predict target output from moduleMap if dependency module is not yet emitted
	if libMod, ok := g.moduleMap[lib]; ok {
		baseName := libMod.Name
		if !strings.HasPrefix(baseName, "lib") {
			baseName = "lib" + baseName
		}
		switch libMod.Type {
		case "cc_library", "cc_library_host":
			if preferExt == ".a" {
				return []string{filepath.Join(g.libDir(), baseName+".a")}
			}
			return []string{filepath.Join(g.libDir(), baseName+".so")}
		case "cc_library_shared", "cc_library_host_shared":
			return []string{filepath.Join(g.libDir(), baseName+".so")}
		case "cc_library_static", "cc_library_host_static":
			return []string{filepath.Join(g.libDir(), baseName+".a")}
		case "cc_library_headers":
			return []string{filepath.Join(g.opts.OutDir, "headers", libMod.Name)}
		case "genrule":
			var genOuts []string
			for _, out := range libMod.GetStringList("out") {
				genOuts = append(genOuts, filepath.Join(g.opts.OutDir, "gen", libMod.Name, out))
			}
			return genOuts
		}
	}
	return nil
}

// collectLinkImplicits returns ninja implicit deps for in-tree library modules.
// shared_libs match .so outputs, static_libs & whole_static_libs match .a outputs.
func (g *Generator) collectLinkImplicits(mod *eval.EvaluatedModule) []string {
	var implicits []string

	// Shared library dependencies -> match .so
	for _, lib := range mod.GetStringList("shared_libs") {
		if outs := g.getModuleTargetOutputs(lib, ".so"); len(outs) > 0 {
			implicits = append(implicits, outs...)
		}
	}

	// Static & whole_static dependencies -> match .a
	for _, lib := range append(mod.GetStringList("static_libs"), mod.GetStringList("whole_static_libs")...) {
		if outs := g.getModuleTargetOutputs(lib, ".a"); len(outs) > 0 {
			implicits = append(implicits, outs...)
		}
	}

	// Header libraries -> phony / gen targets
	for _, hl := range mod.GetStringList("header_libs") {
		if outs := g.getModuleTargetOutputs(hl, ""); len(outs) > 0 {
			implicits = append(implicits, outs...)
		}
	}

	return dedup(implicits)
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
				subDirs := []string{"lib64", "out/lib64"}
				if !strings.Contains(g.opts.TargetArch, "64") {
					subDirs = []string{"lib", "out/lib"}
				}
				for _, sub := range subDirs {
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
		if clean == "sqlite" {
			clean = "sqlite3"
		}
		libs = append(libs, "-l"+clean)
	}

	// Static library dependencies
	for _, lib := range mod.GetStringList("static_libs") {
		clean := strings.TrimPrefix(lib, "lib")
		if clean == "sqlite" {
			clean = "sqlite3"
		}
		libs = append(libs, "-l"+clean)
	}

	for _, lib := range mod.GetStringList("whole_static_libs") {
		clean := strings.TrimPrefix(lib, "lib")
		if clean == "sqlite" {
			clean = "sqlite3"
		}
		libs = append(libs, "-Wl,--whole-archive", "-l"+clean, "-Wl,--no-whole-archive")
	}

	return dedup(ldflags), libs
}
