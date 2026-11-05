package generator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"bp2ninja/pkg/eval"
	"bp2ninja/pkg/ninja"
)

func (g *Generator) buildClasspath(mod *eval.EvaluatedModule) (cpFlag string, cpDeps []string) {
	var cpEntries []string

	// =========================================================================
	// Tier 0: Explicit Bootclasspath and Injected Extra JARs
	// =========================================================================
	for _, bcp := range g.opts.BootClasspath {
		if bcp != "" {
			if fi, err := os.Stat(bcp); err == nil && !fi.IsDir() {
				cpEntries = append(cpEntries, bcp)
				cpDeps = append(cpDeps, bcp)
			}
		}
	}
	for _, ej := range g.opts.ExtraJars {
		if ej != "" {
			if fi, err := os.Stat(ej); err == nil && !fi.IsDir() {
				cpEntries = append(cpEntries, ej)
				cpDeps = append(cpDeps, ej)
			}
		}
	}

	// =========================================================================
	// Tier 1 & Tier 2: Dependencies from libs and static_libs
	// =========================================================================
	depLibs := append(mod.GetStringList("libs"), mod.GetStringList("static_libs")...)
	for _, lib := range depLibs {
		if lib == "current_android_jar" || lib == ":current_android_jar" || lib == "system_android_jar" || lib == ":system_android_jar" {
			jar := g.resolveCurrentAndroidJar()
			if jar != "" {
				cpDeps = append(cpDeps, jar)
				cpEntries = append(cpEntries, jar)
			}
			continue
		}

		cleanLib := strings.TrimSuffix(lib, ".jar")
		var targets []string

		// ---------------------------------------------------------------------
		// Tier 1: Graph-Local Module Outputs (Same Ninja Manifest)
		// ---------------------------------------------------------------------
		if outs, ok := g.moduleOutputs[lib]; ok && len(outs) > 0 {
			for _, o := range outs {
				if strings.HasSuffix(o, ".jar") {
					targets = append(targets, o)
				}
			}
		}
		if len(targets) == 0 {
			if outs, ok := g.moduleOutputs[cleanLib]; ok && len(outs) > 0 {
				for _, o := range outs {
					if strings.HasSuffix(o, ".jar") {
						targets = append(targets, o)
					}
				}
			}
		}

		// Check if dependency is a genrule/java_genrule in moduleMap
		if len(targets) == 0 {
			for _, key := range []string{lib, cleanLib} {
				if depMod, ok := g.moduleMap[key]; ok && (depMod.Type == "genrule" || depMod.Type == "java_genrule") {
					for _, out := range depMod.GetStringList("out") {
						if strings.HasSuffix(out, ".jar") {
							targets = append(targets, filepath.Join(g.opts.OutDir, "gen", key, out))
						}
					}
					if len(targets) > 0 {
						break
					}
				}
			}
		}

		// Check if dependency is an aidl_interface or droidstubs
		if len(targets) == 0 {
			if outs, ok := g.moduleOutputs[lib+"-java"]; ok && len(outs) > 0 {
				for _, o := range outs {
					if strings.HasSuffix(o, ".jar") {
						targets = append(targets, o)
					}
				}
			}
		}
		if len(targets) == 0 {
			if depMod, ok := g.moduleMap[lib]; ok {
				if depMod.Type == "aidl_interface" {
					targets = append(targets, filepath.Join(g.libDir(), lib+"-java.jar"))
				} else if depMod.Type == "droidstubs" || depMod.Type == "droiddoc" {
					targets = append(targets, filepath.Join(g.opts.OutDir, "gen", lib, "stubs.srcjar"))
				}
			}
		}

		// ---------------------------------------------------------------------
		// Tier 2: Extractive APEX & Shadow Prebuilt Physical JARs
		// ---------------------------------------------------------------------
		if len(targets) == 0 {
			if physJar := g.resolvePhysicalJar(lib); physJar != "" {
				targets = append(targets, physJar)
			}
		}

		// ---------------------------------------------------------------------
		// Tier 6: Synthetic Phony Fallback (Only if no local target or physical jar)
		// ---------------------------------------------------------------------
		if len(targets) == 0 {
			jarTarget := filepath.Join(g.libDir(), cleanLib+".jar")
			targets = append(targets, jarTarget)
			if g.opts.AllowMissingDeps {
				hasJarOutput := false
				for _, o := range append(g.moduleOutputs[lib], g.moduleOutputs[cleanLib]...) {
					if strings.HasSuffix(o, ".jar") {
						hasJarOutput = true
						break
					}
				}
				if !hasJarOutput {
					g.emitPhonyIfNeeded(jarTarget)
				}
			}
		}

		for _, t := range targets {
			cpDeps = append(cpDeps, t)
			cpEntries = append(cpEntries, t)
		}
	}

	// =========================================================================
	// Tier 3: Android SDK android.jar and Platform Core Jars (sdk_version aware)
	// =========================================================================
	if !g.opts.IsHost {
		androidJar := g.resolveCurrentAndroidJar()
		if androidJar != "" {
			cpEntries = append(cpEntries, androidJar)
			cpDeps = append(cpDeps, androidJar)
		}

		sdkVer := mod.GetString("sdk_version")
		if sdkVer == "core_platform" || strings.HasPrefix(sdkVer, "core_") || sdkVer == "none" || strings.Contains(sdkVer, "module_") {
			// Resolve core platform library (art.module.public.api / art-module-lib.jar)
			var coreJar string
			for _, artModName := range []string{"art.module.public.api.stubs.module_lib", "art.module.public.api", "art.module.public.api.stubs"} {
				if outs, ok := g.moduleOutputs[artModName]; ok && len(outs) > 0 {
					coreJar = outs[0]
					break
				}
			}
			if coreJar == "" {
				coreJar = g.resolvePhysicalJar("art.module.public.api.stubs.module_lib")
			}
			if coreJar == "" {
				coreJar = g.resolvePhysicalJar("core-libart")
			}
			if coreJar == "" && g.opts.TopDir != "" {
				targetApi := g.opts.APILevel
				if targetApi <= 0 {
					targetApi = 34
				}
				candList := []string{
					filepath.Join(g.opts.TopDir, "prebuilts/target/art/share/art/art-module-lib.jar"),
					filepath.Join(g.opts.TopDir, "prebuilts/sdk", fmt.Sprintf("%d", targetApi), "module-lib/art.jar"),
					filepath.Join(g.opts.TopDir, "prebuilts/sdk/current/module-lib/art.jar"),
					filepath.Join(g.opts.TopDir, "prebuilts/target/libcore/share/libcore/core-libart.jar"),
				}
				for _, c := range candList {
					if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
						coreJar = c
						break
					}
				}
			}
			if coreJar != "" {
				cpEntries = append(cpEntries, coreJar)
				cpDeps = append(cpDeps, coreJar)
			}
		}
	}

	// =========================================================================
	// Tier 4: In-Tree SDK Prebuilts (prebuilts/sdk/<api>/...)
	// =========================================================================
	if g.opts.TopDir != "" {
		targetApi := g.opts.APILevel
		if targetApi <= 0 {
			targetApi = 34
		}
		for _, lib := range depLibs {
			cleanLib := strings.TrimSuffix(lib, ".jar")
			topJarCandidates := []string{
				filepath.Join(g.opts.TopDir, "prebuilts/sdk", fmt.Sprintf("%d", targetApi), "public", cleanLib+".jar"),
				filepath.Join(g.opts.TopDir, "prebuilts/sdk", "current/public", cleanLib+".jar"),
			}
			for _, c := range topJarCandidates {
				if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
					cpEntries = append(cpEntries, c)
					cpDeps = append(cpDeps, c)
					break
				}
			}
		}
	}

	// =========================================================================
	// Tier 5: Environment CLASSPATH fallback
	// =========================================================================
	if envCP := os.Getenv("CLASSPATH"); envCP != "" {
		for _, part := range strings.Split(envCP, ":") {
			if part != "" {
				if fi, err := os.Stat(part); err == nil && !fi.IsDir() {
					cpEntries = append(cpEntries, part)
				}
			}
		}
	}

	cpEntries = dedup(cpEntries)
	cpDeps = dedup(cpDeps)

	if len(cpEntries) > 0 {
		cpFlag = "-cp " + strings.Join(cpEntries, ":")
	}
	return cpFlag, cpDeps
}

