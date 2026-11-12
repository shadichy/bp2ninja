package generator

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"bp2ninja/pkg/eval"
	"bp2ninja/pkg/ninja"
)

func extractAidlPackage(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "package ") && strings.HasSuffix(line, ";") {
			pkg := strings.TrimPrefix(line, "package ")
			pkg = strings.TrimSuffix(pkg, ";")
			return strings.TrimSpace(pkg)
		}
	}
	return ""
}

// generateAidlInterface implements the upstream `aidl_interface` module type (system/tools/aidl/build/aidl_interface.go):
// translates .aidl interface definitions into Java, C++, NDK, and Rust bindings, registering companion
// libraries and versioned target aliases.
func (g *Generator) generateAidlInterface(mod *eval.EvaluatedModule) ([]string, error) {
	srcs := g.ResolveSrcs(mod)
	var aidlFiles []string
	for _, s := range srcs {
		if strings.HasSuffix(s, ".aidl") {
			aidlFiles = append(aidlFiles, s)
		}
	}
	if len(aidlFiles) == 0 {
		if g.opts.AllowMissingDeps {
			target := filepath.Join(g.opts.OutDir, "gen", mod.Name, mod.Name+".stamp")
			g.emitPhonyIfNeeded(target)
			return []string{target}, nil
		}
		return nil, nil
	}

	// 1. Resolve AIDL import search paths
	localIncDir := mod.GetString("local_include_dir")
	if localIncDir == "" {
		localIncDir = mod.Dir
	} else if !filepath.IsAbs(localIncDir) && mod.Dir != "" && mod.Dir != "." {
		localIncDir = filepath.Clean(filepath.Join(mod.Dir, localIncDir))
	}
	if localIncDir == "" {
		localIncDir = "."
	}

	var importDirs []string
	importDirs = append(importDirs, localIncDir)

	for _, src := range mod.GetStringList("srcs") {
		if strings.HasPrefix(src, ":") {
			fgName := strings.TrimPrefix(src, ":")
			if fgMod, ok := g.moduleMap[fgName]; ok {
				if p := fgMod.GetString("path"); p != "" {
					dir := fgMod.Dir
					if dir == "" {
						dir = "."
					}
					importDirs = append(importDirs, filepath.Clean(filepath.Join(dir, p)))
				}
			}
		}
	}

	for _, src := range aidlFiles {
		absPath := src
		if !filepath.IsAbs(absPath) && g.opts.TopDir != "" {
			absPath = filepath.Join(g.opts.TopDir, src)
		}
		if pkg := extractAidlPackage(absPath); pkg != "" {
			pkgPath := strings.ReplaceAll(pkg, ".", "/")
			expectedSuffix := "/" + pkgPath + "/" + filepath.Base(src)
			if idx := strings.Index(src, expectedSuffix); idx != -1 {
				baseDir := src[:idx]
				if baseDir != "" {
					importDirs = append(importDirs, baseDir)
				}
			}
		}
	}

	for _, incDir := range mod.GetStringList("include_dirs") {
		if incDir != "" {
			if !filepath.IsAbs(incDir) && g.opts.TopDir != "" && g.opts.TopDir != "." {
				importDirs = append(importDirs, filepath.Clean(filepath.Join(g.opts.TopDir, incDir)))
			} else {
				importDirs = append(importDirs, incDir)
			}
		}
	}

	imports := mod.GetStringList("imports")
	for _, imp := range imports {
		baseName := imp
		if idx := strings.Index(imp, "-V"); idx != -1 {
			baseName = imp[:idx]
		}
		for _, name := range []string{imp, baseName} {
			if impMod, ok := g.moduleMap[name]; ok {
				dir := impMod.Dir
				if dir == "" {
					dir = "."
				}
				incDir := impMod.GetString("local_include_dir")
				if incDir != "" {
					if !filepath.IsAbs(incDir) && dir != "." {
						importDirs = append(importDirs, filepath.Clean(filepath.Join(dir, incDir)))
					} else {
						importDirs = append(importDirs, incDir)
					}
				}
				importDirs = append(importDirs, dir)
			}
		}
		if libInfo, ok := g.aidlLibraries[imp]; ok {
			importDirs = append(importDirs, libInfo.IncludeDirs...)
		}
		if libInfo, ok := g.aidlLibraries[baseName]; ok {
			importDirs = append(importDirs, libInfo.IncludeDirs...)
		}
	}
	importDirs = dedup(importDirs)

	var importFlags []string
	for _, dir := range importDirs {
		importFlags = append(importFlags, "-I"+dir)
	}

	// 2. Resolve optional flags & stability (all aidl_interface modules are structured)
	var optFlags []string
	optFlags = append(optFlags, "--structured")
	if stability := mod.GetString("stability"); stability != "" {
		optFlags = append(optFlags, "--stability", stability)
	}
	optFlags = append(optFlags, mod.GetStringList("flags")...)

	// 2b. Extract version and frozen hash from versions_with_info or versions per AOSP aidl_interface spec
	var latestVersion string
	if vInfo, ok := mod.Properties["versions_with_info"].([]interface{}); ok && len(vInfo) > 0 {
		for _, item := range vInfo {
			if m, ok := item.(map[string]interface{}); ok {
				if v, ok := m["version"].(string); ok && v != "" {
					latestVersion = v
				}
			}
		}
	}
	if latestVersion == "" {
		if vers := mod.GetStringList("versions"); len(vers) > 0 {
			latestVersion = vers[len(vers)-1]
		}
	}
	if latestVersion != "" {
		latestHash := "notfrozen"
		dir := mod.Dir
		if dir == "" {
			dir = "."
		}
		hashFile := filepath.Join(g.opts.TopDir, dir, "aidl_api", mod.Name, latestVersion, ".hash")
		if data, err := os.ReadFile(hashFile); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if trimmed := strings.TrimSpace(line); trimmed != "" {
					latestHash = trimmed
				}
			}
		}
		optFlags = append(optFlags, "--version", latestVersion, "--hash", latestHash)
	}

	// 3. Inspect enabled backends
	backendMap := mod.GetMap("backend")
	cppEnabled := true
	ndkEnabled := true
	javaEnabled := true
	rustEnabled := false

	if backendMap != nil {
		if cpp, ok := backendMap["cpp"].(map[string]interface{}); ok {
			if en, ok := cpp["enabled"].(bool); ok {
				cppEnabled = en
			}
		}
		if ndk, ok := backendMap["ndk"].(map[string]interface{}); ok {
			if en, ok := ndk["enabled"].(bool); ok {
				ndkEnabled = en
			}
		}
		if java, ok := backendMap["java"].(map[string]interface{}); ok {
			if en, ok := java["enabled"].(bool); ok {
				javaEnabled = en
			}
		}
		if rust, ok := backendMap["rust"].(map[string]interface{}); ok {
			if en, ok := rust["enabled"].(bool); ok {
				rustEnabled = en
			}
		}
	}

	var allTargets []string
	var cppTargets []string
	var ndkTargets []string
	var javaTargets []string
	var rustTargets []string

	cflags := append(mod.GetStringList("cflags"), g.opts.ExtraCflags...)
	cflags = stripIncludeFlags(cflags)
	cflags = append(cflags, g.archCodegenFlags()...)
	cflags = append(cflags, g.crossTargetFlags()...)
	cflags = append(cflags, "-fPIC")
	cflags = dedup(cflags)

	ldflags, _ := g.resolveLinkerArgs(mod)

	// 4. Emit C++ Backend
	if cppEnabled {
		cppModName := mod.Name + "-cpp"
		cleanCppName := strings.TrimPrefix(cppModName, "lib")
		genDir := filepath.Join(g.opts.OutDir, "gen", mod.Name, "cpp")
		hdrDir := filepath.Join(genDir, "include")

		vars := map[string]string{
			"header_dir": hdrDir,
			"out_dir":    genDir,
			"imports":    shellJoin(importFlags),
		}
		if len(optFlags) > 0 {
			vars["optional_flags"] = shellJoin(optFlags)
		}

		var genCpps []string
		var objs []string
		objDir := g.objDir(cppModName)
		for _, f := range aidlFiles {
			rel := strings.TrimSuffix(f, ".aidl")
			genCpps = append(genCpps, filepath.Join(genDir, rel+".cpp"))
			objs = append(objs, filepath.Join(objDir, filepath.Base(rel)+".o"))
		}
		if len(genCpps) == 0 {
			genCpps = []string{filepath.Join(genDir, mod.Name+".cpp")}
			objs = []string{filepath.Join(objDir, mod.Name+".o")}
		}

		if err := g.nw.Build(ninja.BuildEdge{
			Outputs:   genCpps,
			Rule:      "aidl_cpp",
			Inputs:    aidlFiles,
			Variables: vars,
		}); err != nil {
			return nil, err
		}

		// Compile generated C++
		includes := dedup(append(g.resolveIncludeDirs(mod), "-I"+hdrDir, "-I"+genDir))
		compileVars := map[string]string{
			"includes": shellJoin(includes),
		}
		if len(cflags) > 0 {
			compileVars["cflags"] = shellJoin(cflags)
		}

		for i, cpp := range genCpps {
			if err := g.nw.Build(ninja.BuildEdge{
				Outputs:   []string{objs[i]},
				Rule:      "cxx_compile",
				Inputs:    []string{cpp},
				Variables: compileVars,
			}); err != nil {
				return nil, err
			}
		}

		staticTarget := filepath.Join(g.libDir(), "lib"+cleanCppName+".a")
		sharedTarget := filepath.Join(g.libDir(), "lib"+cleanCppName+".so")

		_ = g.nw.Build(ninja.BuildEdge{
			Outputs: []string{staticTarget},
			Rule:    "archive_static",
			Inputs:  objs,
		})

		cppLibs := []string{"-lbinder", "-lutils"}
		_ = g.nw.Build(ninja.BuildEdge{
			Outputs: []string{sharedTarget},
			Rule:    "link_shared",
			Inputs:  objs,
			Variables: map[string]string{
				"ldflags": shellJoin(ldflags),
				"libs":    shellJoin(cppLibs),
				"soname":  "lib" + cleanCppName + ".so",
			},
		})

		cppTargets = []string{sharedTarget, staticTarget}
		g.moduleOutputs[cppModName] = cppTargets
		_ = g.nw.Build(ninja.BuildEdge{
			Outputs: []string{cppModName},
			Rule:    "phony",
			Inputs:  cppTargets,
		})
		allTargets = append(allTargets, cppTargets...)
	}

	// 5. Emit NDK Backend
	if ndkEnabled {
		ndkModName := mod.Name + "-ndk"
		cleanNdkName := strings.TrimPrefix(ndkModName, "lib")
		genDir := filepath.Join(g.opts.OutDir, "gen", mod.Name, "ndk")
		hdrDir := filepath.Join(genDir, "include")

		vars := map[string]string{
			"header_dir": hdrDir,
			"out_dir":    genDir,
			"imports":    shellJoin(importFlags),
		}
		if len(optFlags) > 0 {
			vars["optional_flags"] = shellJoin(optFlags)
		}

		var genCpps []string
		var objs []string
		objDir := g.objDir(ndkModName)
		for _, f := range aidlFiles {
			rel := strings.TrimSuffix(f, ".aidl")
			genCpps = append(genCpps, filepath.Join(genDir, rel+".cpp"))
			objs = append(objs, filepath.Join(objDir, filepath.Base(rel)+".o"))
		}
		if len(genCpps) == 0 {
			genCpps = []string{filepath.Join(genDir, mod.Name+".cpp")}
			objs = []string{filepath.Join(objDir, mod.Name+".o")}
		}

		if err := g.nw.Build(ninja.BuildEdge{
			Outputs:   genCpps,
			Rule:      "aidl_ndk",
			Inputs:    aidlFiles,
			Variables: vars,
		}); err != nil {
			return nil, err
		}

		// Compile generated NDK C++
		includes := dedup(append(g.resolveIncludeDirs(mod), "-I"+hdrDir, "-I"+genDir))
		compileVars := map[string]string{
			"includes": shellJoin(includes),
		}
		if len(cflags) > 0 {
			compileVars["cflags"] = shellJoin(cflags)
		}

		for i, cpp := range genCpps {
			if err := g.nw.Build(ninja.BuildEdge{
				Outputs:   []string{objs[i]},
				Rule:      "cxx_compile",
				Inputs:    []string{cpp},
				Variables: compileVars,
			}); err != nil {
				return nil, err
			}
		}

		staticTarget := filepath.Join(g.libDir(), "lib"+cleanNdkName+".a")
		sharedTarget := filepath.Join(g.libDir(), "lib"+cleanNdkName+".so")

		_ = g.nw.Build(ninja.BuildEdge{
			Outputs: []string{staticTarget},
			Rule:    "archive_static",
			Inputs:  objs,
		})

		ndkLibs := []string{"-lbinder_ndk"}
		_ = g.nw.Build(ninja.BuildEdge{
			Outputs: []string{sharedTarget},
			Rule:    "link_shared",
			Inputs:  objs,
			Variables: map[string]string{
				"ldflags": shellJoin(ldflags),
				"libs":    shellJoin(ndkLibs),
				"soname":  "lib" + cleanNdkName + ".so",
			},
		})

		ndkTargets = []string{sharedTarget, staticTarget}
		g.moduleOutputs[ndkModName] = ndkTargets
		_ = g.nw.Build(ninja.BuildEdge{
			Outputs: []string{ndkModName},
			Rule:    "phony",
			Inputs:  ndkTargets,
		})
		allTargets = append(allTargets, ndkTargets...)
	}

	// 6. Emit Java Backend
	if javaEnabled {
		javaModName := mod.Name + "-java"
		genDir := filepath.Join(g.opts.OutDir, "gen", mod.Name, "java")
		jarTarget := filepath.Join(g.libDir(), javaModName+".jar")

		var javaImports []string
		var javaDepTargets []string
		for _, imp := range imports {
			baseImp := imp
			if idx := strings.Index(imp, "-V"); idx != -1 {
				baseImp = imp[:idx]
			}
			javaImports = append(javaImports, imp+"-java", baseImp+"-java", imp, baseImp)
			found := false
			for _, k := range []string{imp + "-java", baseImp + "-java"} {
				if outs, ok := g.moduleOutputs[k]; ok {
					javaDepTargets = append(javaDepTargets, outs...)
					found = true
				}
			}
			if !found {
				for _, k := range []string{imp, baseImp} {
					if _, ok := g.moduleMap[k]; ok {
						jar := filepath.Join(g.libDir(), k+"-java.jar")
						javaDepTargets = append(javaDepTargets, jar)
						break
					}
				}
			}
		}
		javaImports = dedup(javaImports)
		javaDepTargets = dedup(javaDepTargets)

		var aidlCp []string
		for _, bcp := range g.opts.BootClasspath {
			if bcp != "" {
				if fi, err := os.Stat(bcp); err == nil && !fi.IsDir() {
					aidlCp = append(aidlCp, bcp)
					javaDepTargets = append(javaDepTargets, bcp)
				}
			}
		}
		for _, ej := range g.opts.ExtraJars {
			if ej != "" {
				if fi, err := os.Stat(ej); err == nil && !fi.IsDir() {
					aidlCp = append(aidlCp, ej)
					javaDepTargets = append(javaDepTargets, ej)
				}
			}
		}
		if g.opts.TopDir != "" {
			targetApi := g.opts.APILevel
			if targetApi <= 0 {
				targetApi = 34
			}
			modLibJar := filepath.Join(g.opts.TopDir, "prebuilts/sdk", fmt.Sprintf("%d", targetApi), "module-lib/android.jar")
			if fi, err := os.Stat(modLibJar); err == nil && !fi.IsDir() {
				aidlCp = append(aidlCp, modLibJar)
				javaDepTargets = append(javaDepTargets, modLibJar)
			}
		}
		if len(aidlCp) == 0 {
			if cj := g.resolveCurrentAndroidJar(); cj != "" {
				aidlCp = append(aidlCp, cj)
				javaDepTargets = append(javaDepTargets, cj)
			}
		}
		aidlCp = append(aidlCp, javaDepTargets...)
		aidlCp = dedup(aidlCp)

		vars := map[string]string{
			"out_dir": genDir,
			"imports": shellJoin(importFlags),
		}
		if len(optFlags) > 0 {
			vars["optional_flags"] = shellJoin(optFlags)
		}
		if len(aidlCp) > 0 {
			vars["classpath"] = "-cp " + strings.Join(aidlCp, ":")
		}

		if err := g.nw.Build(ninja.BuildEdge{
			Outputs:   []string{jarTarget},
			Rule:      "aidl_java",
			Inputs:    aidlFiles,
			Implicits: javaDepTargets,
			Variables: vars,
		}); err != nil {
			return nil, err
		}

		g.moduleMap[javaModName] = &eval.EvaluatedModule{
			Name: javaModName,
			Type: "java_library",
			Properties: map[string]interface{}{
				"static_libs": javaImports,
			},
		}

		javaTargets = []string{jarTarget}
		g.moduleOutputs[javaModName] = javaTargets
		_ = g.nw.Build(ninja.BuildEdge{
			Outputs: []string{javaModName},
			Rule:    "phony",
			Inputs:  javaTargets,
		})
		allTargets = append(allTargets, javaTargets...)
	}

	// 7. Emit Rust Backend
	if rustEnabled {
		rustModName := mod.Name + "-rust"
		genDir := filepath.Join(g.opts.OutDir, "gen", mod.Name, "rust")
		rsTarget := filepath.Join(genDir, mod.Name+".rs")

		vars := map[string]string{
			"out_dir": genDir,
			"imports": shellJoin(importFlags),
		}
		if len(optFlags) > 0 {
			vars["optional_flags"] = shellJoin(optFlags)
		}

		if err := g.nw.Build(ninja.BuildEdge{
			Outputs:   []string{rsTarget},
			Rule:      "aidl_rust",
			Inputs:    aidlFiles,
			Variables: vars,
		}); err != nil {
			return nil, err
		}

		rustTargets = []string{rsTarget}
		g.moduleOutputs[rustModName] = rustTargets
		_ = g.nw.Build(ninja.BuildEdge{
			Outputs: []string{rustModName},
			Rule:    "phony",
			Inputs:  rustTargets,
		})
		allTargets = append(allTargets, rustTargets...)
	}

	// 8. Register versioned target aliases (e.g. android.hardware.foo-V1-cpp)
	versions := mod.GetStringList("versions")
	if vInfo, ok := mod.Properties["versions_with_info"].([]interface{}); ok {
		for _, item := range vInfo {
			if m, ok := item.(map[string]interface{}); ok {
				if v, ok := m["version"].(string); ok && v != "" {
					versions = append(versions, v)
				}
			}
		}
	}
	versions = dedup(versions)
	for _, v := range versions {
		vPrefix := mod.Name + "-V" + v
		if len(cppTargets) > 0 {
			vCpp := vPrefix + "-cpp"
			vClean := strings.TrimPrefix(vCpp, "lib")
			vStaticTarget := filepath.Join(g.libDir(), "lib"+vClean+".a")
			vSharedTarget := filepath.Join(g.libDir(), "lib"+vClean+".so")

			_ = g.nw.Build(ninja.BuildEdge{
				Outputs: []string{vStaticTarget},
				Rule:    "copy",
				Inputs:  []string{cppTargets[1]},
			})
			_ = g.nw.Build(ninja.BuildEdge{
				Outputs: []string{vSharedTarget},
				Rule:    "copy",
				Inputs:  []string{cppTargets[0]},
			})
			vCppTargets := []string{vSharedTarget, vStaticTarget}
			g.moduleOutputs[vCpp] = vCppTargets
			_ = g.nw.Build(ninja.BuildEdge{
				Outputs: []string{vCpp},
				Rule:    "phony",
				Inputs:  vCppTargets,
			})
			allTargets = append(allTargets, vCppTargets...)
		}
		if len(ndkTargets) > 0 {
			vNdk := vPrefix + "-ndk"
			vClean := strings.TrimPrefix(vNdk, "lib")
			vStaticTarget := filepath.Join(g.libDir(), "lib"+vClean+".a")
			vSharedTarget := filepath.Join(g.libDir(), "lib"+vClean+".so")

			_ = g.nw.Build(ninja.BuildEdge{
				Outputs: []string{vStaticTarget},
				Rule:    "copy",
				Inputs:  []string{ndkTargets[1]},
			})
			_ = g.nw.Build(ninja.BuildEdge{
				Outputs: []string{vSharedTarget},
				Rule:    "copy",
				Inputs:  []string{ndkTargets[0]},
			})
			vNdkTargets := []string{vSharedTarget, vStaticTarget}
			g.moduleOutputs[vNdk] = vNdkTargets
			_ = g.nw.Build(ninja.BuildEdge{
				Outputs: []string{vNdk},
				Rule:    "phony",
				Inputs:  vNdkTargets,
			})
			allTargets = append(allTargets, vNdkTargets...)
		}
		if len(javaTargets) > 0 {
			vJava := vPrefix + "-java"
			g.moduleOutputs[vJava] = javaTargets
			if g.moduleMap[mod.Name+"-java"] != nil {
				g.moduleMap[vJava] = g.moduleMap[mod.Name+"-java"]
			}
			_ = g.nw.Build(ninja.BuildEdge{
				Outputs: []string{vJava},
				Rule:    "phony",
				Inputs:  javaTargets,
			})
		}
		if len(rustTargets) > 0 {
			vRust := vPrefix + "-rust"
			g.moduleOutputs[vRust] = rustTargets
			_ = g.nw.Build(ninja.BuildEdge{
				Outputs: []string{vRust},
				Rule:    "phony",
				Inputs:  rustTargets,
			})
		}
		_ = g.nw.Build(ninja.BuildEdge{
			Outputs: []string{vPrefix},
			Rule:    "phony",
			Inputs:  allTargets,
		})
		g.moduleOutputs[vPrefix] = allTargets
	}

	// 9. Register main module targets and phony
	g.moduleOutputs[mod.Name] = allTargets
	_ = g.nw.Build(ninja.BuildEdge{
		Outputs: []string{mod.Name},
		Rule:    "phony",
		Inputs:  allTargets,
	})

	return allTargets, nil
}
