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

	// 1. Dependency JARs from libs and static_libs
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

		// Check if dependency has registered outputs (e.g. from genrule)
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

	// 2. Prebuilt directory fallback
	if g.opts.PrebuiltLibDir != "" {
		for _, lib := range depLibs {
			prebuilt := filepath.Join(g.opts.PrebuiltLibDir, lib+".jar")
			if fi, err := os.Stat(prebuilt); err == nil && !fi.IsDir() {
				cpEntries = append(cpEntries, prebuilt)
			}
		}
	}

	// 3. Android SDK android.jar for Android-targeted modules
	if !g.opts.IsHost {
		androidJar := g.resolveCurrentAndroidJar()
		if androidJar != "" {
			cpEntries = append(cpEntries, androidJar)
			cpDeps = append(cpDeps, androidJar)
		}
	}

	// 4. SDK prebuilts from TopDir
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

	// 5. Environment CLASSPATH fallback (prebuilt jars in /apex or host environment)
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
	for _, src := range srcs {
		if strings.HasSuffix(src, ".kt") {
			ktSrcs = append(ktSrcs, src)
		} else if strings.HasSuffix(src, ".java") {
			javaSrcs = append(javaSrcs, src)
		} else if strings.HasSuffix(src, ".jar") {
			jarSrcs = append(jarSrcs, src)
		}
	}

	allSrcs := append(append([]string{}, ktSrcs...), javaSrcs...)
	if len(allSrcs) == 0 {
		if g.opts.AllowMissingDeps {
			g.emitPhonyIfNeeded(jarTarget)
			return []string{jarTarget}, nil
		}
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

	if len(ktSrcs) > 0 && len(javaSrcs) == 0 {
		// Pure Kotlin compilation
		rule = "kotlinc_compile"
		cpArg := ""
		if cpFlag != "" {
			cpArg = cpFlag + " "
		}
		cmd = "mkdir -p " + classesDir +
			" && $kotlinc -d " + classesDir + " " + cpArg + strings.Join(ktSrcs, " ") +
			" && $jar cf " + jarTarget + " -C " + classesDir + " ."
	} else if len(ktSrcs) > 0 && len(javaSrcs) > 0 {
		// Mixed Kotlin + Java compilation:
		// 1. kotlinc compiles Kotlin and processes Java sources for cross-language references
		// 2. javac compiles Java sources against Kotlin bytecode
		rule = "kotlin_java_compile"
		cpArg := ""
		if cpFlag != "" {
			cpArg = cpFlag + " "
		}
		javacCp := classesDir
		if cpFlag != "" {
			javacCp = classesDir + ":" + strings.TrimPrefix(cpFlag, "-cp ")
		}
		cmd = "mkdir -p " + classesDir +
			" && $kotlinc -d " + classesDir + " " + cpArg + strings.Join(ktSrcs, " ") + " " + strings.Join(javaSrcs, " ") +
			" && $javac -d " + classesDir + " -cp " + javacCp + " " + strings.Join(javaSrcs, " ") +
			" && $jar cf " + jarTarget + " -C " + classesDir + " ."
	} else {
		// Pure Java compilation
		rule = "javac_compile"
		cpArg := ""
		if cpFlag != "" {
			cpArg = cpFlag + " "
		}
		cmd = "mkdir -p " + classesDir +
			" && $javac -d " + classesDir + " " + cpArg + strings.Join(javaSrcs, " ") +
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
	return g.compileJavaKotlin(mod, jarTarget)
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
		Outputs:   []string{resApk},
		Rule:      "genrule_cmd",
		Inputs:    aaptInputs,
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