func (g *Generator) compileJavaKotlin(mod *eval.EvaluatedModule, jarTarget string) ([]string, error) {
	srcs := g.ResolveSrcs(mod)

	var javaSrcs []string
	var ktSrcs []string
	var jarSrcs []string
	var srcjarSrcs []string
	var aidlSrcs []string
	for _, src := range srcs {
		if strings.HasSuffix(src, ".kt") {
			ktSrcs = append(ktSrcs, src)
		} else if strings.HasSuffix(src, ".java") {
			javaSrcs = append(javaSrcs, src)
		} else if strings.HasSuffix(src, ".srcjar") {
			srcjarSrcs = append(srcjarSrcs, src)
		} else if strings.HasSuffix(src, ".jar") {
			jarSrcs = append(jarSrcs, src)
		} else if strings.HasSuffix(src, ".aidl") {
			aidlSrcs = append(aidlSrcs, src)
		}
	}

	allSrcs := append(append(append(append([]string{}, ktSrcs...), javaSrcs...), srcjarSrcs...), aidlSrcs...)
	if len(allSrcs) == 0 {
		return nil, nil
	}

	cpFlag, cpDeps := g.buildClasspath(mod)
	if len(jarSrcs) > 0 {
		cpDeps = append(cpDeps, jarSrcs...)
		var currentCp []string
		if cpFlag != "" {
			currentCp = strings.Split(strings.TrimPrefix(cpFlag, "-cp "), ":")
		}
		currentCp = append(currentCp, jarSrcs...)
		currentCp = dedup(currentCp)
		cpFlag = "-cp " + strings.Join(currentCp, ":")
	}
	classesDir := filepath.Join(g.opts.OutDir, "classes", mod.Name)

	var rule string
	var cmd string

	encoding := g.opts.JavaEncoding
	if encoding == "" {
		encoding = "UTF-8"
	}
	envPrefix := "export LC_ALL=C.UTF-8 LANG=C.UTF-8 JAVA_TOOL_OPTIONS=\"-Dfile.encoding=UTF-8\" && "

	// System modules support (--system)
	sysModulesArg := ""
	if sm := mod.GetString("system_modules"); sm != "" {
		sysModulesDir := filepath.Join(g.opts.OutDir, "system_modules", sm)
		sysModulesArg = "--system=" + sysModulesDir + " "
		if outs, ok := g.moduleOutputs[sm]; ok {
			cpDeps = append(cpDeps, outs...)
		}
	}

	srcjarExtractCmd := ""
	srcjarJavaArg := ""
	if len(srcjarSrcs) > 0 {
		srcjarDir := filepath.Join(classesDir, "srcgen")
		srcjarExtractCmd = "mkdir -p " + srcjarDir + " && for sj in " + strings.Join(srcjarSrcs, " ") + "; do unzip -qo -d " + srcjarDir + " $$sj 2>/dev/null || true; done && find " + srcjarDir + " -name '*.java' > " + classesDir + "/srcjar.list && "
		srcjarJavaArg = " @" + classesDir + "/srcjar.list"
	}

	aidlGenCmd := ""
	aidlJavaArg := ""
	if len(aidlSrcs) > 0 {
		aidlDir := filepath.Join(classesDir, "aidlgen")
		var aidlIncludes []string
		if mod.Dir != "" && mod.Dir != "." {
			aidlIncludes = append(aidlIncludes, "-I"+mod.Dir)
		} else {
			aidlIncludes = append(aidlIncludes, "-I.")
		}
		if aidlMap := mod.GetMap("aidl"); aidlMap != nil {
			if incDirs, ok := aidlMap["local_include_dirs"].([]interface{}); ok {
				for _, id := range incDirs {
					if s, ok := id.(string); ok {
						if mod.Dir != "" && mod.Dir != "." {
							aidlIncludes = append(aidlIncludes, "-I"+filepath.Join(mod.Dir, s))
						} else {
							aidlIncludes = append(aidlIncludes, "-I"+s)
						}
					}
				}
			}
			if incDirs, ok := aidlMap["include_dirs"].([]interface{}); ok {
				for _, id := range incDirs {
					if s, ok := id.(string); ok {
						aidlIncludes = append(aidlIncludes, "-I"+s)
					}
				}
			}
		}
		for _, commonInc := range []string{"frameworks/base/core/java", "frameworks/libs/modules-utils/java"} {
			if g.opts.TopDir != "" {
				if fi, err := os.Stat(filepath.Join(g.opts.TopDir, commonInc)); err == nil && fi.IsDir() {
					aidlIncludes = append(aidlIncludes, "-I"+commonInc)
				}
			}
		}
		aidlIncludes = dedup(aidlIncludes)
		incArgs := strings.Join(aidlIncludes, " ")

		aidlGenCmd = fmt.Sprintf("mkdir -p %s && $aidl --lang=java -o %s %s %s && find %s -name '*.java' > %s/aidl.list && ",
			aidlDir, aidlDir, incArgs, strings.Join(aidlSrcs, " "), aidlDir, classesDir)
		aidlJavaArg = " @" + classesDir + "/aidl.list"
	}

	javaFiles := strings.Join(javaSrcs, " ")
	if javaFiles != "" {
		javaFiles = " " + javaFiles
	}

	if len(ktSrcs) > 0 && len(javaSrcs) == 0 && len(srcjarSrcs) == 0 && len(aidlSrcs) == 0 {
		// Pure Kotlin compilation
		rule = "kotlinc_compile"
		cpArg := ""
		if cpFlag != "" {
			cpArg = cpFlag + " "
		}
		cmd = envPrefix + "mkdir -p " + classesDir +
			" && $kotlinc -d " + classesDir + " " + cpArg + strings.Join(ktSrcs, " ") +
			" && $jar cf " + jarTarget + " -C " + classesDir + " ."
	} else if len(ktSrcs) > 0 && (len(javaSrcs) > 0 || len(srcjarSrcs) > 0 || len(aidlSrcs) > 0) {
		// Mixed Kotlin + Java compilation:
		rule = "kotlin_java_compile"
		cpArg := ""
		if cpFlag != "" {
			cpArg = cpFlag + " "
		}
		javacCp := classesDir
		if cpFlag != "" {
			javacCp = classesDir + ":" + strings.TrimPrefix(cpFlag, "-cp ")
		}
		cmd = envPrefix + "mkdir -p " + classesDir + " && " + aidlGenCmd + srcjarExtractCmd +
			"$kotlinc -d " + classesDir + " " + cpArg + strings.Join(ktSrcs, " ") + javaFiles +
			" && $javac -encoding " + encoding + " " + sysModulesArg + "-d " + classesDir + " -cp " + javacCp + javaFiles + aidlJavaArg + srcjarJavaArg +
			" && $jar cf " + jarTarget + " -C " + classesDir + " ."
	} else {
		// Pure Java compilation
		rule = "javac_compile"
		cpArg := ""
		if cpFlag != "" {
			cpArg = cpFlag + " "
		}
		cmd = envPrefix + "mkdir -p " + classesDir + " && " + aidlGenCmd + srcjarExtractCmd +
			"$javac -encoding " + encoding + " " + sysModulesArg + "-d " + classesDir + " " + cpArg + javaFiles + aidlJavaArg + srcjarJavaArg +
			" && $jar cf " + jarTarget + " -C " + classesDir + " ."
	}

	err := g.nw.Build(ninja.BuildEdge{
		Outputs:   []string{jarTarget},
		Rule:      rule,
		Inputs:    allSrcs,
		Implicits: cpDeps,
		Variables: map[string]string{
			"cmd": cmd,
		},
	})

	return []string{jarTarget}, err
}

