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
	TopDir          string
	OutDir          string
	SysrootDir      string
	PrebuiltLibDir  string
	ClangPath       string
	ClangCxxPath    string
	ArPath          string
	Aapt2Path       string
	AndroidJarPath  string
	TargetArch      string
	TargetTriple    string
	AllowMissingDeps bool
	BpDir           string
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
		TopDir:          topDir,
		OutDir:          outDir,
		TargetArch:      "arm64",
		TargetTriple:    "aarch64-linux-android10000",
		ClangPath:       "clang",
		ClangCxxPath:    "clang++",
		ArPath:          "llvm-ar",
		Aapt2Path:       "aapt2",
		AndroidJarPath:  "",
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
	}
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
	if strings.HasPrefix(ref, ":") {
		label := strings.TrimPrefix(ref, ":")
		if fg, ok := g.filegroups[label]; ok && len(fg) > 0 {
			return fg
		}
		if outs, ok := g.moduleOutputs[label]; ok && len(outs) > 0 {
			return outs
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

	// Index all modules and filegroups by name
	for _, mod := range modules {
		if mod.Name != "" {
			g.moduleMap[mod.Name] = mod
			if mod.Type == "filegroup" {
				g.filegroups[mod.Name] = mod.GetStringList("srcs")
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
		case "cc_library", "cc_library_shared", "cc_library_static", "cc_library_headers":
			targets, err = g.generateCcLibrary(mod)
		case "cc_test", "cc_benchmark":
			targets, err = g.generateCcTest(mod)
		case "android_app", "android_app_certificate":
			targets, err = g.generateAndroidApp(mod)
		case "java_library", "java_library_host", "android_library":
			targets, err = g.generateJavaLibrary(mod)
		case "genrule":
			targets, err = g.generateGenrule(mod)
		case "prebuilt_etc", "prebuilt_etc_host", "sh_binary", "sh_binary_host":
			targets, err = g.generatePrebuilt(mod)
		default:
			// Fallback only: Pattern recognition heuristic when no plugin or handler exists
			targets, err = g.autoDeriveFallback(mod)
		}

		if err != nil {
			return fmt.Errorf("failed generating module %s: %w", mod.Name, err)
		}
		g.moduleOutputs[mod.Name] = targets
		allTargets = append(allTargets, targets...)
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
	if filepath.IsAbs(relTop) {
		if rel, err := filepath.Rel(".", relTop); err == nil && !strings.HasPrefix(rel, "..") {
			relTop = rel
		} else {
			relTop = "."
		}
	}
	if relTop == "" {
		relTop = "."
	}
	g.nw.Variable("top", relTop)
	g.nw.BlankLine()

	g.nw.Variable("cc", g.opts.ClangPath)
	g.nw.Variable("cxx", g.opts.ClangCxxPath)
	g.nw.Variable("ar", g.opts.ArPath)
	g.nw.Variable("aapt2", g.opts.Aapt2Path)
	g.nw.BlankLine()
	return nil
}

func (g *Generator) emitStandardRules() error {
	// CC Compile
	if err := g.nw.Rule(ninja.Rule{
		Name:        "cc_compile",
		Command:     "$cc -MD -MF $out.d $cflags $includes -c $in -o $out",
		DepFile:     "$out.d",
		Deps:        "gcc",
		Description: "CC $out",
	}); err != nil {
		return err
	}

	// CXX Compile
	if err := g.nw.Rule(ninja.Rule{
		Name:        "cxx_compile",
		Command:     "$cxx -MD -MF $out.d $cflags $includes -c $in -o $out",
		DepFile:     "$out.d",
		Deps:        "gcc",
		Description: "CXX $out",
	}); err != nil {
		return err
	}

	// Link Shared Library
	if err := g.nw.Rule(ninja.Rule{
		Name:        "link_shared",
		Command:     "$cxx -shared -o $out $in $ldflags $libs",
		Description: "LINK_SHARED $out",
	}); err != nil {
		return err
	}

	// Link Executable Binary
	if err := g.nw.Rule(ninja.Rule{
		Name:        "link_binary",
		Command:     "$cxx -o $out $in $ldflags $libs",
		Description: "LINK_BINARY $out",
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

	// Custom Genrule
	if err := g.nw.Rule(ninja.Rule{
		Name:        "genrule_cmd",
		Command:     "bash -c \"$cmd\"",
		Description: "GEN $out",
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

// ResolveSrcs expands filegroup/genrule references and filters out exclude_srcs.
func (g *Generator) ResolveSrcs(mod *eval.EvaluatedModule) []string {
	rawSrcs := mod.GetStringList("srcs")
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
			} else if genMod, ok := g.moduleMap[label]; ok && genMod.Type == "genrule" {
				for _, out := range genMod.GetStringList("out") {
					expanded = append(expanded, filepath.Join(g.opts.OutDir, "gen", label, out))
				}
			} else {
				expanded = append(expanded, s)
			}
		} else {
			expanded = append(expanded, g.expandGlob(s)...)
		}
	}

	excludeList := mod.GetStringList("exclude_srcs")
	if len(excludeList) > 0 {
		excludeMap := make(map[string]bool)
		for _, ex := range excludeList {
			excludeMap[ex] = true
			excludeMap[filepath.Clean(ex)] = true
		}
		var filtered []string
		for _, src := range expanded {
			if !excludeMap[src] && !excludeMap[filepath.Clean(src)] {
				filtered = append(filtered, src)
			}
		}
		expanded = filtered
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
