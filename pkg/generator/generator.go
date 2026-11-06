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
	TopDir string
	OutDir string
	// Prefix is the runtime install prefix: link edges gain rpath flags for
	// <prefix>/<libsub> (and a deploy-relative $$ORIGIN form) so linked
	// artifacts find their shared libraries — and libc — once deployed
	// (/system, /apex/<name>...). Build outputs always stay in the build-local
	// out/ layout and the link-time -L search is unchanged. "", "." and "/"
	// select relative mode (no rpath flags).
	Prefix              string
	SysrootDir          string
	PrebuiltLibDir      string
	ClangPath           string
	ClangCxxPath        string
	ArPath              string
	Aapt2Path           string
	AndroidJarPath      string
	KotlincPath         string
	JavacPath           string
	JarPath             string
	TurbinePath         string
	HidlGenPath         string
	TargetArch          string
	TargetTriple        string
	APILevel            int
	NDKDir              string
	AllowMissingDeps    bool
	BpDir               string
	ExtraCflags         []string
	ExtraCppflags       []string
	ExtraLdflags        []string
	ExtraAsflags        []string
	LdPath              string
	StripPath           string
	IncludeRoot         string
	IsHost              bool
	HostCodegenArches   []string
	TargetCodegenArches []string

	// Host tools and code generators
	XsdcPath             string // xsdc CLI binary path
	ConvLinkerConfigPath string // conv_linker_config CLI binary path
	XmllintPath          string // xmllint CLI binary path
	MkbootimgPath        string // mkbootimg CLI binary path
	AvbtoolPath          string // avbtool CLI binary path
	LpmakePath           string // lpmake CLI binary path
	CompatConfigPath     string // process-compat-config CLI binary path
	AidlPath             string // aidl CLI binary path
	MetalavaPath         string // metalava CLI binary path

	// Rust and Cargo options
	Cargo        bool   // Whether to synthesize Cargo.toml manifests and drive build via cargo
	CargoPath    string // cargo CLI binary path
	CargoOutDir  string // Custom output directory for Cargo manifests
	CargoOffline bool   // Pass --offline and CARGO_NET_OFFLINE=true to cargo
	BindgenPath  string // bindgen CLI binary path

	// Platform sysroot and ABI controls
	SkipAbiLevel   int    // Skip base ABI libraries at or below this level
	DisableSkipAbis bool   // Disable skipping base ABI libraries (force APEX resolution)
	JavaEncoding   string // Default Java source encoding (default: UTF-8)

	// Java and Kotlin classpath and prebuilt options
	BootClasspath     []string // Explicit bootclasspath JAR paths (prepended to all classpaths)
	ExtraJars         []string // Explicit extra JAR files or directories to inject
	PlatformClasspath []string // Explicit platform JAR paths applied to modules with no sdk_version
	PrebuiltJarDirs   []string // Directories or files containing prebuilt JARs (e.g. prebuilts/target/*/share/*)

	// Siso emits Siso-specific ninja bindings (restat_content, remote_enabled,
	// sandbox_disabled, ...) so the graph runs under `siso ninja` as a drop-in
	// Ninja replacement. Upstream Ninja rejects those bindings, hence opt-in.
	Siso bool
	// SisoRemote additionally marks pure compile actions remote_enabled for
	// RBE offloading (requires a REAPI backend configured in siso.star).
	SisoRemote bool
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
		LdPath:           "ld.lld",
		Aapt2Path:        "aapt2",
		AndroidJarPath:   "",
		KotlincPath:      "kotlinc",
		JavacPath:        "javac",
		JarPath:          "jar",
		TurbinePath:      "turbine",
		HidlGenPath:          "hidl-gen",
		XsdcPath:             "xsdc",
		ConvLinkerConfigPath: "conv_linker_config",
		XmllintPath:          "xmllint",
		MkbootimgPath:        "mkbootimg",
		AvbtoolPath:          "avbtool",
		LpmakePath:           "lpmake",
		CompatConfigPath:     "process-compat-config",
		AidlPath:             "aidl",
		MetalavaPath:         "metalava",
		CargoPath:            "cargo",
		BindgenPath:          "bindgen",
		JavaEncoding:         "UTF-8",
		AllowMissingDeps:     true,
	}
}