func (g *Generator) generateJavaLibrary(mod *eval.EvaluatedModule) ([]string, error) {
	jarTarget := filepath.Join(g.libDir(), strings.TrimSuffix(mod.Name, ".jar")+".jar")
	outs, err := g.compileJavaKotlin(mod, jarTarget)
	if err != nil {
		return nil, err
	}
	if len(outs) == 0 {
		if g.opts.AllowMissingDeps {
			g.emitPhonyIfNeeded(jarTarget)
			return []string{jarTarget}, nil
		}
		return nil, nil
	}
	return outs, nil
}

func (g *Generator) generateJavaImport(mod *eval.EvaluatedModule) ([]string, error) {
	jarTarget := filepath.Join(g.libDir(), strings.TrimSuffix(mod.Name, ".jar")+".jar")
	jars := mod.GetStringList("jars")
	if len(jars) == 0 {
		if g.opts.AllowMissingDeps {
			g.emitPhonyIfNeeded(jarTarget)
			return []string{jarTarget}, nil
		}
		return nil, nil
	}

	srcJar := jars[0]
	if mod.Dir != "" && mod.Dir != "." && !filepath.IsAbs(srcJar) {
		srcJar = filepath.Join(mod.Dir, srcJar)
	}

	err := g.nw.Build(ninja.BuildEdge{
		Outputs: []string{jarTarget},
		Rule:    "copy",
		Inputs:  []string{srcJar},
	})
	return []string{jarTarget}, err
}

