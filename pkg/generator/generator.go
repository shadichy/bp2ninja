package generator

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"bp2ninja/pkg/eval"
	"bp2ninja/pkg/ninja"
	"bp2ninja/pkg/plugins"
)

// Options specifies configuration for Ninja generation.
type Options struct {
	TopDir           string
	OutDir           string
	SysrootDir       string
	PrebuiltLibDir   string
	ClangPath        string
	ClangCxxPath     string
	ArPath           string
	Aapt2Path        string
	AndroidJarPath   string
	KotlincPath      string
	JavacPath        string
	JarPath          string
	TargetArch       string
	TargetTriple     string
	APILevel         int
	NDKDir           string
	AllowMissingDeps bool
	BpDir            string
	ExtraCflags      []string
	ExtraCppflags    []string
	ExtraLdflags     []string
	IsHost           bool
}

// DefaultOptions provides sensible defaults for standalone Android builds.
func DefaultOptions(topDir, outDir string) Options {
	if topDir == "" {
		topDir = "."
	}
	if outDir == "" {
		outDir = "out"
	}
	return Options{
		TopDir:           topDir,
		OutDir:           outDir,
		TargetArch:       "arm64",
		TargetTriple:     "aarch64-linux-android",
		APILevel:         34,
		ClangPath:        "clang",
		ClangCxxPath:     "clang++",
		ArPath:           "ar",
		Aapt2Path:        "aapt2",
		AndroidJarPath:   "",
		KotlincPath:      "kotlinc",
		JavacPath:        "javac",
		JarPath:          "jar",
		AllowMissingDeps: true,
	}
}

// Generator translates evaluated modules into a Ninja build file.
type Generator struct {
	opts           Options
	nw             *ninja.Writer
	registry       *plugins.Registry
	moduleMap      map[string]*eval.EvaluatedModule
	moduleOutputs  map[string][]string
	filegroups     map[string][]string
	emittedPhonies map[string]bool
	topDirModules  map[string]string
	protoHeaders   map[string][]string

	// MissingDeps records external dependencies not found in the parsed blueprint set.
	MissingDeps []string
	// CircularDeps records circular dependency chains detected during topological sort.
	CircularDeps [][]string
}

// New creates a new Generator.
func New(opts Options, nw *ninja.Writer, reg *plugins.Registry) *Generator {
	if reg == nil {
		reg = plugins.GlobalRegistry
	}
	return &Generator{
		opts:           opts,
		nw:             nw,
		registry:       reg,
		moduleMap:      make(map[string]*eval.EvaluatedModule),
		moduleOutputs:  make(map[string][]string),
		filegroups:     make(map[string][]string),
		emittedPhonies: make(map[string]bool),
		topDirModules:  nil,
		protoHeaders:   make(map[string][]string),
	}
}

// GetTopDirModules scans g.opts.TopDir for component directories and module names.
// Skips build artifacts (.git, out) and toolchains (clang, rust, sdk, prebuilts).
func (g *Generator) GetTopDirModules() map[string]string {
	if g.topDirModules != nil {
		return g.topDirModules
	}
	g.topDirModules = make(map[string]string)
	top := g.opts.TopDir
	if g.opts.IsHost || top == "" || top == "." || top == "/usr" {
		return g.topDirModules
	}
	fi, err := os.Stat(top)
	if err != nil || !fi.IsDir() {
		return g.topDirModules
	}

	skipDirs := map[string]bool{
		".git": true, ".repo": true, "out": true, "prebuilts": true, "toolchain": true,
		"clang": true, "rust": true, "sdk": true, "cts": true, "kernel": true,
		"device": true, "packages": true, "tools": true, "development": true,
		"platform_testing": true, "developers": true, "test": true, "vendor": true,
	}

	nameRegex := regexp.MustCompile(`\bname\s*:\s*"([^"]+)"`)

	_ = filepath.Walk(top, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			baseName := info.Name()
			if skipDirs[baseName] {
				return filepath.SkipDir
			}
			rel, _ := filepath.Rel(top, path)
			if strings.Count(rel, string(filepath.Separator)) > 4 {
				return filepath.SkipDir
			}

			// Map directory base name
			g.topDirModules[baseName] = path
			if strings.HasSuffix(baseName, "_headers") {
				g.topDirModules[strings.TrimSuffix(baseName, "_headers")] = path
			}

			// If Android.bp or Android.bp.prebuilt exists in candidate directory, map declared module names
			for _, bpFile := range []string{"Android.bp", "Android.bp.prebuilt"} {
				bpPath := filepath.Join(path, bpFile)
				if data, err := os.ReadFile(bpPath); err == nil {
					for _, match := range nameRegex.FindAllSubmatch(data, -1) {
						mName := string(match[1])
						g.topDirModules[mName] = path
						if strings.HasSuffix(mName, "_headers") {
							g.topDirModules[strings.TrimSuffix(mName, "_headers")] = path
						}
					}
				}
			}
		}
		return nil
	})

	return g.topDirModules
}

