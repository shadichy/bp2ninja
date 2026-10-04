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
		jarTarget := filepath.Join(g.libDir(), lib+".jar")
		cpDeps = append(cpDeps, jarTarget)
		cpEntries = append(cpEntries, jarTarget)

		// If dependency is missing and AllowMissingDeps is true, ensure phony target exists
		if g.opts.AllowMissingDeps {
			if _, ok := g.moduleMap[lib]; !ok {
				g.emitPhonyIfNeeded(jarTarget)
			}
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
	if g.opts.AndroidJarPath != "" && !g.opts.IsHost {
		cpEntries = append(cpEntries, g.opts.AndroidJarPath)
		cpDeps = append(cpDeps, g.opts.AndroidJarPath)
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
	for _, src := range srcs {
		if strings.HasSuffix(src, ".kt") {
			ktSrcs = append(ktSrcs, src)
		} else if strings.HasSuffix(src, ".java") {
			javaSrcs = append(javaSrcs, src)
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
	jarTarget := filepath.Join(g.libDir(), mod.Name+".jar")
	return g.compileJavaKotlin(mod, jarTarget)
}

func (g *Generator) generateJavaImport(mod *eval.EvaluatedModule) ([]string, error) {
	jarTarget := filepath.Join(g.libDir(), mod.Name+".jar")
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
	jarTarget := filepath.Join(g.libDir(), mod.Name+".jar")
	binTarget := filepath.Join(g.binDir(), mod.Name)

	jarOutputs, err := g.compileJavaKotlin(mod, jarTarget)
	if err != nil {
		return nil, err
	}

	mainClass := mod.GetString("main_class")
	runCmd := fmt.Sprintf(`echo '#!/bin/sh\nexec java -cp "%s" %s "$@"' > %s && chmod +x %s`,
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

	return []string{apkTarget}, err
}