func (g *Generator) generateJavaBinary(mod *eval.EvaluatedModule) ([]string, error) {
	jarTarget := filepath.Join(g.libDir(), strings.TrimSuffix(mod.Name, ".jar")+".jar")
	binTarget := filepath.Join(g.binDir(), strings.TrimSuffix(mod.Name, ".jar"))

	jarOutputs, err := g.compileJavaKotlin(mod, jarTarget)
	if err != nil {
		return nil, err
	}

	mainClass := mod.GetString("main_class")
	runCmd := fmt.Sprintf(`echo '#!/bin/sh\nexec java -cp "%s" %s "$$@"' > %s && chmod +x %s`,
		jarTarget, mainClass, binTarget, binTarget)

	if err := g.nw.Build(ninja.BuildEdge{
		Outputs: []string{binTarget},
		Rule:    "genrule_cmd",
		Inputs:  jarOutputs,
		Variables: map[string]string{
			"cmd": runCmd,
		},
	}); err != nil {
		return nil, err
	}

	return []string{binTarget, jarTarget}, nil
}

func (g *Generator) generateAndroidApp(mod *eval.EvaluatedModule) ([]string, error) {
	manifest := mod.GetString("manifest")
	if manifest == "" {
		manifest = "AndroidManifest.xml"
	}

	manifestPath := manifest
	if !filepath.IsAbs(manifest) && g.opts.BpDir != "" {
		manifestPath = filepath.Join(g.opts.BpDir, manifest)
	}
	g.emitPhonyIfNeeded(manifestPath)

	appDir := filepath.Join(g.opts.OutDir, "apps", mod.Name)
	apkTarget := filepath.Join(g.binDir(), mod.Name+".apk")
	jarTarget := filepath.Join(appDir, mod.Name+".jar")

	// 1. Compile Java/Kotlin sources if present
	jarOutputs, err := g.compileJavaKotlin(mod, jarTarget)
	if err != nil {
		return nil, err
	}

	// 2. AAPT2 link rule for resources
	resApk := filepath.Join(appDir, "resources.apk")
	vars := map[string]string{
		"manifest": manifest,
	}
	if g.opts.AndroidJarPath != "" {
		vars["android_jar"] = g.opts.AndroidJarPath
	}

	aaptInputs := []string{manifestPath}
	aaptCmd := "$aapt2 link --manifest " + manifest + " -o " + resApk
	if g.opts.AndroidJarPath != "" {
		aaptCmd = "$aapt2 link -I " + g.opts.AndroidJarPath + " --manifest " + manifest + " -o " + resApk
	}

	if err := g.nw.Build(ninja.BuildEdge{
		Outputs: []string{resApk},
		Rule:    "genrule_cmd",
		Inputs:  aaptInputs,
		Variables: map[string]string{
			"cmd": aaptCmd,
		},
	}); err != nil {
		return nil, err
	}

	// 3. Final APK target
	finalInputs := []string{resApk}
	if len(jarOutputs) > 0 {
		finalInputs = append(finalInputs, jarOutputs...)
	}

	err = g.nw.Build(ninja.BuildEdge{
		Outputs:   []string{apkTarget},
		Rule:      "copy",
		Inputs:    finalInputs,
		Variables: vars,
	})
	if err != nil {
		return nil, err
	}

	// 4. Also provide lib/<name>.jar for other modules depending on this app's classes
	libJarTarget := filepath.Join(g.libDir(), strings.TrimSuffix(mod.Name, ".jar")+".jar")
	if len(jarOutputs) > 0 {
		if err := g.nw.Build(ninja.BuildEdge{
			Outputs: []string{libJarTarget},
			Rule:    "copy",
			Inputs:  jarOutputs,
		}); err != nil {
			return nil, err
		}
	} else if g.opts.AllowMissingDeps {
		g.emitPhonyIfNeeded(libJarTarget)
	}

	return []string{apkTarget, libJarTarget}, nil
}