func (g *Generator) emitPhonyIfNeeded(target string) {
	if !g.opts.AllowMissingDeps || target == "" {
		return
	}
	if g.emittedPhonies[target] {
		return
	}
	g.emittedPhonies[target] = true
	_ = g.nw.Build(ninja.BuildEdge{
		Outputs: []string{target},
		Rule:    "phony",
	})
}

func (g *Generator) resolveReference(ref string) []string {
	if ref == "current_android_jar" || ref == "system_android_jar" {
		jar := g.resolveCurrentAndroidJar()
		if jar != "" {
			return []string{jar}
		}
	}
	if strings.HasPrefix(ref, ":") {
		label := strings.TrimPrefix(ref, ":")
		if fg, ok := g.filegroups[label]; ok && len(fg) > 0 {
			return fg
		}
		if outs, ok := g.moduleOutputs[label]; ok && len(outs) > 0 {
			return outs
		}
		if genMod, ok := g.moduleMap[label]; ok && genMod.Type == "genrule" {
			var genOuts []string
			for _, out := range genMod.GetStringList("out") {
				genOuts = append(genOuts, filepath.Join(g.opts.OutDir, "gen", label, out))
			}
			return genOuts
		}
		if label == "current_android_jar" || label == "system_android_jar" {
			jar := g.resolveCurrentAndroidJar()
			if jar != "" {
				return []string{jar}
			}
		}
		if g.opts.AllowMissingDeps {
			g.emitPhonyIfNeeded(ref)
			return []string{ref}
		}
	} else if strings.HasPrefix(ref, "//") {
		parts := strings.Split(ref, ":")
		if len(parts) == 2 {
			label := parts[1]
			if fg, ok := g.filegroups[label]; ok && len(fg) > 0 {
				return fg
			}
			if outs, ok := g.moduleOutputs[label]; ok && len(outs) > 0 {
				return outs
			}
			if label == "current_android_jar" || label == "system_android_jar" {
				jar := g.resolveCurrentAndroidJar()
				if jar != "" {
					return []string{jar}
				}
			}
		}
		if g.opts.AllowMissingDeps {
			g.emitPhonyIfNeeded(ref)
			return []string{ref}
		}
	}
	return []string{ref}
}

