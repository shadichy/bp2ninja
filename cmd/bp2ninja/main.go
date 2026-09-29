package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"bp2ninja/pkg/eval"
	"bp2ninja/pkg/generator"
	"bp2ninja/pkg/gowork"
	"bp2ninja/pkg/ndk"
	"bp2ninja/pkg/ninja"
	"bp2ninja/pkg/parser"
	"bp2ninja/pkg/plugins"
)

type stringListFlag []string

func (s *stringListFlag) String() string {
	return strings.Join(*s, ",")
}

func (s *stringListFlag) Set(val string) error {
	for _, part := range strings.Split(val, ",") {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			*s = append(*s, trimmed)
		}
	}
	return nil
}

func handleConvertPlugin(args []string) {
	var target string
	var outSo string
	var bp2ninjaDir string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-o" || arg == "--output" || arg == "-output" {
			if i+1 < len(args) {
				outSo = args[i+1]
				i++
			}
		} else if strings.HasPrefix(arg, "-o=") || strings.HasPrefix(arg, "--output=") {
			parts := strings.SplitN(arg, "=", 2)
			outSo = parts[1]
		} else if arg == "--bp2ninja-dir" || arg == "-bp2ninja-dir" {
			if i+1 < len(args) {
				bp2ninjaDir = args[i+1]
				i++
			}
		} else if strings.HasPrefix(arg, "--bp2ninja-dir=") {
			parts := strings.SplitN(arg, "=", 2)
			bp2ninjaDir = parts[1]
		} else if !strings.HasPrefix(arg, "-") && target == "" {
			target = arg
		}
	}
	if target == "" {
		target = "."
	}

	if err := plugins.ConvertPlugin(target, outSo, bp2ninjaDir); err != nil {
		fmt.Fprintf(os.Stderr, "Error converting plugin: %v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}

func handleGoWork(args []string) {
	var treeDir string
	var workDir = "."
	var remote bool
	var clean bool

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--remote" || arg == "-remote":
			remote = true
		case arg == "--clean" || arg == "-clean":
			clean = true
		case arg == "--tree" || arg == "-tree":
			if i+1 < len(args) {
				treeDir = args[i+1]
				i++
			}
		case strings.HasPrefix(arg, "--tree="):
			parts := strings.SplitN(arg, "=", 2)
			treeDir = parts[1]
		case arg == "--dir" || arg == "-dir":
			if i+1 < len(args) {
				workDir = args[i+1]
				i++
			}
		case strings.HasPrefix(arg, "--dir="):
			parts := strings.SplitN(arg, "=", 2)
			workDir = parts[1]
		}
	}

	if clean {
		if err := gowork.CleanGoWork(workDir); err != nil {
			fmt.Fprintf(os.Stderr, "Error cleaning go.work: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	if err := gowork.GenerateGoWork(treeDir, workDir, remote); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating go.work: %v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "convert-plugin":
			handleConvertPlugin(os.Args[2:])
			return
		case "gowork":
			handleGoWork(os.Args[2:])
			return
		case "clean-gowork":
			_ = gowork.CleanGoWork(".")
			return
		}
	}

	var (
		bpFile              string
		outFile             string
		outDir              string
		topDir              string
		sysrootDir          string
		prebuiltLibs        string
		clangPath           string
		clangCxxPath        string
		arch                string
		allowMissing        bool
		ndkPath             string
		ndkVersion          string
		apiLevel            int
		ndkInfoFlag         bool
		extraCflags         string
		extraCppflags       string
		extraLdflags        string
		pluginPaths         stringListFlag
		configVars          stringListFlag
		convertPluginTarget string
		genGoWork           bool
		cleanGoWork         bool
		goworkRemote        bool
		goworkTree          string
		bp2ninjaDir         string
		isHost              bool
		traceSubdirs        bool
	)

	flag.StringVar(&bpFile, "bp", "Android.bp", "Path to the target Android.bp file or directory")
	flag.StringVar(&outFile, "o", "build.ninja", "Output Ninja build file path")
	flag.StringVar(&outDir, "out", "out", "Output directory for build artifacts")
	flag.StringVar(&topDir, "top", "", "Top of Android source tree (defaults to $ANDROID_BUILD_TOP or current dir)")
	flag.StringVar(&sysrootDir, "sysroot", "", "Path to Android sysroot / NDK (optional)")
	flag.StringVar(&prebuiltLibs, "prebuilt-libs", "", "Directory containing prebuilt .so / .a libraries")
	flag.StringVar(&clangPath, "cc", "clang", "C compiler executable path")
	flag.StringVar(&clangCxxPath, "cxx", "clang++", "C++ compiler executable path")
	flag.StringVar(&arch, "arch", "arm64", "Target architecture (arm64, arm, x86_64, x86, riscv64)")
	flag.BoolVar(&allowMissing, "allow-missing-deps", true, "Allow missing dependencies / inputs by generating phony rules")
	flag.BoolVar(&isHost, "host", false, "Build for host instead of target (uses host compiler and host libraries)")
	flag.BoolVar(&traceSubdirs, "subdirs", true, "Trace subdirectories containing Android.bp and include their build rules into one Ninja file")
	flag.BoolVar(&traceSubdirs, "r", true, "Alias for -subdirs (recursive)")

	// Custom/project-specific flags (can also be read from CFLAGS, CXXFLAGS, LDFLAGS)
	flag.StringVar(&extraCflags, "cflags", "", "Extra C compiler flags (or via $CFLAGS)")
	flag.StringVar(&extraCppflags, "cppflags", "", "Extra C++ flags (or via $CPPFLAGS / $CXXFLAGS)")
	flag.StringVar(&extraCppflags, "cxxflags", "", "Alias for -cppflags")
	flag.StringVar(&extraLdflags, "ldflags", "", "Extra linker flags (or via $LDFLAGS)")

	// Android NDK flags
	flag.StringVar(&ndkPath, "ndk", "", "Path to Android NDK or 'auto' to auto-discover (Studio, $ANDROID_NDK, distro packages)")
	flag.StringVar(&ndkVersion, "ndk-version", "", "Preferred Android NDK version (e.g. 'latest', 'beta', 'r29', '30')")
	flag.IntVar(&apiLevel, "api", 34, "Android API level for NDK target compiler (defaults to 34)")
	flag.BoolVar(&ndkInfoFlag, "ndk-info", false, "Display discovered Android NDK installations and exit")

	// Built-in tool flags
	flag.StringVar(&convertPluginTarget, "convert-plugin", "", "Convert in-tree Soong plugin sources to bp2ninja .so plugin")
	flag.StringVar(&bp2ninjaDir, "bp2ninja-dir", "", "Path to bp2ninja package root (used with -convert-plugin)")
	flag.BoolVar(&genGoWork, "gen-gowork", false, "Generate go.work resolving android/* packages")
	flag.BoolVar(&cleanGoWork, "clean-gowork", false, "Remove go.work and go.work.sum")
	flag.BoolVar(&goworkRemote, "remote", false, "Clone missing packages remotely in go.work generation")
	flag.StringVar(&goworkTree, "tree", "", "Android source tree root for go.work generation")

	// Plugin flags: -a, -add-plugin, --add-plugin, -plugin
	flag.Var(&pluginPaths, "a", "Path to compiled Go plugin (.so) to load (repeated or comma-separated)")
	flag.Var(&pluginPaths, "add-plugin", "Path to compiled Go plugin (.so) to load (repeated or comma-separated)")
	flag.Var(&pluginPaths, "plugin", "Alias for -a/--add-plugin")
	flag.Var(&configVars, "config", "Soong config variable in key=value format (can be repeated)")

	var positional []string
	args := os.Args[1:]
	for len(args) > 0 {
		if err := flag.CommandLine.Parse(args); err != nil {
			break
		}
		rem := flag.CommandLine.Args()
		if len(rem) == 0 {
			break
		}
		if rem[0] == "--" {
			positional = append(positional, rem[1:]...)
			break
		}
		positional = append(positional, rem[0])
		args = rem[1:]
	}

	if len(positional) > 0 {
		bpFile = positional[0]
	}

	// Handle NDK Info query
	if ndkInfoFlag {
		all := ndk.DiscoverAll()
		if len(all) == 0 {
			fmt.Println("No Android NDK installations discovered.")
			fmt.Println("Checked:")
			fmt.Println(" - Environment variables: $ANDROID_NDK, $ANDROID_NDK_HOME, $ANDROID_NDK_ROOT, $NDK_HOME, $NDK_ROOT")
			fmt.Println(" - Distro packages: /opt/android-ndk, /opt/android-ndk-beta, /usr/lib/android-ndk")
			fmt.Println(" - Android Studio / SDK: ~/Android/Sdk/ndk, $ANDROID_HOME/ndk, $ANDROID_SDK_ROOT/ndk")
			fmt.Println(" - PATH: ndk-build")
			return
		}
		fmt.Printf("Discovered %d Android NDK installation(s):\n\n", len(all))
		for i, n := range all {
			betaTag := ""
			if n.IsBeta {
				betaTag = " [BETA/PREVIEW]"
			}
			fmt.Printf("  [%d] NDK %s (r%d)%s\n", i+1, n.Version, n.MajorVer, betaTag)
			fmt.Printf("      Location: %s\n", n.Path)
			fmt.Printf("      Source:   %s\n", n.Source)
			fmt.Printf("      LLVM Bin: %s\n", n.LLVMBinDir)
			fmt.Printf("      Sysroot:  %s\n", n.SysrootDir)
			fmt.Println()
		}
		return
	}

	// Handle standalone tool actions if flags passed
	if convertPluginTarget != "" {
		if err := plugins.ConvertPlugin(convertPluginTarget, outFile, bp2ninjaDir); err != nil {
			fmt.Fprintf(os.Stderr, "Error converting plugin: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if cleanGoWork {
		if err := gowork.CleanGoWork("."); err != nil {
			fmt.Fprintf(os.Stderr, "Error cleaning go.work: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if genGoWork {
		if err := gowork.GenerateGoWork(goworkTree, ".", goworkRemote); err != nil {
			fmt.Fprintf(os.Stderr, "Error generating go.work: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Detect topDir if unset
	if topDir == "" {
		if envTop := os.Getenv("ANDROID_BUILD_TOP"); envTop != "" {
			topDir = filepath.Clean(envTop)
		} else {
			topDir = "."
		}
	} else {
		topDir = filepath.Clean(topDir)
	}

	// Parse config variables (e.g. target_board_platform=sm8450)
	configMap := make(map[string]string)
	for _, kv := range configVars {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) == 2 {
			configMap[parts[0]] = parts[1]
		}
	}

	// 1. Auto-discover plugins from default directory candidates
	loadedPlugins := make(map[string]bool)

	loadPluginFile := func(p string, isExplicit bool) error {
		absPath, err := filepath.Abs(p)
		if err != nil {
			absPath = p
		}
		if loadedPlugins[absPath] {
			return nil
		}
		if err := plugins.GlobalRegistry.LoadPlugin(absPath); err != nil {
			return err
		}
		loadedPlugins[absPath] = true
		if isExplicit {
			fmt.Printf("[bp2ninja] Loaded plugin: %s\n", p)
		} else {
			fmt.Printf("[bp2ninja] Auto-loaded plugin: %s\n", p)
		}
		return nil
	}

	var searchDirs []string
	if envDir := os.Getenv("BP2NINJA_PLUGINS_DIR"); envDir != "" {
		searchDirs = append(searchDirs, envDir)
	}
	searchDirs = append(searchDirs, "/usr/lib/bp2ninja/plugins", "/usr/local/lib/bp2ninja/plugins")

	for _, sDir := range searchDirs {
		if matches, err := filepath.Glob(filepath.Join(sDir, "*.so")); err == nil {
			for _, p := range matches {
				_ = loadPluginFile(p, false)
			}
		}
	}

	// Load any explicitly requested dynamic plugins (-a / --add-plugin)
	for _, p := range pluginPaths {
		if err := loadPluginFile(p, true); err != nil {
			fmt.Fprintf(os.Stderr, "Error loading plugin %s: %v\n", p, err)
			os.Exit(1)
		}
	}

	// 2. Determine root directory and discover Android.bp files
	st, err := os.Stat(bpFile)
	if err != nil {
		if bpFile == "Android.bp" {
			if curSt, curErr := os.Stat("."); curErr == nil && curSt.IsDir() {
				bpFile = "."
				st = curSt
				err = nil
			}
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error accessing %s: %v\n", bpFile, err)
		os.Exit(1)
	}

	var rootDir string
	var hasRootBp bool
	var primaryBp string
	if st.IsDir() {
		rootDir = filepath.Clean(bpFile)
		primaryBp = filepath.Join(rootDir, "Android.bp")
		if pst, err := os.Stat(primaryBp); err == nil && !pst.IsDir() {
			hasRootBp = true
		}
	} else {
		rootDir = filepath.Dir(bpFile)
		primaryBp = bpFile
		hasRootBp = true
	}
	if rootDir == "" {
		rootDir = "."
	}
	if filepath.IsAbs(rootDir) {
		if rel, err := filepath.Rel(".", rootDir); err == nil && !strings.HasPrefix(rel, "..") {
			rootDir = rel
		}
	}

	oFlagPassed := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "o" {
			oFlagPassed = true
		}
	})
	if !oFlagPassed {
		if !hasRootBp || rootDir != "." {
			outFile = filepath.Join(rootDir, "build.ninja")
		}
	}

	evalCtx := eval.NewContext(configMap, arch)
	evalCtx.IsHost = isHost

	evaluatedFiles := make(map[string]bool)
	var explicitSubdirs []string
	var explicitBuildFiles []string

	// Check if primaryBp exists (e.g. rootDir/Android.bp)
	if hasRootBp {
		absPrimary, err := filepath.Abs(primaryBp)
		if err == nil {
			evaluatedFiles[absPrimary] = true
		}

		bpData, err := os.ReadFile(primaryBp)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading %s: %v\n", primaryBp, err)
			os.Exit(1)
		}

		r := strings.NewReader(string(bpData))
		file, errs := parser.Parse(primaryBp, r, parser.NewScope(nil))
		if len(errs) > 0 {
			fmt.Fprintf(os.Stderr, "Error parsing %s:\n", primaryBp)
			for _, e := range errs {
				fmt.Fprintf(os.Stderr, "  %v\n", e)
			}
			os.Exit(1)
		}

		if err := evalCtx.EvalFileInDir(file, ""); err != nil {
			fmt.Fprintf(os.Stderr, "Error evaluating AST for %s: %v\n", primaryBp, err)
			os.Exit(1)
		}

		sDirs, bFiles := eval.ExtractSubdirs(file)
		explicitSubdirs = append(explicitSubdirs, sDirs...)
		explicitBuildFiles = append(explicitBuildFiles, bFiles...)
	}

	type childFile struct {
		path   string
		relDir string
		depth  int
	}
	var childFiles []childFile

	// 1) Explicit build files (e.g. build = ["..."])
	for _, bf := range explicitBuildFiles {
		bfPath := filepath.Join(rootDir, bf)
		if fi, err := os.Stat(bfPath); err == nil && !fi.IsDir() {
			abs, err := filepath.Abs(bfPath)
			if err == nil && !evaluatedFiles[abs] {
				relPath, _ := filepath.Rel(rootDir, bfPath)
				relDir := filepath.Dir(relPath)
				if relDir == "." {
					relDir = ""
				}
				depth := 0
				if relDir != "" {
					depth = strings.Count(relDir, string(filepath.Separator)) + 1
				}
				childFiles = append(childFiles, childFile{
					path:   bfPath,
					relDir: relDir,
					depth:  depth,
				})
			}
		}
	}

	// 2) Trace subdirectories
	// If the folder does not have Android.bp, ALWAYS recursively find Android.bp in subfolders.
	// If it does have Android.bp, recursively find if traceSubdirs is true.
	if !hasRootBp || traceSubdirs {
		discovered, err := generator.DiscoverBpFiles(rootDir)
		if err == nil {
			for _, d := range discovered {
				abs, err := filepath.Abs(d.Path)
				if err == nil && evaluatedFiles[abs] {
					continue
				}
				childFiles = append(childFiles, childFile{
					path:   d.Path,
					relDir: d.RelDir,
					depth:  d.Depth,
				})
			}
		}
	} else {
		// Process explicit subdirs if traceSubdirs is disabled
		for _, sDir := range explicitSubdirs {
			subBp := filepath.Join(rootDir, sDir, "Android.bp")
			if fi, err := os.Stat(subBp); err == nil && !fi.IsDir() {
				abs, err := filepath.Abs(subBp)
				if err == nil && !evaluatedFiles[abs] {
					relDir := sDir
					depth := strings.Count(relDir, string(filepath.Separator)) + 1
					childFiles = append(childFiles, childFile{
						path:   subBp,
						relDir: relDir,
						depth:  depth,
					})
				}
			}
		}
	}

	// Sort child files so parent directories are evaluated before deeper children
	sort.Slice(childFiles, func(i, j int) bool {
		if childFiles[i].depth != childFiles[j].depth {
			return childFiles[i].depth < childFiles[j].depth
		}
		return childFiles[i].relDir < childFiles[j].relDir
	})

	// Evaluate all discovered child files
	for _, cf := range childFiles {
		abs, err := filepath.Abs(cf.path)
		if err == nil {
			if evaluatedFiles[abs] {
				continue
			}
			evaluatedFiles[abs] = true
		}

		childData, err := os.ReadFile(cf.path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Could not read child file %s: %v\n", cf.path, err)
			continue
		}

		r := strings.NewReader(string(childData))
		childAst, errs := parser.Parse(cf.path, r, parser.NewScope(nil))
		if len(errs) > 0 {
			fmt.Fprintf(os.Stderr, "Warning: Parsing errors in %s:\n", cf.path)
			for _, e := range errs {
				fmt.Fprintf(os.Stderr, "  %v\n", e)
			}
			continue
		}

		if err := evalCtx.EvalFileInDir(childAst, cf.relDir); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Evaluating %s: %v\n", cf.path, err)
			continue
		}
		fmt.Printf("[bp2ninja] Evaluated child Android.bp: %s\n", cf.path)
	}

	if len(evalCtx.Modules) == 0 {
		fmt.Fprintf(os.Stderr, "Error: No modules found in %s\n", bpFile)
		os.Exit(1)
	}

	// 3.5. Resolve Android NDK if requested or configured (target only)
	var activeNDK *ndk.NDKInfo
	if !isHost {
		if ndkPath != "" || ndkVersion != "" {
			resolved, err := ndk.ResolveNDK(ndkPath, ndkVersion)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error resolving Android NDK: %v\n", err)
				os.Exit(1)
			}
			activeNDK = resolved
		} else if envNdk := os.Getenv("ANDROID_NDK"); envNdk != "" && os.Getenv("USE_NDK") == "1" {
			if resolved, err := ndk.ResolveNDK(envNdk, ""); err == nil {
				activeNDK = resolved
			}
		}
	}

	arPath := "ar"
	if _, err := exec.LookPath("llvm-ar"); err == nil {
		arPath = "llvm-ar"
	}
	targetTriple := ndk.ArchitectureToTriple(arch)
	if activeNDK != nil {
		tc, err := activeNDK.GetToolchain(arch, apiLevel)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error configuring NDK toolchain: %v\n", err)
			os.Exit(1)
		}
		if clangPath == "clang" {
			clangPath = tc.CC
		}
		if clangCxxPath == "clang++" {
			clangCxxPath = tc.CXX
		}
		if tc.AR != "" {
			arPath = tc.AR
		}
		if sysrootDir == "" {
			sysrootDir = tc.Sysroot
		}
		targetTriple = tc.TargetTriple
		fmt.Printf("[bp2ninja] Using Android NDK %s (%s, API %d) -> %s\n",
			activeNDK.Version, arch, apiLevel, clangPath)
	}

	// 4. Set up Generator options
	opts := generator.DefaultOptions(topDir, outDir)
	opts.IsHost = isHost
	opts.TargetArch = arch
	opts.TargetTriple = targetTriple
	opts.APILevel = apiLevel
	opts.ClangPath = clangPath
	opts.ClangCxxPath = clangCxxPath
	opts.ArPath = arPath
	opts.AllowMissingDeps = allowMissing
	if activeNDK != nil {
		opts.NDKDir = activeNDK.Path
	}
	opts.BpDir = rootDir
	if sysrootDir != "" {
		opts.SysrootDir = sysrootDir
	}
	if prebuiltLibs != "" {
		opts.PrebuiltLibDir = prebuiltLibs
	}

	// Merge environment variables and CLI extra flags (per-project configuration)
	var combinedCflags []string
	if envCflags := os.Getenv("CFLAGS"); envCflags != "" {
		combinedCflags = append(combinedCflags, strings.Fields(envCflags)...)
	}
	if extraCflags != "" {
		combinedCflags = append(combinedCflags, strings.Fields(extraCflags)...)
	}
	opts.ExtraCflags = combinedCflags

	var combinedCppflags []string
	if envCppflags := os.Getenv("CPPFLAGS"); envCppflags != "" {
		combinedCppflags = append(combinedCppflags, strings.Fields(envCppflags)...)
	}
	if envCxxflags := os.Getenv("CXXFLAGS"); envCxxflags != "" {
		combinedCppflags = append(combinedCppflags, strings.Fields(envCxxflags)...)
	}
	if extraCppflags != "" {
		combinedCppflags = append(combinedCppflags, strings.Fields(extraCppflags)...)
	}
	opts.ExtraCppflags = combinedCppflags

	var combinedLdflags []string
	if envLdflags := os.Getenv("LDFLAGS"); envLdflags != "" {
		combinedLdflags = append(combinedLdflags, strings.Fields(envLdflags)...)
	}
	if extraLdflags != "" {
		combinedLdflags = append(combinedLdflags, strings.Fields(extraLdflags)...)
	}
	opts.ExtraLdflags = combinedLdflags

	// 5. Open output file and generate Ninja rules
	if err := os.MkdirAll(filepath.Dir(outFile), 0755); err != nil && filepath.Dir(outFile) != "." {
		fmt.Fprintf(os.Stderr, "Error creating output directory: %v\n", err)
		os.Exit(1)
	}

	outF, err := os.Create(outFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating %s: %v\n", outFile, err)
		os.Exit(1)
	}
	defer outF.Close()

	nw := ninja.NewWriter(outF)
	gen := generator.New(opts, nw, plugins.GlobalRegistry)

	if err := gen.Generate(evalCtx.Modules); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating Ninja: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("[bp2ninja] Successfully converted %s -> %s (%d modules)\n",
		bpFile, outFile, len(evalCtx.Modules))
}