func (g *Generator) generateAndroidAppImport(mod *eval.EvaluatedModule) ([]string, error) {
	apk := mod.GetString("apk")
	if apk == "" {
		if g.opts.AllowMissingDeps {
			target := filepath.Join(g.binDir(), mod.Name+".apk")
			g.emitPhonyIfNeeded(target)
			return []string{target}, nil
		}
		return nil, nil
	}

	srcApk := apk
	if mod.Dir != "" && mod.Dir != "." && !filepath.IsAbs(srcApk) {
		srcApk = filepath.Clean(filepath.Join(mod.Dir, srcApk))
	}

	apkTarget := filepath.Join(g.binDir(), mod.Name+".apk")
	if err := g.nw.Build(ninja.BuildEdge{
		Outputs: []string{apkTarget},
		Rule:    "copy",
		Inputs:  []string{srcApk},
	}); err != nil {
		return nil, err
	}

	libJarTarget := filepath.Join(g.libDir(), strings.TrimSuffix(mod.Name, ".jar")+".jar")
	if err := g.nw.Build(ninja.BuildEdge{
		Outputs: []string{libJarTarget},
		Rule:    "copy",
		Inputs:  []string{srcApk},
	}); err != nil {
		return nil, err
	}

	targets := []string{apkTarget, libJarTarget}

	// Register srcjar if present (e.g. R.java / Manifest.java symbols)
	srcjar := mod.GetString("srcjar")
	if srcjar != "" {
		srcjarPath := srcjar
		if mod.Dir != "" && mod.Dir != "." && !filepath.IsAbs(srcjarPath) {
			srcjarPath = filepath.Clean(filepath.Join(mod.Dir, srcjarPath))
		}
		g.taggedOutputs[mod.Name+":.aapt.srcjar"] = []string{srcjarPath}
		g.taggedOutputs[mod.Name+":.srcjar"] = []string{srcjarPath}
		g.taggedOutputs[mod.Name+":.aapt.jar"] = []string{libJarTarget}
	}

	return targets, nil
}