// Generate emits global variables, standard rules, and module build edges.
func (g *Generator) Generate(modules []*eval.EvaluatedModule) error {
	if err := g.emitHeader(); err != nil {
		return err
	}
	if err := g.emitStandardRules(); err != nil {
		return err
	}

	// Sort modules by internal dependencies and report missing/circular dependencies
	modules = g.SortModules(modules)

	// Index all modules and filegroups by name
	for _, mod := range modules {
		if mod.Name != "" {
			g.moduleMap[mod.Name] = mod
			if mod.Type == "filegroup" {
				var fgSrcs []string
				for _, s := range mod.GetStringList("srcs") {
					srcPath := s
					if mod.Dir != "" && mod.Dir != "." && !filepath.IsAbs(s) && !strings.HasPrefix(s, ":") {
						srcPath = filepath.Clean(filepath.Join(mod.Dir, s))
					}
					fgSrcs = append(fgSrcs, g.expandGlob(srcPath)...)
				}
				fgExcludes := mod.GetStringList("exclude_srcs")
				fgSrcs = filterExcludeSrcs(fgSrcs, fgExcludes, mod.Dir)
				g.filegroups[mod.Name] = fgSrcs
			}
			// Pre-index proto outputs for any module defining .proto sources
			for _, src := range mod.GetStringList("srcs") {
				if strings.HasSuffix(src, ".proto") {
					rel := src
					if mod.Dir != "" && mod.Dir != "." && strings.HasPrefix(src, mod.Dir+"/") {
						rel = strings.TrimPrefix(src, mod.Dir+"/")
					}
					pbRel := strings.TrimSuffix(rel, ".proto")
					pbHdr := filepath.Join(g.opts.OutDir, "gen", mod.Name, pbRel+".pb.h")
					g.protoHeaders[mod.Name] = append(g.protoHeaders[mod.Name], pbHdr)
				}
			}
		}
	}

	var allTargets []string

	for _, mod := range modules {
		if mod.Name == "" || !mod.IsEnabled() {
			continue
		}

		// 1. Check if a dynamic plugin or builtin handler handles this module type
		if handler, ok := g.registry.Get(mod.Type); ok {
			ctx := &plugins.PluginContext{
				ModuleName:       mod.Name,
				ModuleType:       mod.Type,
				Properties:       mod.Properties,
				TopDir:           g.opts.TopDir,
				OutDir:           g.opts.OutDir,
				BpDir:            g.opts.BpDir,
				SubDir:           mod.Dir,
				AllowMissingDeps: g.opts.AllowMissingDeps,
				NinjaWriter:      g.nw,
			}
			targets, err := handler.HandleModule(ctx)
			if err != nil {
				return fmt.Errorf("plugin failed for module %s (%s): %w", mod.Name, mod.Type, err)
			}
			g.moduleOutputs[mod.Name] = targets
			allTargets = append(allTargets, targets...)
			continue
		}

		// Standard module generators
		var targets []string
		var err error

		switch mod.Type {
		case "package", "license", "license_kind", "package_metadata",
			"soong_namespace", "soong_config_module_type", "soong_config_string_variable",
			"soong_config_bool_variable", "soong_config_module_type_import",
			"filegroup", "phony", "ndk_headers", "ndk_library", "vintf_fragment",
			"cc_defaults", "java_defaults":
			// Declarative meta-modules in generic BP: no direct build action
			continue
		case "cc_binary", "cc_binary_host":
			targets, err = g.generateCcBinary(mod)
		case "cc_library", "cc_library_shared", "cc_library_static", "cc_library_headers",
			"cc_library_host", "cc_library_host_shared", "cc_library_host_static":
			targets, err = g.generateCcLibrary(mod)
		case "cc_test", "cc_benchmark", "cc_test_host", "cc_fuzz":
			targets, err = g.generateCcTest(mod)
		case "android_app", "android_app_certificate":
			targets, err = g.generateAndroidApp(mod)
		case "java_library", "java_library_host", "android_library", "java_library_static":
			targets, err = g.generateJavaLibrary(mod)
		case "java_import", "android_library_import":
			targets, err = g.generateJavaImport(mod)
		case "java_binary", "java_binary_host":
			targets, err = g.generateJavaBinary(mod)
		case "genrule":
			targets, err = g.generateGenrule(mod)
		case "gensrcs":
			targets, err = g.generateGensrcs(mod)
		case "prebuilt_etc", "prebuilt_etc_host", "sh_binary", "sh_binary_host":
			targets, err = g.generatePrebuilt(mod)
		case "python_binary", "python_binary_host", "python_test_host":
			targets, err = g.generatePythonBinary(mod)
		default:
			// Fallback only: Pattern recognition heuristic when no plugin or handler exists
			targets, err = g.autoDeriveFallback(mod)
		}

		if err != nil {
			return fmt.Errorf("failed generating module %s: %w", mod.Name, err)
		}
		g.moduleOutputs[mod.Name] = targets
		if !strings.Contains(mod.Type, "test") && !strings.Contains(mod.Type, "benchmark") {
			allTargets = append(allTargets, targets...)
		}
	}

	if len(allTargets) == 0 {
		for _, outs := range g.moduleOutputs {
			allTargets = append(allTargets, outs...)
		}
	}

	if len(allTargets) > 0 {
		g.nw.BlankLine()
		g.nw.Default(allTargets...)
	}

	return g.nw.Flush()
}