// Generator translates evaluated modules into a Ninja build file.
type Generator struct {
	opts             Options
	nw               *ninja.Writer
	registry         *plugins.Registry
	moduleMap        map[string]*eval.EvaluatedModule
	moduleOutputs    map[string][]string
	taggedOutputs    map[string][]string
	aidlLibraries    map[string]*aidlLibraryInfo
	filegroups       map[string][]string
	emittedPhonies   map[string]bool
	topDirModules    map[string]string
	protoHeaders     map[string][]string
	aconfigHeaders   map[string][]string
	hidlPackageRoots map[string]string
	prebuiltJarIndex map[string]string

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
	if opts.JavaEncoding == "" {
		opts.JavaEncoding = "UTF-8"
	}
	return &Generator{
		opts:             opts,
		nw:               nw,
		registry:         reg,
		moduleMap:        make(map[string]*eval.EvaluatedModule),
		moduleOutputs:    make(map[string][]string),
		taggedOutputs:    make(map[string][]string),
		aidlLibraries:    make(map[string]*aidlLibraryInfo),
		filegroups:       make(map[string][]string),
		emittedPhonies:   make(map[string]bool),
		topDirModules:    nil,
		protoHeaders:     make(map[string][]string),
		aconfigHeaders:   make(map[string][]string),
		hidlPackageRoots: make(map[string]string),
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
	if (top == "" || top == "." || top == "/usr") && g.opts.IncludeRoot != "" {
		top = g.opts.IncludeRoot
	}
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

			// 1. If Android.bp or Android.bp.prebuilt exists in candidate directory, map declared module names
			for _, bpFile := range []string{"Android.bp", "Android.bp.prebuilt"} {
				bpPath := filepath.Join(path, bpFile)
				if data, err := os.ReadFile(bpPath); err == nil {
					for _, match := range nameRegex.FindAllSubmatch(data, -1) {
						mName := string(match[1])
						if existing, ok := g.topDirModules[mName]; ok {
							if fi, err := os.Stat(filepath.Join(existing, "include")); err == nil && fi.IsDir() {
								continue
							}
						}
						g.topDirModules[mName] = path
						if strings.HasSuffix(mName, "_headers") {
							tr := strings.TrimSuffix(mName, "_headers")
							if existing, ok := g.topDirModules[tr]; ok {
								if fi, err := os.Stat(filepath.Join(existing, "include")); err == nil && fi.IsDir() {
									continue
								}
							}
							g.topDirModules[tr] = path
						}
					}
				}
			}

			// 2. Map directory base name only if not already set, and only if candidate has an include directory
			if _, ok := g.topDirModules[baseName]; !ok {
				if fi, err := os.Stat(filepath.Join(path, "include")); err == nil && fi.IsDir() {
					g.topDirModules[baseName] = path
					if strings.HasSuffix(baseName, "_headers") {
						tr := strings.TrimSuffix(baseName, "_headers")
						if _, ok2 := g.topDirModules[tr]; !ok2 {
							g.topDirModules[tr] = path
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

func parseTaggedLabel(label string) (string, string) {
	if idx := strings.Index(label, "{"); idx != -1 && strings.HasSuffix(label, "}") {
		return label[:idx], label[idx+1 : len(label)-1]
	}
	return label, ""
}

func (g *Generator) resolveReference(ref string) []string {
	return g.resolveReferenceWithVisited(ref, make(map[string]bool))
}

func (g *Generator) resolveReferenceWithVisited(ref string, visited map[string]bool) []string {
	if ref == "current_android_jar" || ref == "system_android_jar" {
		jar := g.resolveCurrentAndroidJar()
		if jar != "" {
			return []string{jar}
		}
	}
	if strings.HasPrefix(ref, ":") {
		label := strings.TrimPrefix(ref, ":")
		baseLabel, tag := parseTaggedLabel(label)
		if fg, ok := g.filegroups[baseLabel]; ok && len(fg) > 0 {
			if visited[baseLabel] {
				return nil
			}
			visited[baseLabel] = true
			defer func() { visited[baseLabel] = false }()

			var expanded []string
			for _, item := range fg {
				if strings.HasPrefix(item, ":") || strings.HasPrefix(item, "//") {
					expanded = append(expanded, g.resolveReferenceWithVisited(item, visited)...)
				} else {
					expanded = append(expanded, item)
				}
			}
			return expanded
		}
		if tag != "" {
			if tagged, ok := g.taggedOutputs[baseLabel+":"+tag]; ok && len(tagged) > 0 {
				return tagged
			}
		}
		if outs, ok := g.moduleOutputs[baseLabel]; ok && len(outs) > 0 {
			if tag != "" {
				var matched []string
				for _, out := range outs {
					if filepath.Base(out) == tag || strings.HasSuffix(out, tag) {
						matched = append(matched, out)
					}
				}
				if len(matched) > 0 {
					return matched
				}
			}
			return outs
		}
		if targetMod, ok := g.moduleMap[baseLabel]; ok {
			if targetMod.Type == "aconfig_declarations_group" {
				outs, _ := g.generateAconfigDeclarationsGroup(targetMod)
				if tag != "" {
					if tagged, ok := g.taggedOutputs[baseLabel+":"+tag]; ok && len(tagged) > 0 {
						return tagged
					}
				}
				return outs
			}
			if targetMod.Type == "java_aconfig_library" {
				outs, _ := g.generateJavaAconfigLibrary(targetMod)
				if tag != "" {
					if tagged, ok := g.taggedOutputs[baseLabel+":"+tag]; ok && len(tagged) > 0 {
						return tagged
					}
				}
				return outs
			}
		}
		if genMod, ok := g.moduleMap[baseLabel]; ok && (genMod.Type == "genrule" || genMod.Type == "java_genrule" || genMod.Type == "gensrcs") {
			var genOuts []string
			for _, out := range genMod.GetStringList("out") {
				fullOut := filepath.Join(g.opts.OutDir, "gen", baseLabel, out)
				if tag == "" || filepath.Base(out) == tag || strings.HasSuffix(out, tag) {
					genOuts = append(genOuts, fullOut)
				}
			}
			if len(genOuts) == 0 {
				for _, out := range genMod.GetStringList("out") {
					genOuts = append(genOuts, filepath.Join(g.opts.OutDir, "gen", baseLabel, out))
				}
			}
			return genOuts
		}
		if baseLabel == "current_android_jar" || baseLabel == "system_android_jar" {
			jar := g.resolveCurrentAndroidJar()
			if jar != "" {
				return []string{jar}
			}
		}
		if g.opts.AllowMissingDeps {
			g.emitPhonyIfNeeded(baseLabel)
			return []string{baseLabel}
		}
	} else if strings.HasPrefix(ref, "//") {
		parts := strings.Split(ref, ":")
		if len(parts) == 2 {
			label := parts[1]
			baseLabel, tag := parseTaggedLabel(label)
			if fg, ok := g.filegroups[baseLabel]; ok && len(fg) > 0 {
				if visited[baseLabel] {
					return nil
				}
				visited[baseLabel] = true
				defer func() { visited[baseLabel] = false }()

				var expanded []string
				for _, item := range fg {
					if strings.HasPrefix(item, ":") || strings.HasPrefix(item, "//") {
						expanded = append(expanded, g.resolveReferenceWithVisited(item, visited)...)
					} else {
						expanded = append(expanded, item)
					}
				}
				return expanded
			}
			if outs, ok := g.moduleOutputs[baseLabel]; ok && len(outs) > 0 {
				if tag != "" {
					var matched []string
					for _, out := range outs {
						if filepath.Base(out) == tag || strings.HasSuffix(out, tag) {
							matched = append(matched, out)
						}
					}
					if len(matched) > 0 {
						return matched
					}
				}
				return outs
			}
			if genMod, ok := g.moduleMap[baseLabel]; ok && (genMod.Type == "genrule" || genMod.Type == "java_genrule" || genMod.Type == "gensrcs") {
				var genOuts []string
				for _, out := range genMod.GetStringList("out") {
					fullOut := filepath.Join(g.opts.OutDir, "gen", baseLabel, out)
					if tag == "" || filepath.Base(out) == tag || strings.HasSuffix(out, tag) {
						genOuts = append(genOuts, fullOut)
					}
				}
				if len(genOuts) == 0 {
					for _, out := range genMod.GetStringList("out") {
						genOuts = append(genOuts, filepath.Join(g.opts.OutDir, "gen", baseLabel, out))
					}
				}
				return genOuts
			}
			if baseLabel == "current_android_jar" || baseLabel == "system_android_jar" {
				jar := g.resolveCurrentAndroidJar()
				if jar != "" {
					return []string{jar}
				}
			}
			if g.opts.AllowMissingDeps {
				g.emitPhonyIfNeeded(baseLabel)
				return []string{baseLabel}
			}
		}
		if g.opts.AllowMissingDeps {
			cleanRef := strings.TrimPrefix(ref, "//")
			g.emitPhonyIfNeeded(cleanRef)
			return []string{cleanRef}
		}
	}
	return []string{ref}
}

// Generate emits global variables, standard rules, and module build edges.
func (g *Generator) Generate(modules []*eval.EvaluatedModule) error {
	if g.opts.Siso {
		g.nw.EnableSiso()
	}
	if err := g.emitHeader(); err != nil {
		return err
	}
	if err := g.emitStandardRules(); err != nil {
		return err
	}

	modules = g.expandHidlInterfaces(modules)

	// Sort modules by internal dependencies and report missing/circular dependencies
	modules = g.SortModules(modules)

	// Invariant: Prebuilts with `prefer: true` supersede source modules of the same name.
	preferred := make(map[string]*eval.EvaluatedModule)
	for _, mod := range modules {
		if mod.Name != "" && mod.GetBool("prefer") {
			preferred[mod.Name] = mod
		}
	}
	if len(preferred) > 0 {
		for _, mod := range modules {
			if pref, ok := preferred[mod.Name]; ok && pref != mod {
				mod.Properties["enabled"] = false
			}
		}
	}

	// Index all modules and filegroups by name
	for _, mod := range modules {
		if mod.Name != "" {
			if pref, ok := preferred[mod.Name]; ok {
				g.moduleMap[mod.Name] = pref
			} else {
				g.moduleMap[mod.Name] = mod
			}
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
			// Pre-index srcjar outputs for android_app_import or android_app
			if mod.Type == "android_app_import" || mod.Type == "android_app" {
				if srcjar := mod.GetString("srcjar"); srcjar != "" {
					srcjarPath := srcjar
					if mod.Dir != "" && mod.Dir != "." && !filepath.IsAbs(srcjarPath) {
						srcjarPath = filepath.Clean(filepath.Join(mod.Dir, srcjarPath))
					}
					g.taggedOutputs[mod.Name+":.aapt.srcjar"] = []string{srcjarPath}
					g.taggedOutputs[mod.Name+":.srcjar"] = []string{srcjarPath}
				} else if mod.Type == "android_app" {
					srcjarPath := filepath.Join(g.opts.OutDir, "gen", mod.Name, mod.Name+".srcjar")
					g.taggedOutputs[mod.Name+":.aapt.srcjar"] = []string{srcjarPath}
					g.taggedOutputs[mod.Name+":.srcjar"] = []string{srcjarPath}
				}
			}
			// Pre-index sub-module targets for java_sdk_library
			if mod.Type == "java_sdk_library" {
				g.moduleMap[mod.Name+".impl"] = mod
				g.moduleMap[mod.Name+".stubs"] = mod
				g.moduleMap[mod.Name+".stubs.system"] = mod
				g.moduleMap[mod.Name+".stubs.module_lib"] = mod
				g.moduleMap[mod.Name+".stubs.test"] = mod
			}
		}
	}

	// Pre-index proto outputs once all modules are indexed in moduleMap
	for _, mod := range modules {
		if mod.Name == "" {
			continue
		}
		hasProto := false
		for _, s := range mod.GetStringList("srcs") {
			if strings.Contains(s, ".proto") {
				hasProto = true
				break
			}
		}
		if hasProto {
			for _, src := range g.ResolveSrcs(mod) {
				if strings.HasSuffix(src, ".proto") {
					pbRel := strings.TrimSuffix(src, ".proto")
					pbHdr := filepath.Join(g.opts.OutDir, "gen", mod.Name, pbRel+".pb.h")
					g.protoHeaders[mod.Name] = append(g.protoHeaders[mod.Name], pbHdr)
				}
			}
		}
	}

	var allTargets []string
	var rootTargets []string
	var anyRootTargets []string

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
			if mod.Dir == "" || mod.Dir == "." {
				anyRootTargets = append(anyRootTargets, targets...)
			}
			if !strings.Contains(mod.Type, "test") && !strings.Contains(mod.Type, "benchmark") {
				allTargets = append(allTargets, targets...)
				if mod.Dir == "" || mod.Dir == "." {
					rootTargets = append(rootTargets, targets...)
				}
			}
			continue
		}

		// Standard module generators
		if strings.HasSuffix(mod.Type, "_defaults") || strings.HasSuffix(mod.Type, "_default") || mod.Type == "defaults" {
			continue
		}

		var targets []string
		var err error

		switch mod.Type {
		case "package", "license", "license_kind", "package_metadata",
			"soong_namespace", "soong_config_module_type", "soong_config_string_variable",
			"soong_config_bool_variable", "soong_config_module_type_import",
			"filegroup", "phony", "ndk_headers", "vintf_fragment",
			"cc_defaults", "java_defaults", "art_cc_defaults", "art_global_defaults",
			"hidl_package_root":
			// Declarative meta-modules in generic BP: no direct build action
			continue
		case "install_symlink":
			targets, err = g.generateInstallSymlink(mod)
		case "android_filesystem", "filesystem":
			targets, err = g.generateFilesystem(mod)
		case "cc_object":
			targets, err = g.generateCcObject(mod)
		case "bpf":
			targets, err = g.generateBpf(mod)
		case "sysprop_library":
			targets, err = g.generateSyspropLibrary(mod)
		case "ndk_library":
			targets, err = g.generateNdkLibrary(mod)
		case "hidl_gen_sources":
			targets, err = g.generateHidlGenSources(mod)
		case "hidl_gen_headers":
			targets, err = g.generateHidlGenHeaders(mod)
		case "hidl_java_library":
			targets, err = g.generateHidlJavaLibrary(mod)
		case "hidl_java_constants":
			targets, err = g.generateHidlJavaConstants(mod)
		case "hidl_interface":
			targets, err = g.generateHidlInterface(mod)
		case "cc_binary", "cc_binary_host", "art_cc_binary":
			if mod.IsHostOnly() {
				if prebuilt := g.findPrebuiltHostTool(mod.Name); prebuilt != "" {
					g.moduleOutputs[mod.Name] = []string{prebuilt}
					continue
				}
			}
			targets, err = g.generateCcBinary(mod)
		case "cc_library", "cc_library_shared", "cc_library_static", "cc_library_headers",
			"cc_library_host", "cc_library_host_shared", "cc_library_host_static",
			"art_cc_library", "art_cc_library_static":
			targets, err = g.generateCcLibrary(mod)
		case "cc_test", "cc_benchmark", "cc_test_host", "cc_fuzz", "art_cc_test", "art_cc_test_library":
			targets, err = g.generateCcTest(mod)
		case "android_app":
			targets, err = g.generateAndroidApp(mod)
		case "android_app_import":
			targets, err = g.generateAndroidAppImport(mod)
		case "android_app_certificate":
			// Certificate declarations define signing key/cert pairs, not a runnable APK
			targets = []string{}
		case "java_library", "java_library_host", "android_library", "java_library_static":
			targets, err = g.generateJavaLibrary(mod)
		case "java_sdk_library":
			targets, err = g.generateJavaSdkLibrary(mod)
		case "java_import", "android_library_import":
			targets, err = g.generateJavaImport(mod)
		case "java_sdk_library_import":
			targets, err = g.generateJavaSdkLibraryImport(mod)
		case "java_binary", "java_binary_host":
			if mod.IsHostOnly() {
				if prebuilt := g.findPrebuiltHostTool(mod.Name); prebuilt != "" {
					g.moduleOutputs[mod.Name] = []string{prebuilt}
					continue
				}
			}
			targets, err = g.generateJavaBinary(mod)
		case "genrule":
			targets, err = g.generateGenrule(mod)
		case "gensrcs":
			targets, err = g.generateGensrcs(mod)
		case "prebuilt_etc", "prebuilt_etc_host", "sh_binary", "sh_binary_host", "cc_prebuilt_binary":
			targets, err = g.generatePrebuilt(mod)
		case "python_binary", "python_binary_host", "python_test_host":
			targets, err = g.generatePythonBinary(mod)
		case "java_aconfig_library":
			targets, err = g.generateJavaAconfigLibrary(mod)
		case "aconfig_declarations_group":
			targets, err = g.generateAconfigDeclarationsGroup(mod)
		case "cc_aconfig_library":
			targets, err = g.generateCcAconfigLibrary(mod)
		case "xsd_config":
			targets, err = g.generateXsdConfig(mod)
		case "linker_config":
			targets, err = g.generateLinkerConfig(mod)
		case "prebuilt_etc_xml":
			targets, err = g.generatePrebuiltEtcXml(mod)
		case "aidl_library":
			targets, err = g.generateAidlLibrary(mod)
		case "bootimg":
			targets, err = g.generateBootimg(mod)
		case "vbmeta", "avb_gen_vbmeta_image":
			targets, err = g.generateVbmeta(mod)
		case "logical_partition":
			targets, err = g.generateLogicalPartition(mod)
		case "platform_compat_config":
			targets, err = g.generatePlatformCompatConfig(mod)
		case "java_system_modules":
			targets, err = g.generateJavaSystemModules(mod)
		case "rust_ffi", "rust_ffi_shared", "rust_ffi_static", "rust_ffi_host", "rust_ffi_host_shared", "rust_ffi_host_static",
			"rust_library", "rust_library_host", "rust_library_rlib", "rust_library_dylib", "rust_library_host_rlib", "rust_library_host_dylib",
			"rust_binary", "rust_binary_host", "rust_proc_macro":
			targets, err = g.generateRustModule(mod)
		case "rust_bindgen", "rust_bindgen_host":
			targets, err = g.generateRustBindgen(mod)
		case "aidl_interface":
			targets, err = g.generateAidlInterface(mod)
		case "droidstubs", "droidstubs_host", "droiddoc", "droiddoc_host":
			targets, err = g.generateDroidstubs(mod)
		default:
			// Fallback only: Pattern recognition heuristic when no plugin or handler exists
			targets, err = g.autoDeriveFallback(mod)
		}

		if err != nil {
			return fmt.Errorf("failed generating module %s: %w", mod.Name, err)
		}
		g.moduleOutputs[mod.Name] = targets
		if mod.Dir == "" || mod.Dir == "." {
			anyRootTargets = append(anyRootTargets, targets...)
		}
		if !strings.Contains(mod.Type, "test") && !strings.Contains(mod.Type, "benchmark") {
			allTargets = append(allTargets, targets...)
			if mod.Dir == "" || mod.Dir == "." {
				rootTargets = append(rootTargets, targets...)
			}
		}
	}

	defaultTargets := rootTargets
	if len(defaultTargets) == 0 && len(anyRootTargets) > 0 {
		defaultTargets = anyRootTargets
	}
	if len(defaultTargets) == 0 && len(allTargets) <= 50 {
		defaultTargets = allTargets
	}

	var cleanDefaultTargets []string
	for _, dt := range defaultTargets {
		if strings.HasPrefix(dt, g.opts.OutDir) {
			cleanDefaultTargets = append(cleanDefaultTargets, dt)
		}
	}

	if len(cleanDefaultTargets) > 0 {
		g.nw.BlankLine()
		g.nw.Default(cleanDefaultTargets...)
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
	ldPath := g.opts.LdPath
	if ldPath == "" {
		ldPath = "ld.lld"
	}
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
		if strings.HasPrefix(ldPath, g.opts.NDKDir) {
			ldPath = "$ndk" + strings.TrimPrefix(ldPath, g.opts.NDKDir)
		}
	}
	if sysrootVal != "" {
		g.nw.Variable("sysroot", sysrootVal)
	}
	g.nw.Variable("cc", ccPath)
	g.nw.Variable("cxx", cxxPath)
	g.nw.Variable("ar", arPath)
	g.nw.Variable("ld", ldPath)
	stripPath := g.opts.StripPath
	if stripPath == "" {
		stripPath = "llvm-strip"
	}
	if g.opts.NDKDir != "" && strings.HasPrefix(stripPath, g.opts.NDKDir) {
		stripPath = "$ndk" + strings.TrimPrefix(stripPath, g.opts.NDKDir)
	}
	g.nw.Variable("strip", stripPath)
	g.nw.Variable("aapt2", g.opts.Aapt2Path)
	g.nw.Variable("kotlinc", g.opts.KotlincPath)
	g.nw.Variable("javac", g.opts.JavacPath)
	g.nw.Variable("jar", g.opts.JarPath)
	turbinePath := g.opts.TurbinePath
	if turbinePath == "" {
		turbinePath = g.findPrebuiltHostTool("turbine")
	}
	if turbinePath == "" {
		turbinePath = "turbine"
	}
	g.nw.Variable("turbine", turbinePath)
	hidlGenPath := g.opts.HidlGenPath
	if hidlGenPath == "" {
		hidlGenPath = "hidl-gen"
	}
	g.nw.Variable("hidl_gen", hidlGenPath)

	xsdcPath := g.opts.XsdcPath
	if xsdcPath == "" {
		xsdcPath = "xsdc"
	}
	g.nw.Variable("xsdc", xsdcPath)

	convLinkerConfigPath := g.opts.ConvLinkerConfigPath
	if convLinkerConfigPath == "" {
		convLinkerConfigPath = "conv_linker_config"
	}
	g.nw.Variable("conv_linker_config", convLinkerConfigPath)

	xmllintPath := g.opts.XmllintPath
	if xmllintPath == "" {
		xmllintPath = "xmllint"
	}
	g.nw.Variable("xmllint", xmllintPath)

	mkbootimgPath := g.opts.MkbootimgPath
	if mkbootimgPath == "" {
		mkbootimgPath = "mkbootimg"
	}
	g.nw.Variable("mkbootimg", mkbootimgPath)

	avbtoolPath := g.opts.AvbtoolPath
	if avbtoolPath == "" {
		avbtoolPath = "avbtool"
	}
	g.nw.Variable("avbtool", avbtoolPath)

	lpmakePath := g.opts.LpmakePath
	if lpmakePath == "" {
		lpmakePath = "lpmake"
	}
	g.nw.Variable("lpmake", lpmakePath)

	compatConfigPath := g.opts.CompatConfigPath
	if compatConfigPath == "" {
		compatConfigPath = "process-compat-config"
	}
	g.nw.Variable("process_compat_config", compatConfigPath)

	cargoPath := g.opts.CargoPath
	if cargoPath == "" {
		cargoPath = "cargo"
	}
	g.nw.Variable("cargo", cargoPath)

	bindgenPath := g.opts.BindgenPath
	if bindgenPath == "" {
		bindgenPath = "bindgen"
	}
	g.nw.Variable("bindgen", bindgenPath)

	aidlPath := g.opts.AidlPath
	if aidlPath == "" {
		aidlPath = "aidl"
	}
	g.nw.Variable("aidl", aidlPath)

	metalavaPath := g.opts.MetalavaPath
	if metalavaPath == "" {
		metalavaPath = "metalava"
	}
	g.nw.Variable("metalava", metalavaPath)
	g.nw.BlankLine()
	return nil
}

func (g *Generator) emitStandardRules() error {
	// CC Compile
	if err := g.nw.Rule(ninja.Rule{
		Name:          "cc_compile",
		Command:       "$cc -MD -MF $out.d $includes $cflags $${CFLAGS} $${CCFLAGS} -c $in -o $out",
		DepFile:       "$out.d",
		Deps:          "gcc",
		Description:   "CC $out",
		RemoteEnabled: g.opts.SisoRemote,
	}); err != nil {
		return err
	}

	// AS Compile (Assembly pipeline using asflags without C headers)
	if err := g.nw.Rule(ninja.Rule{
		Name:          "as_compile",
		Command:       "$cc -MD -MF $out.d $includes $asflags $${ASFLAGS} -c $in -o $out",
		DepFile:       "$out.d",
		Deps:          "gcc",
		Description:   "AS $out",
		RemoteEnabled: g.opts.SisoRemote,
	}); err != nil {
		return err
	}

	// Partial Link (Combine object files using ld.lld -r)
	if err := g.nw.Rule(ninja.Rule{
		Name:        "partial_link",
		Command:     "$ld -r -o $out $in $ldflags $${LDFLAGS}",
		Description: "PARTIAL_LINK $out",
	}); err != nil {
		return err
	}

	// Archive Static Library via MRI script (Composite archives)
	if err := g.nw.Rule(ninja.Rule{
		Name:            "ar_mri",
		Command:         "bash -c \"$cmd\"",
		Description:     "AR_MRI $out",
		RestatContent:   g.opts.Siso,
		SandboxDisabled: g.opts.Siso,
	}); err != nil {
		return err
	}

	// Generate Linker Version Script
	if err := g.nw.Rule(ninja.Rule{
		Name:            "gen_version_script",
		Command:         "bash -c \"$cmd\"",
		Description:     "VERSION_SCRIPT $out",
		RestatContent:   g.opts.Siso,
		SandboxDisabled: g.opts.Siso,
	}); err != nil {
		return err
	}

	// CXX Compile
	if err := g.nw.Rule(ninja.Rule{
		Name:          "cxx_compile",
		Command:       "$cxx -MD -MF $out.d $includes $cflags $${CXXFLAGS} $${CPPFLAGS} -c $in -o $out",
		DepFile:       "$out.d",
		Deps:          "gcc",
		Description:   "CXX $out",
		RemoteEnabled: g.opts.SisoRemote,
	}); err != nil {
		return err
	}

	// Link Shared Library
	if err := g.nw.Rule(ninja.Rule{
		Name:        "link_shared",
		Command:     "$cxx -shared -Wl,-soname,$soname -o $out $in $ldflags $${LDFLAGS} $libs",
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
		Name:            "javac_compile",
		Command:         "$cmd",
		Description:     "JAVAC $out",
		RspFile:         "${out}.rsp",
		RspContent:      "$rspcontent",
		SandboxDisabled: g.opts.Siso,
	}); err != nil {
		return err
	}

	// Kotlin Compile
	if err := g.nw.Rule(ninja.Rule{
		Name:            "kotlinc_compile",
		Command:         "$cmd",
		Description:     "KOTLINC $out",
		RspFile:         "${out}.rsp",
		RspContent:      "$rspcontent",
		SandboxDisabled: g.opts.Siso,
	}); err != nil {
		return err
	}

	// Kotlin + Java Compile
	if err := g.nw.Rule(ninja.Rule{
		Name:            "kotlin_java_compile",
		Command:         "$cmd",
		Description:     "KOTLINC/JAVAC $out",
		RspFile:         "${out}.rsp",
		RspContent:      "$rspcontent",
		SandboxDisabled: g.opts.Siso,
	}); err != nil {
		return err
	}

	// Turbine Compile (header-only compilation)
	if err := g.nw.Rule(ninja.Rule{
		Name:            "turbine_compile",
		Command:         "$cmd",
		Description:     "TURBINE $out",
		RspFile:         "${out}.rsp",
		RspContent:      "$rspcontent",
		SandboxDisabled: g.opts.Siso,
	}); err != nil {
		return err
	}

	// Logtags Compile
	if err := g.nw.Rule(ninja.Rule{
		Name:            "logtags_compile",
		Command:         "bash -c 'mkdir -p $$(dirname $out) && python3 $top/build/make/tools/java-event-log-tags.py -o $out $in'",
		Description:     "LOGTAGS $out",
		SandboxDisabled: g.opts.Siso,
	}); err != nil {
		return err
	}

	// Archive Static Library
	if err := g.nw.Rule(ninja.Rule{
		Name:          "archive_static",
		Command:       "rm -f $out && $ar rcs $out $in",
		Description:   "AR $out",
		RestatContent: g.opts.Siso,
	}); err != nil {
		return err
	}

	// Copy file
	if err := g.nw.Rule(ninja.Rule{
		Name:          "copy",
		Command:       "cp -f $in $out",
		Description:   "COPY $out",
		RestatContent: g.opts.Siso,
	}); err != nil {
		return err
	}

	// Copy executable binary/script
	if err := g.nw.Rule(ninja.Rule{
		Name:          "copy_executable",
		Command:       "cp -f $in $out && chmod +x $out",
		Description:   "COPY_BIN $out",
		RestatContent: g.opts.Siso,
	}); err != nil {
		return err
	}

	// Python executable binary wrapper
	if err := g.nw.Rule(ninja.Rule{
		Name:            "python_binary",
		Command:         "mkdir -p $$(dirname $out) && printf '#!/bin/sh\\nexport PYTHONPATH=\"$$PWD\":$$PYTHONPATH\\nexec python3 \"$in\" \"$$@\"\\n' > $out && chmod +x $out",
		Description:     "PY_BIN $out",
		RestatContent:   g.opts.Siso,
		SandboxDisabled: g.opts.Siso,
	}); err != nil {
		return err
	}

	// Custom Genrule
	if err := g.nw.Rule(ninja.Rule{
		Name:            "genrule_cmd",
		Command:         "$cmd",
		Description:     "GEN $out",
		RestatContent:   g.opts.Siso,
		SandboxDisabled: g.opts.Siso,
	}); err != nil {
		return err
	}

	// Protoc: emit C++ from .proto. Recipe build env pins /tmp/pbc/protoc321
	// via $PROTOC (ABI-matched to the fleet runtime); fall back to authentic prebuilt or system protoc.
	protocCmd := "$${PROTOC:-protoc}"
	if g.opts.TopDir != "" {
		aprotocCand := filepath.Join(g.opts.TopDir, "prebuilts/external/protobuf/host/bin/aprotoc")
		if fi, err := os.Stat(aprotocCand); err == nil && !fi.IsDir() {
			protocCmd = fmt.Sprintf("$${PROTOC:-%s}", aprotocCand)
		}
	}
	if err := g.nw.Rule(ninja.Rule{
		Name:          "protoc",
		Command:       fmt.Sprintf("mkdir -p $$(dirname $out) && %s $protoc_args", protocCmd),
		Description:   "PROTOC $in",
		RestatContent: g.opts.Siso,
	}); err != nil {
		return err
	}

	// Symlink
	if err := g.nw.Rule(ninja.Rule{
		Name:          "symlink",
		Command:       "mkdir -p $$(dirname $out) && rm -f $out && ln -sf $symlink_target $out",
		Description:   "SYMLINK $out",
		RestatContent: g.opts.Siso,
	}); err != nil {
		return err
	}

	// BPF program compile (upstream `bpf` module: clang --target=bpf)
	if err := g.nw.Rule(ninja.Rule{
		Name:          "bpf_compile",
		Command:       "$cc --target=bpf -MD -MF $out.d $cflags -c $in -o $out",
		DepFile:       "$out.d",
		Deps:          "gcc",
		Description:   "BPF $out",
		RestatContent: g.opts.Siso,
	}); err != nil {
		return err
	}

	// BPF strip: remove BTF relocation sections like upstream llvm-strip rule
	if err := g.nw.Rule(ninja.Rule{
		Name:          "bpf_strip",
		Command:       "$strip --strip-unneeded --remove-section=.rel.BTF --remove-section=.rel.BTF.ext --remove-section=.BTF.ext $in -o $out",
		Description:   "BPF_STRIP $out",
		RestatContent: g.opts.Siso,
	}); err != nil {
		return err
	}

	// Sysprop C++ codegen (platform sysprop tool; pin via $SYSPROP_CLI)
	syspropCppCmd := "$${SYSPROP_CLI:-sysprop-cli} --cpp"
	if hostSyspropCpp := g.findPrebuiltHostTool("sysprop_cpp"); hostSyspropCpp != "" {
		syspropCppCmd = hostSyspropCpp
	}
	if err := g.nw.Rule(ninja.Rule{
		Name:            "sysprop_cpp",
		Command:         "mkdir -p $$(dirname $out) && " + syspropCppCmd + " $in $$(dirname $out)",
		Description:     "SYSPROP $in",
		RestatContent:   g.opts.Siso,
		SandboxDisabled: g.opts.Siso,
	}); err != nil {
		return err
	}

	// Sysprop Java library codegen + compilation
	if err := g.nw.Rule(ninja.Rule{
		Name:            "sysprop_library_java",
		Command:         "$cmd",
		Description:     "SYSPROP_JAVA $out",
		RestatContent:   g.opts.Siso,
		SandboxDisabled: g.opts.Siso,
	}); err != nil {
		return err
	}

	// HIDL C++ sources codegen
	if err := g.nw.Rule(ninja.Rule{
		Name:            "hidl_cpp_sources",
		Command:         "mkdir -p $gen_dir && $${HIDL_GEN:-$hidl_gen} -R -p . -o $gen_dir -L c++-sources $options $fq_name",
		Description:     "HIDL_CPP_SRCS $fq_name",
		RestatContent:   g.opts.Siso,
		SandboxDisabled: g.opts.Siso,
	}); err != nil {
		return err
	}

	// HIDL C++ headers codegen
	if err := g.nw.Rule(ninja.Rule{
		Name:            "hidl_cpp_headers",
		Command:         "mkdir -p $gen_dir && $${HIDL_GEN:-$hidl_gen} -R -p . -o $gen_dir -L c++-headers $options $fq_name",
		Description:     "HIDL_CPP_HDRS $fq_name",
		RestatContent:   g.opts.Siso,
		SandboxDisabled: g.opts.Siso,
	}); err != nil {
		return err
	}

	// HIDL Java interface codegen and JAR packaging
	if err := g.nw.Rule(ninja.Rule{
		Name:            "hidl_java",
		Command:         "bash -c \"$cmd\"",
		Description:     "HIDL_JAVA $out",
		RestatContent:   g.opts.Siso,
		SandboxDisabled: g.opts.Siso,
	}); err != nil {
		return err
	}

	// HIDL Java constants codegen and JAR packaging
	if err := g.nw.Rule(ninja.Rule{
		Name:            "hidl_java_constants",
		Command:         "bash -c \"$cmd\"",
		Description:     "HIDL_JAVA_CONSTANTS $out",
		RestatContent:   g.opts.Siso,
		SandboxDisabled: g.opts.Siso,
	}); err != nil {
		return err
	}

	// XSDC C++ codegen
	if err := g.nw.Rule(ninja.Rule{
		Name:            "xsdc_cpp",
		Command:         "mkdir -p $out_dir && $xsdc $in -p $package_name -o $out_dir -c $args",
		Description:     "XSDC_CPP $out",
		RestatContent:   g.opts.Siso,
		SandboxDisabled: g.opts.Siso,
	}); err != nil {
		return err
	}

	// XSDC Java codegen and srcjar packaging
	if err := g.nw.Rule(ninja.Rule{
		Name:            "xsdc_java",
		Command:         "rm -rf $out_dir && mkdir -p $out_dir && $xsdc $in -p $package_name -o $out_dir -j $args && $jar cf $out -C $out_dir .",
		Description:     "XSDC_JAVA $out",
		RestatContent:   g.opts.Siso,
		SandboxDisabled: g.opts.Siso,
	}); err != nil {
		return err
	}

	// XSD schema copy
	if err := g.nw.Rule(ninja.Rule{
		Name:          "xsd_copy",
		Command:       "cp -f $in $out",
		Description:   "COPY_XSD $out",
		RestatContent: g.opts.Siso,
	}); err != nil {
		return err
	}

	// Linker config binary protobuf compiler
	if err := g.nw.Rule(ninja.Rule{
		Name:          "conv_linker_config",
		Command:       "$conv_linker_config proto -s $in -o $out",
		Description:   "CONV_LINKER_CONFIG $out",
		RestatContent: g.opts.Siso,
	}); err != nil {
		return err
	}

	// XML schema validation (XSD)
	if err := g.nw.Rule(ninja.Rule{
		Name:          "xmllint_xsd",
		Command:       "$xmllint --schema $schema $in > /dev/null && cp -f $in $out",
		Description:   "XMLLINT_XSD $out",
		RestatContent: g.opts.Siso,
	}); err != nil {
		return err
	}

	// XML DTD validation
	if err := g.nw.Rule(ninja.Rule{
		Name:          "xmllint_dtd",
		Command:       "$xmllint --dtdvalid $dtd $in > /dev/null && cp -f $in $out",
		Description:   "XMLLINT_DTD $out",
		RestatContent: g.opts.Siso,
	}); err != nil {
		return err
	}

	// Boot image generation (mkbootimg)
	if err := g.nw.Rule(ninja.Rule{
		Name:            "mkbootimg",
		Command:         "$mkbootimg $args -o $out",
		Description:     "MKBOOTIMG $out",
		RestatContent:   g.opts.Siso,
		SandboxDisabled: g.opts.Siso,
	}); err != nil {
		return err
	}

	// Android Verified Boot vbmeta image (avbtool)
	if err := g.nw.Rule(ninja.Rule{
		Name:            "avbtool_vbmeta",
		Command:         "$avbtool make_vbmeta_image $args --output $out",
		Description:     "AVBTOOL_VBMETA $out",
		RestatContent:   g.opts.Siso,
		SandboxDisabled: g.opts.Siso,
	}); err != nil {
		return err
	}

	// Dynamic partition super image (lpmake)
	if err := g.nw.Rule(ninja.Rule{
		Name:            "lpmake",
		Command:         "$lpmake $args --output $out",
		Description:     "LPMAKE $out",
		RestatContent:   g.opts.Siso,
		SandboxDisabled: g.opts.Siso,
	}); err != nil {
		return err
	}

	// Platform compatibility config
	if err := g.nw.Rule(ninja.Rule{
		Name:          "process_compat_config",
		Command:       "$process_compat_config $args -o $out",
		Description:   "PROCESS_COMPAT_CONFIG $out",
		RestatContent: g.opts.Siso,
	}); err != nil {
		return err
	}

	// Java system modules image generation (jlink)
	if err := g.nw.Rule(ninja.Rule{
		Name:            "system_modules_jlink",
		Command:         "rm -rf $out && jlink --module-path $module_path --add-modules $add_modules --output $out",
		Description:     "JLINK $out",
		RestatContent:   g.opts.Siso,
		SandboxDisabled: g.opts.Siso,
	}); err != nil {
		return err
	}

	// Rust / Cargo compilation
	if err := g.nw.Rule(ninja.Rule{
		Name:            "cargo_build",
		Command:         "bash -c \"$cmd\"",
		Description:     "CARGO_BUILD $out",
		SandboxDisabled: g.opts.Siso,
	}); err != nil {
		return err
	}

	// Rust bindgen
	if err := g.nw.Rule(ninja.Rule{
		Name:            "rust_bindgen",
		Command:         "$bindgen $in -o $out $bindgen_flags -- $cflags",
		Description:     "BINDGEN $out",
		RestatContent:   g.opts.Siso,
		SandboxDisabled: g.opts.Siso,
	}); err != nil {
		return err
	}

	// AIDL C++ codegen
	if err := g.nw.Rule(ninja.Rule{
		Name:          "aidl_cpp",
		Command:       "mkdir -p $header_dir $out_dir && $aidl --lang=cpp $optional_flags --ninja -d $out.d -h $header_dir -o $out_dir $imports $in",
		DepFile:       "$out.d",
		Deps:          "gcc",
		Description:   "AIDL_CPP $out",
		RestatContent: g.opts.Siso,
	}); err != nil {
		return err
	}

	// AIDL NDK codegen
	if err := g.nw.Rule(ninja.Rule{
		Name:          "aidl_ndk",
		Command:       "mkdir -p $header_dir $out_dir && $aidl --lang=ndk $optional_flags --ninja -d $out.d -h $header_dir -o $out_dir $imports $in",
		DepFile:       "$out.d",
		Deps:          "gcc",
		Description:   "AIDL_NDK $out",
		RestatContent: g.opts.Siso,
	}); err != nil {
		return err
	}

	// AIDL Java codegen & compilation
	if err := g.nw.Rule(ninja.Rule{
		Name:            "aidl_java",
		Command:         "bash -c 'rm -rf $out_dir && mkdir -p $out_dir/src $out_dir/classes && $aidl --lang=java $optional_flags -o $out_dir/src $imports $in && jfs=$$(find $out_dir/src -name \"*.java\") && if [ -n \"$$jfs\" ]; then javac -encoding UTF-8 -d $out_dir/classes $classpath $$jfs; fi && $jar cf $out -C $out_dir/classes .'",
		Description:     "AIDL_JAVA $out",
		RestatContent:   g.opts.Siso,
		SandboxDisabled: g.opts.Siso,
	}); err != nil {
		return err
	}

	// Merge Zips (for Java libraries merging static_libs or aggregation libraries)
	if err := g.nw.Rule(ninja.Rule{
		Name:            "merge_zips",
		Command:         "bash -c 'mkdir -p $$(dirname $out) && MZ=$$(which merge_zips 2>/dev/null || echo prebuilts/build-tools/linux-x86/bin/merge_zips) && if [ -s ${out}.rsp ]; then $$MZ -j -ignore-duplicates $out @${out}.rsp; else rm -rf ${out}.tmp && mkdir -p ${out}.tmp && $jar cf $out -C ${out}.tmp . && rm -rf ${out}.tmp; fi'",
		Description:     "MERGE_ZIPS $out",
		RspFile:         "${out}.rsp",
		RspContent:      "$in",
		RestatContent:   g.opts.Siso,
		SandboxDisabled: g.opts.Siso,
	}); err != nil {
		return err
	}

	// Jarjar repackaging rule
	if err := g.nw.Rule(ninja.Rule{
		Name:            "jarjar_process",
		Command:         "bash -c 'mkdir -p $$(dirname $out) && JJ=$$(which jarjar 2>/dev/null || echo $top/prebuilts/host/external/jarjar/lib/jarjar.jar) && if [[ \"$$JJ\" == *.jar ]]; then java -jar $$JJ process $rules $in $out; else $$JJ process $rules $in $out; fi'",
		Description:     "JARJAR $out",
		RestatContent:   g.opts.Siso,
		SandboxDisabled: g.opts.Siso,
	}); err != nil {
		return err
	}

	// AIDL Rust codegen
	if err := g.nw.Rule(ninja.Rule{
		Name:          "aidl_rust",
		Command:       "mkdir -p $out_dir && $aidl --lang=rust $optional_flags --ninja -d $out.d -o $out_dir $imports $in",
		Description:   "AIDL_RUST $out",
		RestatContent: g.opts.Siso,
	}); err != nil {
		return err
	}

	// AIDL Batch Java codegen (uses rspfile + xargs to prevent ARG_MAX overflow)
	if err := g.nw.Rule(ninja.Rule{
		Name:            "aidl_batch_compile",
		Command:         "bash -c \"rm -rf $out_dir && mkdir -p $out_dir && xargs -r -n 100 $aidl --lang=java -o $out_dir $imports < ${out}.rsp && touch $out\"",
		Description:     "AIDL_BATCH $out",
		RspFile:         "${out}.rsp",
		RspContent:      "$in",
		RestatContent:   g.opts.Siso,
		SandboxDisabled: g.opts.Siso,
	}); err != nil {
		return err
	}

	// Aconfig Java codegen
	if err := g.nw.Rule(ninja.Rule{
		Name:            "aconfig_java",
		Command:         "bash -c \"mkdir -p $cache_dir $gen_dir && aconfig create-cache --package $pkg $decl_flags --cache $out_cache && rm -rf $gen_dir && mkdir -p $gen_dir && aconfig create-java-lib --cache $out_cache --out $gen_dir && $jar cf $out -C $gen_dir .\"",
		Description:     "ACONFIG_JAVA $out",
		RestatContent:   g.opts.Siso,
		SandboxDisabled: g.opts.Siso,
	}); err != nil {
		return err
	}

	// Metalava stubs generation
	if err := g.nw.Rule(ninja.Rule{
		Name:            "metalava_stubs",
		Command:         "bash -c \"$cmd\"",
		Description:     "METALAVA_STUBS $out",
		RestatContent:   g.opts.Siso,
		SandboxDisabled: g.opts.Siso,
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

// archLibSub maps a target architecture to its library subdirectory name:
// lib64, lib, or libx32 (x32 ABI, ILP32 on x86-64, follows the glibc multilib
// layout). Single source of truth for libDir() and the prefix rpath location.
func archLibSub(arch string) string {
	switch {
	case arch == "x32":
		return "libx32"
	case strings.Contains(arch, "64"):
		return "lib64"
	default:
		return "lib"
	}
}

func (g *Generator) libDir() string {
	return filepath.Join(g.opts.OutDir, archLibSub(g.opts.TargetArch))
}

// prefixMode reports whether -prefix selected a runtime install prefix whose
// libsub receives rpath flags on link edges. Relative mode ("" default, "."
// or "/") keeps the exact pre--prefix behavior (no rpath flags) instead.
func (g *Generator) prefixMode() bool {
	p := g.opts.Prefix
	return p != "" && p != "." && p != "/"
}

// prefixLibSub returns the library subdirectory under the prefix
// (lib64/lib/libx32 by target arch).
func (g *Generator) prefixLibSub() string {
	return archLibSub(g.opts.TargetArch)
}

// archCodegenFlags returns ABI-selection flags for the target architecture.
// NDK wrapper compilers already encode the triple in their name, so this only
// covers ABIs plain host compilers cannot infer from the target: x32 (-mx32).
func (g *Generator) archCodegenFlags() []string {
	if g.opts.TargetArch == "x32" {
		return []string{"-mx32"}
	}
	return nil
}

// plainCompilerUsesDefaultTarget reports whether the configured compiler
// targets its build host by default (plain clang/gcc) as opposed to NDK
// wrapper compilers (aarch64-linux-android34-clang) that already encode the
// Android target triple in their name.
func (g *Generator) plainCompilerUsesDefaultTarget() bool {
	cc := filepath.Base(g.opts.ClangPath)
	triple := g.opts.TargetTriple
	if triple == "" {
		triple = "-linux-android"
	}
	if strings.Contains(cc, "-linux-android") || strings.Contains(cc, triple) {
		return false
	}
	// android-clang wrappers from newer NDK layouts
	if strings.HasPrefix(cc, "android") && strings.HasSuffix(cc, "-clang") {
		return false
	}
	return true
}

// crossTargetFlags returns the target-selection flags (-target <triple>,
// --sysroot=<dir>) that must be passed explicitly to plain compilers in
// target mode. NDK wrappers resolve these themselves, so nothing is injected
// for them. This is what makes plain `clang` on arm64 servers (or Termux on
// a device) cross-target Android correctly instead of compiling for the
// build host.
func (g *Generator) crossTargetFlags() []string {
	if g.opts.IsHost || g.opts.TargetTriple == "" {
		return nil
	}
	if !g.plainCompilerUsesDefaultTarget() {
		return nil
	}
	// Single-token forms survive shellJoin's bare-flag-prefix filter and
	// dedup as opaque strings; clang/gcc accept both --target= / --sysroot=.
	triple := g.opts.TargetTriple
	if g.opts.APILevel > 0 && !strings.HasSuffix(triple, fmt.Sprintf("%d", g.opts.APILevel)) {
		triple = fmt.Sprintf("%s%d", triple, g.opts.APILevel)
	}
	flags := []string{"--target=" + triple}
	if g.opts.SysrootDir != "" {
		flags = append(flags, "--sysroot="+g.opts.SysrootDir)
	}
	return flags
}

// resolveCurrentAndroidJarForModule locates the platform SDK android.jar matching the module's sdk_version scope.
func (g *Generator) resolveCurrentAndroidJarForModule(mod *eval.EvaluatedModule) string {
	if g.opts.AndroidJarPath != "" {
		if fi, err := os.Stat(g.opts.AndroidJarPath); err == nil && !fi.IsDir() {
			return g.opts.AndroidJarPath
		}
	}

	targetApi := g.opts.APILevel
	if targetApi <= 0 {
		targetApi = 34
	}

	sdkVer := ""
	if mod != nil {
		sdkVer = mod.GetString("sdk_version")
	}

	if g.opts.TopDir != "" {
		var scopeSubdir string
		if sdkVer == "core_platform" || strings.Contains(sdkVer, "module_") {
			scopeSubdir = "module-lib"
		} else if strings.Contains(sdkVer, "system_") {
			scopeSubdir = "system"
		} else if strings.Contains(sdkVer, "test_") {
			scopeSubdir = "test"
		}

		if scopeSubdir != "" {
			candList := []string{
				filepath.Join(g.opts.TopDir, "prebuilts/sdk", fmt.Sprintf("%d", targetApi), scopeSubdir, "android.jar"),
				filepath.Join(g.opts.TopDir, "prebuilts/sdk/current", scopeSubdir, "android.jar"),
			}
			for _, cand := range candList {
				if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
					return cand
				}
			}
		}
	}

	return g.resolveCurrentAndroidJar()
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

func (g *Generator) isHostModule(mod *eval.EvaluatedModule) bool {
	return g.opts.IsHost || (mod != nil && mod.IsHostOnly())
}

func (g *Generator) findPrebuiltHostTool(name string) string {
	if g.opts.TopDir == "" {
		return ""
	}
	hostPatterns := []string{
		filepath.Join(g.opts.TopDir, "prebuilts", "build-tools", "linux-x86", "bin", name),
		filepath.Join(g.opts.TopDir, "prebuilts", "build-tools", "path", "linux-x86", name),
		filepath.Join(g.opts.TopDir, "prebuilts", "host", "*", "bin", name),
		filepath.Join(g.opts.TopDir, "prebuilts", "external", "*", "host", "bin", name),
		filepath.Join(g.opts.TopDir, "prebuilts", "build-tools", "*", "bin", name),
	}
	for _, pat := range hostPatterns {
		if matches, err := filepath.Glob(pat); err == nil && len(matches) > 0 {
			for _, m := range matches {
				if strings.Contains(m, "darwin") {
					continue
				}
				if fi, err := os.Stat(m); err == nil && !fi.IsDir() {
					return m
				}
			}
		}
	}
	return ""
}

// resolveTool locates the executable or output path for a named tool/module.
func (g *Generator) resolveTool(name string) string {
	// 1. Check authentic prebuilt shadows (prebuilts/host/*/bin/<name> or prebuilts/build-tools/*/bin/<name>)
	if prebuilt := g.findPrebuiltHostTool(name); prebuilt != "" {
		return prebuilt
	}

	// 2. Check if tool module outputs were already recorded
	if targets, ok := g.moduleOutputs[name]; ok && len(targets) > 0 {
		return targets[0]
	}

	// 3. Check if the module is in moduleMap
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
		if strings.HasPrefix(s, ":") || strings.HasPrefix(s, "//") {
			for _, ref := range g.resolveReference(s) {
				expanded = append(expanded, g.expandGlob(ref)...)
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

func (g *Generator) generateInstallSymlink(mod *eval.EvaluatedModule) ([]string, error) {
	installedLoc := mod.GetString("installed_location")
	symlinkTarget := mod.GetString("symlink_target")
	if installedLoc == "" || symlinkTarget == "" {
		return nil, nil
	}
	outPath := filepath.Join(g.opts.OutDir, installedLoc)
	err := g.nw.Build(ninja.BuildEdge{
		Outputs: []string{outPath},
		Rule:    "symlink",
		Variables: map[string]string{
			"symlink_target": symlinkTarget,
		},
	})
	if err != nil {
		return nil, err
	}
	return []string{outPath}, nil
}

func (g *Generator) generateFilesystem(mod *eval.EvaluatedModule) ([]string, error) {
	rawSymlinks, ok := mod.Properties["symlinks"].([]interface{})
	if !ok || len(rawSymlinks) == 0 {
		return nil, nil
	}
	var targets []string
	for _, raw := range rawSymlinks {
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		target, _ := item["target"].(string)
		name, _ := item["name"].(string)
		if target == "" || name == "" {
			continue
		}
		outPath := filepath.Join(g.opts.OutDir, name)
		err := g.nw.Build(ninja.BuildEdge{
			Outputs: []string{outPath},
			Rule:    "symlink",
			Variables: map[string]string{
				"symlink_target": target,
			},
		})
		if err != nil {
			return nil, err
		}
		targets = append(targets, outPath)
	}
	return targets, nil
}

// IsTransitiveInputOf checks whether candidate is a direct or indirect dependency of rootModuleName.
func (g *Generator) IsTransitiveInputOf(candidate, rootModuleName string, visited map[string]bool) bool {
	if candidate == rootModuleName {
		return true
	}
	rootMod := g.moduleMap[rootModuleName]
	if rootMod == nil {
		return false
	}
	if visited[rootModuleName] {
		return false
	}
	visited[rootModuleName] = true

	var deps []string
	deps = append(deps, rootMod.GetStringList("static_libs")...)
	deps = append(deps, rootMod.GetStringList("libs")...)
	for _, src := range rootMod.GetStringList("srcs") {
		if strings.HasPrefix(src, ":") {
			deps = append(deps, strings.TrimPrefix(src, ":"))
		}
	}
	for _, d := range deps {
		cleanD := strings.TrimSuffix(d, ".jar")
		if cleanD == candidate {
			return true
		}
		if g.IsTransitiveInputOf(candidate, cleanD, visited) {
			return true
		}
	}
	return false
}