func (g *Generator) generateJavaSdkLibraryImport(mod *eval.EvaluatedModule) ([]string, error) {
	var targets []string

	extractJars := func(subName string) []string {
		if subMap := mod.GetMap(subName); subMap != nil {
			if rawJars, ok := subMap["jars"].([]interface{}); ok {
				var res []string
				for _, r := range rawJars {
					if s, ok := r.(string); ok {
						res = append(res, s)
					}
				}
				return res
			}
		}
		return nil
	}

	moduleLibJars := extractJars("module_lib")
	systemJars := extractJars("system")
	publicJars := extractJars("public")

	bestJars := moduleLibJars
	if len(bestJars) == 0 {
		bestJars = systemJars
	}
	if len(bestJars) == 0 {
		bestJars = publicJars
	}

	registerScope := func(suffix string, jars []string) {
		if len(jars) == 0 {
			return
		}
		srcJar := jars[0]
		if mod.Dir != "" && mod.Dir != "." && !filepath.IsAbs(srcJar) {
			srcJar = filepath.Clean(filepath.Join(mod.Dir, srcJar))
		}
		scopeName := mod.Name + suffix
		targetJar := filepath.Join(g.libDir(), scopeName+".jar")
		_ = g.nw.Build(ninja.BuildEdge{
			Outputs: []string{targetJar},
			Rule:    "copy",
			Inputs:  []string{srcJar},
		})
		targets = append(targets, targetJar)
		g.moduleOutputs[scopeName] = []string{targetJar}
	}

	registerScope("", bestJars)
	registerScope(".stubs", publicJars)
	registerScope(".stubs.system", systemJars)
	registerScope(".stubs.module_lib", moduleLibJars)

	return targets, nil
}