func (g *Generator) emitHeader() error {
	g.nw.Comment("Generated by bp2ninja (Standalone Android.bp to Ninja Converter)")
	g.nw.Comment("Dependency checks bypassed for rapid standalone iteration")
	g.nw.BlankLine()
	g.nw.Variable("ninja_required_version", "1.7.0")
	g.nw.Variable("builddir", g.opts.OutDir)
	relTop := g.opts.TopDir
	if relTop == "" {
		relTop = "."
	}
	g.nw.Variable("top", relTop)
	ccPath := g.opts.ClangPath
	cxxPath := g.opts.ClangCxxPath
	arPath := g.opts.ArPath
	sysrootVal := g.opts.SysrootDir

	if g.opts.NDKDir != "" {
		ndkVal := g.opts.NDKDir
		g.nw.Variable("ndk", ndkVal)
		if strings.HasPrefix(sysrootVal, g.opts.NDKDir) {
			sysrootVal = "$ndk" + strings.TrimPrefix(sysrootVal, g.opts.NDKDir)
		}
		if strings.HasPrefix(ccPath, g.opts.NDKDir) {
			ccPath = "$ndk" + strings.TrimPrefix(ccPath, g.opts.NDKDir)
		}
		if strings.HasPrefix(cxxPath, g.opts.NDKDir) {
			cxxPath = "$ndk" + strings.TrimPrefix(cxxPath, g.opts.NDKDir)
		}
		if strings.HasPrefix(arPath, g.opts.NDKDir) {
			arPath = "$ndk" + strings.TrimPrefix(arPath, g.opts.NDKDir)
		}
	}
	if sysrootVal != "" {
		g.nw.Variable("sysroot", sysrootVal)
	}
	g.nw.Variable("cc", ccPath)
	g.nw.Variable("cxx", cxxPath)
	g.nw.Variable("ar", arPath)
	g.nw.Variable("aapt2", g.opts.Aapt2Path)
	g.nw.Variable("kotlinc", g.opts.KotlincPath)
	g.nw.Variable("javac", g.opts.JavacPath)
	g.nw.Variable("jar", g.opts.JarPath)
	g.nw.BlankLine()
	return nil
}

func (g *Generator) emitStandardRules() error {
	// CC Compile
	if err := g.nw.Rule(ninja.Rule{
		Name:        "cc_compile",
		Command:     "$cc -MD -MF $out.d $cflags $${CFLAGS} $${CCFLAGS} $includes -c $in -o $out",
		DepFile:     "$out.d",
		Deps:        "gcc",
		Description: "CC $out",
	}); err != nil {
		return err
	}

	// CXX Compile
	if err := g.nw.Rule(ninja.Rule{
		Name:        "cxx_compile",
		Command:     "$cxx -MD -MF $out.d $cflags $${CXXFLAGS} $${CPPFLAGS} $includes -c $in -o $out",
		DepFile:     "$out.d",
		Deps:        "gcc",
		Description: "CXX $out",
	}); err != nil {
		return err
	}

	// Link Shared Library
	if err := g.nw.Rule(ninja.Rule{
		Name:        "link_shared",
		Command:     "$cxx -shared -o $out $in $ldflags $${LDFLAGS} $libs",
		Description: "LINK_SHARED $out",
	}); err != nil {
		return err
	}

	// NASM Assembly (x86 SIMD .asm sources; needs nasm on PATH)
	if err := g.nw.Rule(ninja.Rule{
		Name:        "asm_nasm",
		Command:     "nasm -f elf64 -DELF $asflags -o $out $in",
		Description: "NASM $out",
	}); err != nil {
		return err
	}

	// Link Executable Binary
	if err := g.nw.Rule(ninja.Rule{
		Name:        "link_binary",
		Command:     "$cxx -o $out $in $ldflags $${LDFLAGS} $libs",
		Description: "LINK_BINARY $out",
	}); err != nil {
		return err
	}

	// Java Compile
	if err := g.nw.Rule(ninja.Rule{
		Name:        "javac_compile",
		Command:     "bash -c \"$cmd\"",
		Description: "JAVAC $out",
	}); err != nil {
		return err
	}

	// Kotlin Compile
	if err := g.nw.Rule(ninja.Rule{
		Name:        "kotlinc_compile",
		Command:     "bash -c \"$cmd\"",
		Description: "KOTLINC $out",
	}); err != nil {
		return err
	}

	// Kotlin + Java Compile
	if err := g.nw.Rule(ninja.Rule{
		Name:        "kotlin_java_compile",
		Command:     "bash -c \"$cmd\"",
		Description: "KOTLINC/JAVAC $out",
	}); err != nil {
		return err
	}

	// Archive Static Library
	if err := g.nw.Rule(ninja.Rule{
		Name:        "archive_static",
		Command:     "rm -f $out && $ar rcs $out $in",
		Description: "AR $out",
	}); err != nil {
		return err
	}

	// Copy file
	if err := g.nw.Rule(ninja.Rule{
		Name:        "copy",
		Command:     "cp -f $in $out",
		Description: "COPY $out",
	}); err != nil {
		return err
	}

	// Copy executable binary/script
	if err := g.nw.Rule(ninja.Rule{
		Name:        "copy_executable",
		Command:     "cp -f $in $out && chmod +x $out",
		Description: "COPY_BIN $out",
	}); err != nil {
		return err
	}

	// Python executable binary wrapper
	if err := g.nw.Rule(ninja.Rule{
		Name:        "python_binary",
		Command:     "mkdir -p $$(dirname $out) && printf '#!/bin/sh\\nexport PYTHONPATH=\"$$PWD\":$$PYTHONPATH\\nexec python3 \"$in\" \"$$@\"\\n' > $out && chmod +x $out",
		Description: "PY_BIN $out",
	}); err != nil {
		return err
	}

	// Custom Genrule
	if err := g.nw.Rule(ninja.Rule{
		Name:        "genrule_cmd",
		Command:     "bash -c \"$cmd\"",
		Description: "GEN $out",
	}); err != nil {
		return err
	}

	// Protoc: emit C++ from .proto. Recipe build env pins /tmp/pbc/protoc321
	// via $PROTOC (ABI-matched to the fleet runtime); fall back to system protoc.
	if err := g.nw.Rule(ninja.Rule{
		Name:        "protoc",
		Command:     "mkdir -p $$(dirname $out) && $${PROTOC:-protoc} $protoc_args",
		Description: "PROTOC $in",
	}); err != nil {
		return err
	}

	return nil
}

func (g *Generator) objDir(moduleName string) string {
	return filepath.Join(g.opts.OutDir, "obj", moduleName)
}

func (g *Generator) binDir() string {
	return filepath.Join(g.opts.OutDir, "bin")
}

func (g *Generator) libDir() string {
	return filepath.Join(g.opts.OutDir, "lib64")
}

// resolveCurrentAndroidJar locates the platform SDK android.jar matching APILevel or best available.
func (g *Generator) resolveCurrentAndroidJar() string {
	if g.opts.AndroidJarPath != "" {
		if fi, err := os.Stat(g.opts.AndroidJarPath); err == nil && !fi.IsDir() {
			return g.opts.AndroidJarPath
		}
	}

	targetApi := g.opts.APILevel
	if targetApi <= 0 {
		targetApi = 34
	}

	homeDir, _ := os.UserHomeDir()
	candidates := []string{
		os.Getenv("ANDROID_SDK"),
		os.Getenv("ANDROID_HOME"),
		"/opt/android-sdk",
		filepath.Join(homeDir, "Android/Sdk"),
	}

	// 1. Look for <SDK>/platforms/android-<version>/android.jar
	for _, sdk := range candidates {
		if sdk == "" {
			continue
		}
		// Exact match: platforms/android-<targetApi>/android.jar or platforms/android-<targetApi>.0/android.jar
		exact := filepath.Join(sdk, "platforms", fmt.Sprintf("android-%d", targetApi), "android.jar")
		if fi, err := os.Stat(exact); err == nil && !fi.IsDir() {
			return exact
		}
		exactDot := filepath.Join(sdk, "platforms", fmt.Sprintf("android-%d.0", targetApi), "android.jar")
		if fi, err := os.Stat(exactDot); err == nil && !fi.IsDir() {
			return exactDot
		}

		// Search available platforms
		platformsDir := filepath.Join(sdk, "platforms")
		if entries, err := os.ReadDir(platformsDir); err == nil {
			var bestJar string
			bestDiff := 9999
			for _, e := range entries {
				name := e.Name()
				if !strings.HasPrefix(name, "android-") {
					continue
				}
				jarPath := filepath.Join(platformsDir, name, "android.jar")
				if fi, err := os.Stat(jarPath); err != nil || fi.IsDir() {
					continue
				}
				verStr := strings.TrimPrefix(name, "android-")
				if idx := strings.Index(verStr, "."); idx != -1 {
					verStr = verStr[:idx]
				}
				var verNum int
				if n, err := fmt.Sscanf(verStr, "%d", &verNum); err == nil && n == 1 {
					diff := targetApi - verNum
					if diff < 0 {
						diff = -diff + 100 // Prefer <= targetApi if possible
					}
					if diff < bestDiff {
						bestDiff = diff
						bestJar = jarPath
					}
				} else if bestJar == "" {
					bestJar = jarPath
				}
			}
			if bestJar != "" {
				return bestJar
			}
		}
	}

	// 2. In-tree prebuilts/sdk/current/public/android.jar
	if g.opts.TopDir != "" {
		treeJar := filepath.Join(g.opts.TopDir, "prebuilts/sdk/current/public/android.jar")
		if fi, err := os.Stat(treeJar); err == nil && !fi.IsDir() {
			return treeJar
		}
	}

	// 3. Fallback: <SDK>/android.jar if present
	for _, sdk := range candidates {
		if sdk == "" {
			continue
		}
		p := filepath.Join(sdk, "android.jar")
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}

	return filepath.Join("/opt/android-sdk/platforms", fmt.Sprintf("android-%d", targetApi), "android.jar")
}

// resolveTool locates the executable or output path for a named tool/module.
func (g *Generator) resolveTool(name string) string {
	// 1. Check if tool module outputs were already recorded
	if targets, ok := g.moduleOutputs[name]; ok && len(targets) > 0 {
		return targets[0]
	}

	// 2. Check if the module is in moduleMap
	if mod, ok := g.moduleMap[name]; ok {
		switch mod.Type {
		case "sh_binary", "sh_binary_host":
			src := mod.GetString("src")
			filename := mod.GetString("filename")
			if filename == "" {
				filename = filepath.Base(src)
			}
			return filepath.Join(g.binDir(), filename)
		case "cc_binary", "cc_binary_host":
			return filepath.Join(g.binDir(), mod.Name)
		case "python_binary", "python_binary_host", "python_test_host":
			return filepath.Join(g.binDir(), mod.Name)
		}
	}

	// 3. Check if name is a file in the workspace
	if g.opts.BpDir != "" {
		p := filepath.Join(g.opts.BpDir, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if _, err := os.Stat(name); err == nil {
		return name
	}

	// 4. Default: tool available in PATH or host environment
	return name
}

// ResolveSrcs expands filegroup/genrule/gensrcs references and filters out exclude_srcs.
func (g *Generator) ResolveSrcs(mod *eval.EvaluatedModule) []string {
	rawSrcs := mod.GetStringList("srcs")
	rawSrcs = append(rawSrcs, mod.GetStringList("generated_sources")...)
	if len(rawSrcs) == 0 {
		return nil
	}

	var expanded []string
	for _, s := range rawSrcs {
		if strings.HasPrefix(s, ":") {
			label := strings.TrimPrefix(s, ":")
			if fgSrcs, ok := g.filegroups[label]; ok {
				for _, fg := range fgSrcs {
					expanded = append(expanded, g.expandGlob(fg)...)
				}
			} else if outs, ok := g.moduleOutputs[label]; ok {
				expanded = append(expanded, outs...)
			} else if genMod, ok := g.moduleMap[label]; ok && genMod.Type == "genrule" {
				for _, out := range genMod.GetStringList("out") {
					expanded = append(expanded, filepath.Join(g.opts.OutDir, "gen", label, out))
				}
			} else if label == "current_android_jar" || label == "system_android_jar" {
				expanded = append(expanded, g.resolveCurrentAndroidJar())
			} else {
				expanded = append(expanded, s)
			}
		} else if outs, ok := g.moduleOutputs[s]; ok {
			expanded = append(expanded, outs...)
		} else if s == "current_android_jar" || s == "system_android_jar" {
			expanded = append(expanded, g.resolveCurrentAndroidJar())
		} else {
			srcPath := s
			if mod.Dir != "" && mod.Dir != "." && !filepath.IsAbs(s) {
				srcPath = filepath.Clean(filepath.Join(mod.Dir, s))
			}
			expanded = append(expanded, g.expandGlob(srcPath)...)
		}
	}

	excludeList := mod.GetStringList("exclude_srcs")
	if len(excludeList) > 0 {
		expanded = filterExcludeSrcs(expanded, excludeList, mod.Dir)
	}

	return dedup(expanded)
}

func (g *Generator) expandGlob(pattern string) []string {
	if !strings.ContainsAny(pattern, "*?[") {
		return []string{pattern}
	}

	baseDir := g.opts.BpDir
	if baseDir == "" {
		baseDir = "."
	}

	// Determine static prefix directory to avoid walking unnecessary trees
	staticPrefix := ""
	parts := strings.Split(pattern, "/")
	var globParts []string
	foundGlob := false
	for _, p := range parts {
		if !foundGlob && !strings.ContainsAny(p, "*?[") {
			staticPrefix = filepath.Join(staticPrefix, p)
		} else {
			foundGlob = true
			globParts = append(globParts, p)
		}
	}

	walkRoot := filepath.Join(baseDir, staticPrefix)
	if _, err := os.Stat(walkRoot); err != nil {
		return nil
	}

	// Convert glob pattern to regexp
	regexStr := globToRegex(pattern)
	re, err := regexp.Compile("^" + regexStr + "$")
	if err != nil {
		return nil
	}

	var matches []string
	_ = filepath.Walk(walkRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(baseDir, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if re.MatchString(rel) {
			matches = append(matches, rel)
		}
		return nil
	})

	if len(matches) == 0 {
		return nil
	}
	sort.Strings(matches)
	return matches
}

func globToRegex(glob string) string {
	var sb strings.Builder
	i := 0
	for i < len(glob) {
		c := glob[i]
		switch c {
		case '*':
			if i+1 < len(glob) && glob[i+1] == '*' {
				if i+2 < len(glob) && glob[i+2] == '/' {
					sb.WriteString("(?:.*/)?")
					i += 3
					continue
				}
				sb.WriteString(".*")
				i += 2
				continue
			}
			sb.WriteString("[^/]*")
			i++
		case '?':
			sb.WriteString("[^/]")
			i++
		case '.', '+', '(', ')', '|', '^', '$', '[', ']', '{', '}', '\\':
			sb.WriteByte('\\')
			sb.WriteByte(c)
			i++
		default:
			sb.WriteByte(c)
			i++
		}
	}
	return sb.String()
}

func filterExcludeSrcs(srcs []string, excludeList []string, modDir string) []string {
	if len(excludeList) == 0 || len(srcs) == 0 {
		return srcs
	}
	var filtered []string
	for _, s := range srcs {
		excluded := false
		for _, ex := range excludeList {
			exPath := ex
			if modDir != "" && modDir != "." && !filepath.IsAbs(ex) {
				exPath = filepath.Clean(filepath.Join(modDir, ex))
			}
			if matchesExclude(ex, s) || matchesExclude(exPath, s) {
				excluded = true
				break
			}
		}
		if !excluded {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

func matchesExclude(pattern, target string) bool {
	pattern = strings.TrimPrefix(filepath.ToSlash(filepath.Clean(pattern)), "./")
	target = strings.TrimPrefix(filepath.ToSlash(filepath.Clean(target)), "./")

	if pattern == target {
		return true
	}
	// Check basename match if pattern is a bare filename without slashes
	if !strings.Contains(pattern, "/") && !strings.ContainsAny(pattern, "*?[") {
		if pattern == filepath.Base(target) {
			return true
		}
	}

	// If pattern contains glob wildcards
	if strings.ContainsAny(pattern, "*?[") {
		regexStr := globToRegex(pattern)
		if re, err := regexp.Compile("^" + regexStr + "$"); err == nil {
			if re.MatchString(target) {
				return true
			}
			if !strings.Contains(pattern, "/") && re.MatchString(filepath.Base(target)) {
				return true
			}
		}
	}

	return false
}

