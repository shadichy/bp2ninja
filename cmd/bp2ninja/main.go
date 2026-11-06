package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

type pathListFlag []string

func (p *pathListFlag) String() string {
	return strings.Join(*p, ":")
}

func (p *pathListFlag) Set(val string) error {
	sep := ":"
	if strings.Contains(val, ",") && !strings.Contains(val, ":") {
		sep = ","
	}
	for _, part := range strings.Split(val, sep) {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			*p = append(*p, trimmed)
		}
	}
	return nil
}

// normalizeArgs normalizes command-line arguments to a single '-' prefix.
// Any flag starting with '--' (e.g. '--top', '--tree=foo') is rewritten to
// use a single '-' prefix ('-top', '-tree=foo'). Standalone '--' is preserved
// as the standard separator indicating the end of flags, and all arguments
// following it are left unmodified.
func normalizeArgs(args []string) []string {
	out := make([]string, len(args))
	sawTerminator := false
	for i, a := range args {
		if sawTerminator {
			out[i] = a
			continue
		}
		if a == "--" {
			sawTerminator = true
			out[i] = a
			continue
		}
		if strings.HasPrefix(a, "--") {
			out[i] = "-" + strings.TrimLeft(a, "-")
		} else {
			out[i] = a
		}
	}
	return out
}

func handleConvertPlugin(args []string) {
	var target string
	var outSo string
	var bp2ninjaDir string

	args = normalizeArgs(args)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-o" || arg == "-output" {
			if i+1 < len(args) {
				outSo = args[i+1]
				i++
			}
		} else if strings.HasPrefix(arg, "-o=") || strings.HasPrefix(arg, "-output=") {
			parts := strings.SplitN(arg, "=", 2)
			outSo = parts[1]
		} else if arg == "-bp2ninja-dir" {
			if i+1 < len(args) {
				bp2ninjaDir = args[i+1]
				i++
			}
		} else if strings.HasPrefix(arg, "-bp2ninja-dir=") {
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

	args = normalizeArgs(args)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-remote":
			remote = true
		case arg == "-clean":
			clean = true
		case arg == "-tree":
			if i+1 < len(args) {
				treeDir = args[i+1]
				i++
			}
		case strings.HasPrefix(arg, "-tree="):
			parts := strings.SplitN(arg, "=", 2)
			treeDir = parts[1]
		case arg == "-dir":
			if i+1 < len(args) {
				workDir = args[i+1]
				i++
			}
		case strings.HasPrefix(arg, "-dir="):
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

// isBp2SisoInvocation reports whether the binary was invoked as bp2siso
// (e.g. through a symlink), which enables -siso by default.
func isBp2SisoInvocation(argv0 string) bool {
	name := strings.TrimSuffix(filepath.Base(argv0), ".exe")
	return name == "bp2siso" || name == "bp2ninja-siso"
}

// isBp2CargoInvocation reports whether the binary was invoked as bp2cargo
// (e.g. through a symlink), which enables -cargo by default.
func isBp2CargoInvocation(argv0 string) bool {
	name := strings.TrimSuffix(filepath.Base(argv0), ".exe")
	return name == "bp2cargo" || name == "bp2ninja-cargo"
}

// printUsage outputs formatted command-line help matching GNU software documentation standards.
func printUsage(out io.Writer, argv0 string) {
	progName := filepath.Base(argv0)
	if progName == "" {
		progName = "bp2ninja"
	}
	isCargo := isBp2CargoInvocation(argv0)
	isSiso := isBp2SisoInvocation(argv0)

	cargoDef := "false (true when invoked as bp2cargo)"
	if isCargo {
		cargoDef = "true"
	}
	sisoDef := "false (true when invoked as bp2siso)"
	if isSiso {
		sisoDef = "true"
	}

	fmt.Fprintf(out, "Usage: %s [OPTION]... [FILE|DIR]\n", progName)
	if !isCargo && !isSiso {
		fmt.Fprintf(out, "  or:  bp2cargo [OPTION]... [FILE|DIR]\n")
		fmt.Fprintf(out, "  or:  bp2siso [OPTION]... [FILE|DIR]\n")
	}
	fmt.Fprintf(out, "\nTranslate Android.bp (Soong/Blueprint) build manifests into Ninja build files.\n\n")

	fmt.Fprintf(out, "Core options:\n")
	fmt.Fprintf(out, "  -bp PATH                   Path to target Android.bp file or directory (default: \"Android.bp\")\n")
	fmt.Fprintf(out, "  -o FILE                    Output Ninja build file path (default: \"build.ninja\")\n")
	fmt.Fprintf(out, "  -out DIR                   Output directory for build artifacts (default: \"out\")\n")
	fmt.Fprintf(out, "  -top DIR                   Top of Android source tree (defaults to $ANDROID_BUILD_TOP or \".\")\n")
	fmt.Fprintf(out, "  -include-root DIR          Root directory for resolving root-relative include paths\n")
	fmt.Fprintf(out, "  -prefix DIR                Runtime install prefix for deploy-relative and absolute rpaths\n")
	fmt.Fprintf(out, "  -host                      Build for host machine instead of target device\n")
	fmt.Fprintf(out, "  -subdirs, -r               Recursively discover nested Android.bp files across subdirectories (default: true)\n")
	fmt.Fprintf(out, "  -allow-missing-deps        Allow missing dependencies / inputs by generating phony rules (default: true)\n\n")

	fmt.Fprintf(out, "Target architecture and NDK options:\n")
	fmt.Fprintf(out, "  -arch ARCH                 Target architecture: arm64 (default), arm, x86_64, x86, x32, riscv64\n")
	fmt.Fprintf(out, "  -api LEVEL                 Target Android API level for NDK compilation (default: 34)\n")
	fmt.Fprintf(out, "  -ndk PATH                  Path to Android NDK or 'auto' to auto-discover\n")
	fmt.Fprintf(out, "  -ndk-version VER           Preferred Android NDK version (e.g. 'latest', 'beta', 'r29', '30')\n")
	fmt.Fprintf(out, "  -ndk-info                  Display discovered Android NDK installations and exit\n")
	fmt.Fprintf(out, "  -sysroot DIR               Path to target Android sysroot (auto-derived with -ndk)\n")
	fmt.Fprintf(out, "  -prebuilt-libs DIR         Directory containing prebuilt .so / .a libraries for linker search path\n")
	fmt.Fprintf(out, "  -skip-abi-level LEVEL      Base ABI level threshold for skipping platform libraries (default: 0)\n")
	fmt.Fprintf(out, "  -disable-skip-abis         Disable skipping base ABI libraries (force APEX resolution)\n\n")

	fmt.Fprintf(out, "Compiler and linker overrides:\n")
	fmt.Fprintf(out, "  -cc PATH                   C compiler executable path (default: \"clang\", or via $CC)\n")
	fmt.Fprintf(out, "  -cxx PATH                  C++ compiler executable path (default: \"clang++\", or via $CXX)\n")
	fmt.Fprintf(out, "  -ld PATH                   Linker executable path for partial linking (default: \"ld.lld\", or via $LD)\n")
	fmt.Fprintf(out, "  -cflags, -ccflags FLAGS    Extra C compiler flags (or via $CFLAGS / $CCFLAGS)\n")
	fmt.Fprintf(out, "  -cppflags, -cxxflags FLAGS Extra C++ compiler flags (or via $CPPFLAGS / $CXXFLAGS)\n")
	fmt.Fprintf(out, "  -asflags FLAGS             Extra assembler flags for .s/.S files (or via $ASFLAGS)\n")
	fmt.Fprintf(out, "  -ldflags FLAGS             Extra linker flags (or via $LDFLAGS)\n")
	fmt.Fprintf(out, "  -art-target-codegen-archs ARCHES\n")
	fmt.Fprintf(out, "                             Space-delimited target codegen architectures for ART/VIXL\n")
	fmt.Fprintf(out, "  -art-host-codegen-archs ARCHES\n")
	fmt.Fprintf(out, "                             Space-delimited host codegen architectures for ART/VIXL\n")
	fmt.Fprintf(out, "  -config KEY=VAL            Soong config variable in key=value format (can be repeated)\n\n")

	fmt.Fprintf(out, "Java and Kotlin toolchain options:\n")
	fmt.Fprintf(out, "  -bootclasspath PATHS       Colon-delimited bootclasspath JAR paths (prepended to all classpaths)\n")
	fmt.Fprintf(out, "  -prebuilt-jars PATHS       Colon-delimited directories or files containing prebuilt JARs\n")
	fmt.Fprintf(out, "  -extra-jars PATHS          Colon-delimited extra JAR files or directories to inject\n")
	fmt.Fprintf(out, "  -android-jar PATH          Explicit path to SDK android.jar (or via $ANDROID_JAR)\n")
	fmt.Fprintf(out, "  -java-encoding ENC         Character encoding for Java compilation (default: \"UTF-8\", or via $JAVA_ENCODING)\n\n")

	fmt.Fprintf(out, "Rust and Cargo options (bp2cargo):\n")
	fmt.Fprintf(out, "  -cargo                     Enable Cargo hybrid engine: synthesize Cargo.toml and compile via cargo\n")
	fmt.Fprintf(out, "                             (default: %s)\n", cargoDef)
	fmt.Fprintf(out, "  -cargo-path PATH           Path to cargo executable (default: \"cargo\", or via $CARGO)\n")
	fmt.Fprintf(out, "  -cargo-out-dir DIR         Custom directory for synthesized Cargo.toml manifests\n")
	fmt.Fprintf(out, "  -cargo-offline             Pass --offline and export CARGO_NET_OFFLINE=true during cargo builds\n")
	fmt.Fprintf(out, "  -bindgen-path PATH         Path to bindgen executable (default: \"bindgen\", or via $BINDGEN)\n\n")

	fmt.Fprintf(out, "Platform codegen tools:\n")
	fmt.Fprintf(out, "  -aidl PATH                 Path to aidl executable (default: \"aidl\", or via $AIDL)\n")
	fmt.Fprintf(out, "  -metalava PATH             Path to metalava executable (default: \"metalava\", or via $METALAVA)\n")
	fmt.Fprintf(out, "  -hidl-gen PATH             Path to hidl-gen executable (default: \"hidl-gen\", or via $HIDL_GEN)\n")
	fmt.Fprintf(out, "  -xsdc PATH                 Path to xsdc XML schema compiler (default: \"xsdc\", or via $XSDC)\n")
	fmt.Fprintf(out, "  -conv-linker-config PATH   Path to conv_linker_config executable (default: \"conv_linker_config\")\n")
	fmt.Fprintf(out, "  -xmllint PATH              Path to xmllint XML validator (default: \"xmllint\", or via $XMLLINT)\n")
	fmt.Fprintf(out, "  -mkbootimg PATH            Path to mkbootimg executable (default: \"mkbootimg\", or via $MKBOOTIMG)\n")
	fmt.Fprintf(out, "  -avbtool PATH              Path to avbtool executable (default: \"avbtool\", or via $AVBTOOL)\n")
	fmt.Fprintf(out, "  -lpmake PATH               Path to lpmake executable (default: \"lpmake\", or via $LPMAKE)\n")
	fmt.Fprintf(out, "  -compat-config PATH        Path to process-compat-config executable (default: \"process-compat-config\")\n\n")

	fmt.Fprintf(out, "Siso execution options (bp2siso):\n")
	fmt.Fprintf(out, "  -siso                      Emit Siso-specific ninja bindings and companion siso.star config\n")
	fmt.Fprintf(out, "                             (default: %s)\n", sisoDef)
	fmt.Fprintf(out, "  -siso-remote               With -siso: mark pure compile actions remote_enabled for RBE\n\n")

	fmt.Fprintf(out, "Modular plugins and Go workspace:\n")
	fmt.Fprintf(out, "  -a, -add-plugin, -plugin PATH\n")
	fmt.Fprintf(out, "                             Path to compiled Go plugin (.so) to load (can be repeated)\n")
	fmt.Fprintf(out, "  -convert-plugin TARGET     Convert in-tree Soong plugin sources into a bp2ninja .so plugin\n")
	fmt.Fprintf(out, "  -bp2ninja-dir DIR          Path to bp2ninja root source directory (used with -convert-plugin)\n")
	fmt.Fprintf(out, "  -gen-gowork                Generate go.work workspace resolving android/* packages\n")
	fmt.Fprintf(out, "  -clean-gowork              Remove go.work and go.work.sum files\n")
	fmt.Fprintf(out, "  -remote                    Clone missing packages remotely during go.work generation\n")
	fmt.Fprintf(out, "  -tree DIR                  Android source tree root for go.work generation\n\n")

	fmt.Fprintf(out, "Informational options:\n")
	fmt.Fprintf(out, "  -help, -h                  Display this help text and exit\n")
	fmt.Fprintf(out, "  -version, -v               Print version information and exit\n\n")

	fmt.Fprintf(out, "Subcommands:\n")
	fmt.Fprintf(out, "  convert-plugin TARGET [-o OUT.so] [-bp2ninja-dir DIR]\n")
	fmt.Fprintf(out, "                             Convert an in-tree Soong plugin into a loadable .so plugin\n")
	fmt.Fprintf(out, "  gowork [-tree DIR] [-remote] [-clean]\n")
	fmt.Fprintf(out, "                             Generate or clean go.work workspace for Android Go modules\n")
	fmt.Fprintf(out, "  clean-gowork               Remove go.work and go.work.sum workspace files\n\n")

	fmt.Fprintf(out, "Environment variables:\n")
	fmt.Fprintf(out, "  CC                         Default C compiler (overridden by -cc)\n")
	fmt.Fprintf(out, "  CXX                        Default C++ compiler (overridden by -cxx)\n")
	fmt.Fprintf(out, "  LD                         Default linker (overridden by -ld)\n")
	fmt.Fprintf(out, "  CFLAGS, CCFLAGS            Extra flags passed to C compilation\n")
	fmt.Fprintf(out, "  CPPFLAGS, CXXFLAGS         Extra flags passed to C++ compilation\n")
	fmt.Fprintf(out, "  ASFLAGS                    Extra flags passed to assembly compilation\n")
	fmt.Fprintf(out, "  LDFLAGS                    Extra flags passed to linking\n")
	fmt.Fprintf(out, "  CARGO                      Path to cargo binary (overridden by -cargo-path)\n")
	fmt.Fprintf(out, "  BINDGEN                    Path to bindgen binary (overridden by -bindgen-path)\n")
	fmt.Fprintf(out, "  AIDL                       Path to aidl binary (overridden by -aidl)\n")
	fmt.Fprintf(out, "  METALAVA                   Path to metalava binary (overridden by -metalava)\n")
	fmt.Fprintf(out, "  ANDROID_BUILD_TOP          Fallback path for -top\n")
	fmt.Fprintf(out, "  ANDROID_NDK                Android NDK directory for auto-discovery\n")
	fmt.Fprintf(out, "  ANDROID_SDK, ANDROID_HOME  Android SDK directory for android.jar resolution\n")
	fmt.Fprintf(out, "  BOOTCLASSPATH              Default bootclasspath JAR paths (overridden by -bootclasspath)\n")
	fmt.Fprintf(out, "  PREBUILT_JARS              Default prebuilt JAR directories/files (overridden by -prebuilt-jars)\n")
	fmt.Fprintf(out, "  EXTRA_JARS                 Default extra JARs to inject (overridden by -extra-jars)\n")
	fmt.Fprintf(out, "  ANDROID_JAR                Explicit path to SDK android.jar (overridden by -android-jar)\n")
	fmt.Fprintf(out, "  JAVA_ENCODING              Character encoding for Java compilation (overridden by -java-encoding)\n")
	fmt.Fprintf(out, "  BP2NINJA_PLUGINS_DIR       Directory containing dynamic .so plugins for auto-loading\n")
	fmt.Fprintf(out, "  HIDL_GEN, XSDC, CONV_LINKER_CONFIG, XMLLINT, MKBOOTIMG, AVBTOOL, LPMAKE, PROCESS_COMPAT_CONFIG\n")
	fmt.Fprintf(out, "                             Custom tool binary paths\n\n")

	fmt.Fprintf(out, "Report bugs to: <https://github.com/shadichy/bp2ninja/issues>\n")
	fmt.Fprintf(out, "bp2ninja home page: <https://github.com/shadichy/bp2ninja>\n")
}

func main() {
	rawArgs := normalizeArgs(os.Args[1:])
	if len(rawArgs) > 0 {
		switch rawArgs[0] {
		case "convert-plugin":
			handleConvertPlugin(rawArgs[1:])
			return
		case "gowork":
			handleGoWork(rawArgs[1:])
			return
		case "clean-gowork":
			_ = gowork.CleanGoWork(".")
			return
		case "help", "-help", "-h":
			printUsage(os.Stdout, os.Args[0])
			return
		}
	}

	// Invocation alias: a bp2siso symlink behaves as bp2ninja -siso.
	sisoDefault := isBp2SisoInvocation(os.Args[0])
	cargoDefault := isBp2CargoInvocation(os.Args[0])

	flag.Usage = func() {
		out := flag.CommandLine.Output()
		for _, a := range rawArgs {
			if a == "-h" || a == "-help" || a == "help" {
				out = os.Stdout
				break
			}
		}
		printUsage(out, os.Args[0])
	}
	flag.CommandLine.Usage = flag.Usage

	var (
		bpFile              string
		outFile             string
		outDir              string
		prefix              string
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
		extraCcflags        string
		extraCppflags       string
		extraLdflags        string
		extraAsflags        string
		ldPath              string
		includeRoot         string
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
		siso                bool
		sisoRemote          bool
		cargo               bool
		cargoPath           string
		cargoOutDir         string
		cargoOffline        bool
		bindgenPath         string
		aidlPath            string
		metalavaPath        string
	)

	// 1. Core Options
	flag.StringVar(&bpFile, "bp", "Android.bp", "Path to target Android.bp file or directory")
	flag.StringVar(&outFile, "o", "build.ninja", "Output Ninja build file path")
	flag.StringVar(&outDir, "out", "out", "Output directory for build artifacts")
	flag.StringVar(&topDir, "top", "", "Top of Android source tree (defaults to $ANDROID_BUILD_TOP or current dir)")
	flag.StringVar(&includeRoot, "include-root", "", "Root directory for resolving root-relative include paths")
	flag.StringVar(&prefix, "prefix", "", "Runtime install prefix: link edges gain -Wl,-rpath,<prefix>/<libsub>")
	flag.BoolVar(&isHost, "host", false, "Build for host machine instead of target device")
	flag.BoolVar(&traceSubdirs, "subdirs", true, "Trace subdirectories containing Android.bp and include their build rules into one Ninja file")
	flag.BoolVar(&traceSubdirs, "r", true, "Alias for -subdirs (recursive)")
	flag.BoolVar(&allowMissing, "allow-missing-deps", true, "Allow missing dependencies / inputs by generating phony rules")

	// 2. Target Architecture & NDK Options
	flag.StringVar(&arch, "arch", "arm64", "Target architecture (arm64, arm, x86_64, x86, x32, riscv64)")
	flag.IntVar(&apiLevel, "api", 34, "Android API level for NDK target compiler (defaults to 34)")
	flag.StringVar(&ndkPath, "ndk", "", "Path to Android NDK or 'auto' to auto-discover")
	flag.StringVar(&ndkVersion, "ndk-version", "", "Preferred Android NDK version (e.g. 'latest', 'beta', 'r29', '30')")
	flag.BoolVar(&ndkInfoFlag, "ndk-info", false, "Display discovered Android NDK installations and exit")
	flag.StringVar(&sysrootDir, "sysroot", "", "Path to target Android sysroot (auto-derived with -ndk)")
	flag.StringVar(&prebuiltLibs, "prebuilt-libs", "", "Directory containing prebuilt .so / .a libraries")
	var skipAbiLevel int
	var disableSkipAbis bool
	flag.IntVar(&skipAbiLevel, "skip-abi-level", 0, "Base ABI level threshold for skipping platform libraries")
	flag.BoolVar(&disableSkipAbis, "disable-skip-abis", false, "Disable skipping base ABI libraries (force APEX resolution)")

	// 3. Compiler & Linker Overrides
	flag.StringVar(&clangPath, "cc", "clang", "C compiler executable path")
	flag.StringVar(&clangCxxPath, "cxx", "clang++", "C++ compiler executable path")
	flag.StringVar(&ldPath, "ld", "ld.lld", "Linker executable path")
	flag.StringVar(&extraCflags, "cflags", "", "Extra C compiler flags (or via $CFLAGS / $CCFLAGS)")
	flag.StringVar(&extraCcflags, "ccflags", "", "Alias for -cflags")
	flag.StringVar(&extraCppflags, "cppflags", "", "Extra C++ flags (or via $CPPFLAGS / $CXXFLAGS)")
	flag.StringVar(&extraCppflags, "cxxflags", "", "Alias for -cppflags")
	flag.StringVar(&extraLdflags, "ldflags", "", "Extra linker flags (or via $LDFLAGS)")
	flag.StringVar(&extraAsflags, "asflags", "", "Extra assembler flags (or via $ASFLAGS)")
	var javaEncoding string
	flag.StringVar(&javaEncoding, "java-encoding", "UTF-8", "Character encoding for Java compilation (or via $JAVA_ENCODING)")
	var artTargetCodegen string
	var artHostCodegen string
	flag.StringVar(&artTargetCodegen, "art-target-codegen-archs", "", "Space-delimited target codegen architectures for ART/VIXL")
	flag.StringVar(&artHostCodegen, "art-host-codegen-archs", "", "Space-delimited host codegen architectures for ART/VIXL")
	flag.Var(&configVars, "config", "Soong config variable in key=value format (can be repeated)")

	// 3.5. Java and Kotlin Toolchain Options
	var bootclasspathFlags pathListFlag
	var prebuiltJarsFlags pathListFlag
	var extraJarsFlags pathListFlag
	var platformClasspathFlags pathListFlag
	var androidJarPath string
	flag.Var(&bootclasspathFlags, "bootclasspath", "Colon-delimited bootclasspath JAR paths (or via $BOOTCLASSPATH)")
	flag.Var(&prebuiltJarsFlags, "prebuilt-jars", "Colon-delimited directories or files containing prebuilt JARs (or via $PREBUILT_JARS)")
	flag.Var(&extraJarsFlags, "extra-jars", "Colon-delimited extra JAR files or directories to inject (or via $EXTRA_JARS)")
	flag.Var(&platformClasspathFlags, "platform-classpath", "Colon-delimited JAR paths applied to platform modules without sdk_version (or via $PLATFORM_CLASSPATH)")
	flag.Var(&platformClasspathFlags, "platform-jars", "Alias for -platform-classpath")
	flag.StringVar(&androidJarPath, "android-jar", "", "Explicit path to SDK android.jar (or via $ANDROID_JAR)")

	// 4. Rust and Cargo Hybrid Options (bp2cargo)
	flag.BoolVar(&cargo, "cargo", cargoDefault, "Enable Cargo hybrid engine (default: on when invoked as bp2cargo)")
	flag.StringVar(&cargoPath, "cargo-path", "cargo", "Path to cargo executable (or via $CARGO)")
	flag.StringVar(&cargoOutDir, "cargo-out-dir", "", "Custom directory for synthesized Cargo manifests")
	flag.BoolVar(&cargoOffline, "cargo-offline", false, "Pass --offline and CARGO_NET_OFFLINE=true to cargo builds")
	flag.StringVar(&bindgenPath, "bindgen-path", "bindgen", "Path to bindgen executable (or via $BINDGEN)")

	// 5. Platform Codegen Tool Overrides
	var hidlGenPath string
	var xsdcPath string
	var convLinkerConfigPath string
	var xmllintPath string
	var mkbootimgPath string
	var avbtoolPath string
	var lpmakePath string
	var compatConfigPath string
	flag.StringVar(&aidlPath, "aidl", "aidl", "Path to aidl executable (or via $AIDL)")
	flag.StringVar(&metalavaPath, "metalava", "metalava", "Path to metalava executable (or via $METALAVA)")
	flag.StringVar(&hidlGenPath, "hidl-gen", "hidl-gen", "Path to hidl-gen executable (or via $HIDL_GEN)")
	flag.StringVar(&xsdcPath, "xsdc", "xsdc", "Path to xsdc executable (or via $XSDC)")
	flag.StringVar(&convLinkerConfigPath, "conv-linker-config", "conv_linker_config", "Path to conv_linker_config executable (or via $CONV_LINKER_CONFIG)")
	flag.StringVar(&xmllintPath, "xmllint", "xmllint", "Path to xmllint executable (or via $XMLLINT)")
	flag.StringVar(&mkbootimgPath, "mkbootimg", "mkbootimg", "Path to mkbootimg executable (or via $MKBOOTIMG)")
	flag.StringVar(&avbtoolPath, "avbtool", "avbtool", "Path to avbtool executable (or via $AVBTOOL)")
	flag.StringVar(&lpmakePath, "lpmake", "lpmake", "Path to lpmake executable (or via $LPMAKE)")
	flag.StringVar(&compatConfigPath, "compat-config", "process-compat-config", "Path to process-compat-config executable (or via $PROCESS_COMPAT_CONFIG)")

	// 6. Siso Execution Options (bp2siso)
	flag.BoolVar(&siso, "siso", sisoDefault, "Emit Siso-specific ninja bindings and companion siso.star config (default: on when invoked as bp2siso)")
	flag.BoolVar(&sisoRemote, "siso-remote", false, "With -siso: mark pure compile actions remote_enabled for RBE offloading")

	// 7. Modular Plugins and Go Workspace
	flag.Var(&pluginPaths, "a", "Path to compiled Go plugin (.so) to load (repeated or comma-separated)")
	flag.Var(&pluginPaths, "add-plugin", "Path to compiled Go plugin (.so) to load (repeated or comma-separated)")
	flag.Var(&pluginPaths, "plugin", "Alias for -a/-add-plugin")
	flag.StringVar(&convertPluginTarget, "convert-plugin", "", "Convert in-tree Soong plugin sources to bp2ninja .so plugin")
	flag.StringVar(&bp2ninjaDir, "bp2ninja-dir", "", "Path to bp2ninja package root (used with -convert-plugin)")
	flag.BoolVar(&genGoWork, "gen-gowork", false, "Generate go.work resolving android/* packages")
	flag.BoolVar(&cleanGoWork, "clean-gowork", false, "Remove go.work and go.work.sum")
	flag.BoolVar(&goworkRemote, "remote", false, "Clone missing packages remotely in go.work generation")
	flag.StringVar(&goworkTree, "tree", "", "Android source tree root for go.work generation")

	// 8. Informational Options
	var versionFlag bool
	flag.BoolVar(&versionFlag, "version", false, "Print bp2ninja version and exit")
	flag.BoolVar(&versionFlag, "v", false, "Alias for -version")

	var positional []string
	args := rawArgs
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

	if versionFlag {
		fmt.Println("bp2ninja 0.1.2")
		return
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

	// Normalize -prefix: "", "." and "/" (after Clean) select relative mode.
	if prefix != "" {
		prefix = filepath.Clean(prefix)
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
	if os.Getenv("BP2NINJA_DISABLE_PLUGINS") == "" {
		if envDir := os.Getenv("BP2NINJA_PLUGINS_DIR"); envDir != "" {
			if envDir != "none" && envDir != "0" {
				searchDirs = append(searchDirs, envDir)
			}
		} else {
			searchDirs = append(searchDirs, "/usr/lib/bp2ninja/plugins", "/usr/local/lib/bp2ninja/plugins")
		}
	}

	for _, sDir := range searchDirs {
		if matches, err := filepath.Glob(filepath.Join(sDir, "*.so")); err == nil {
			for _, p := range matches {
				_ = loadPluginFile(p, false)
			}
		}
	}

	// Load any explicitly requested dynamic plugins (-a / -add-plugin)
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

	if isHost {
		if envCC := os.Getenv("CC"); envCC != "" && clangPath == "clang" {
			clangPath = envCC
		}
		if envCXX := os.Getenv("CXX"); envCXX != "" && clangCxxPath == "clang++" {
			clangCxxPath = envCXX
		}
		archPassed := false
		flag.Visit(func(f *flag.Flag) {
			if f.Name == "arch" {
				archPassed = true
			}
		})
		if !archPassed {
			switch runtime.GOARCH {
			case "amd64":
				arch = "x86_64"
			case "arm64":
				arch = "arm64"
			}
		}
	}

	evalCtx := eval.NewContext(configMap, arch)
	evalCtx.IsHost = isHost
	if artTargetCodegen != "" {
		evalCtx.TargetCodegenArches = strings.Fields(artTargetCodegen)
	}
	if artHostCodegen != "" {
		evalCtx.HostCodegenArches = strings.Fields(artHostCodegen)
	}

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

	// 2) Explicit subdirs from root Android.bp (subdirs = [...], optional_subdirs = [...])
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
		if traceSubdirs {
			subDirFullPath := filepath.Join(rootDir, sDir)
			if dList, err := generator.DiscoverBpFiles(subDirFullPath); err == nil {
				for _, d := range dList {
					abs, err := filepath.Abs(d.Path)
					if err == nil && !evaluatedFiles[abs] {
						relPath, _ := filepath.Rel(rootDir, d.Path)
						relDir := filepath.Dir(relPath)
						childFiles = append(childFiles, childFile{
							path:   d.Path,
							relDir: relDir,
							depth:  strings.Count(relDir, string(filepath.Separator)) + 1,
						})
					}
				}
			}
		}
	}

	// 3) Trace subdirectories
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
	}

	// Sort child files so parent directories are evaluated before deeper children
	sort.Slice(childFiles, func(i, j int) bool {
		if childFiles[i].depth != childFiles[j].depth {
			return childFiles[i].depth < childFiles[j].depth
		}
		return childFiles[i].relDir < childFiles[j].relDir
	})

	// Evaluate all discovered child files
	for i := 0; i < len(childFiles); i++ {
		cf := childFiles[i]
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

		// Discover build = [...] files and subdirs = [...] in this child
		childDir := filepath.Dir(cf.path)
		sDirs, bFiles := eval.ExtractSubdirs(childAst)
		for _, bf := range bFiles {
			bfPath := filepath.Join(childDir, bf)
			if fi, err := os.Stat(bfPath); err == nil && !fi.IsDir() {
				absBf, err := filepath.Abs(bfPath)
				if err == nil && !evaluatedFiles[absBf] {
					childFiles = append(childFiles, childFile{
						path:   bfPath,
						relDir: cf.relDir,
						depth:  cf.depth,
					})
				}
			}
		}
		for _, sd := range sDirs {
			subBp := filepath.Join(childDir, sd, "Android.bp")
			if fi, err := os.Stat(subBp); err == nil && !fi.IsDir() {
				absSd, err := filepath.Abs(subBp)
				if err == nil && !evaluatedFiles[absSd] {
					subRelDir := filepath.Clean(filepath.Join(cf.relDir, sd))
					childFiles = append(childFiles, childFile{
						path:   subBp,
						relDir: subRelDir,
						depth:  strings.Count(subRelDir, string(filepath.Separator)) + 1,
					})
				}
			}
		}
	}

	evalCtx.ResolveGlobalDefaults()

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
		if tc.LD != "" && ldPath == "ld.lld" {
			ldPath = tc.LD
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
	opts.LdPath = ldPath
	if envHidl := os.Getenv("HIDL_GEN"); envHidl != "" && hidlGenPath == "hidl-gen" {
		hidlGenPath = envHidl
	} else if hidlGenPath == "hidl-gen" {
		for _, cand := range []string{
			filepath.Join(topDir, "prebuilts/build-tools/linux-x86/bin/hidl-gen"),
			"/los-21/prebuilts/build-tools/linux-x86/bin/hidl-gen",
			"/opt/android/system/tools/hidl/bin/hidl-gen",
		} {
			if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
				hidlGenPath = cand
				break
			}
		}
	}
	opts.HidlGenPath = hidlGenPath

	if envXsdc := os.Getenv("XSDC"); envXsdc != "" && xsdcPath == "xsdc" {
		xsdcPath = envXsdc
	} else if xsdcPath == "xsdc" {
		for _, cand := range []string{
			filepath.Join(topDir, "prebuilts/host/xsdc/bin/xsdc"),
			"/los-21/prebuilts/host/xsdc/bin/xsdc",
			"/opt/xsdc/bin/xsdc",
		} {
			if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
				xsdcPath = cand
				break
			}
		}
	}
	opts.XsdcPath = xsdcPath

	if envConv := os.Getenv("CONV_LINKER_CONFIG"); envConv != "" && convLinkerConfigPath == "conv_linker_config" {
		convLinkerConfigPath = envConv
	} else if convLinkerConfigPath == "conv_linker_config" {
		for _, cand := range []string{
			filepath.Join(topDir, "out/host/linux-x86/bin/conv_linker_config"),
			"/los-21/out/host/linux-x86/bin/conv_linker_config",
		} {
			if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
				convLinkerConfigPath = cand
				break
			}
		}
	}
	opts.ConvLinkerConfigPath = convLinkerConfigPath

	if envXml := os.Getenv("XMLLINT"); envXml != "" && xmllintPath == "xmllint" {
		xmllintPath = envXml
	}
	opts.XmllintPath = xmllintPath

	if envMkboot := os.Getenv("MKBOOTIMG"); envMkboot != "" && mkbootimgPath == "mkbootimg" {
		mkbootimgPath = envMkboot
	}
	opts.MkbootimgPath = mkbootimgPath

	if envAvb := os.Getenv("AVBTOOL"); envAvb != "" && avbtoolPath == "avbtool" {
		avbtoolPath = envAvb
	}
	opts.AvbtoolPath = avbtoolPath

	if envLp := os.Getenv("LPMAKE"); envLp != "" && lpmakePath == "lpmake" {
		lpmakePath = envLp
	}
	opts.LpmakePath = lpmakePath

	if envCompat := os.Getenv("PROCESS_COMPAT_CONFIG"); envCompat != "" && compatConfigPath == "process-compat-config" {
		compatConfigPath = envCompat
	}
	opts.CompatConfigPath = compatConfigPath

	if envEnc := os.Getenv("JAVA_ENCODING"); envEnc != "" && javaEncoding == "UTF-8" {
		javaEncoding = envEnc
	}
	opts.JavaEncoding = javaEncoding

	// Merge Java/Kotlin environment variables if flags were not explicitly provided
	if len(bootclasspathFlags) == 0 {
		if envBCP := os.Getenv("BOOTCLASSPATH"); envBCP != "" {
			_ = bootclasspathFlags.Set(envBCP)
		}
	}
	if len(prebuiltJarsFlags) == 0 {
		if envPJ := os.Getenv("PREBUILT_JARS"); envPJ != "" {
			_ = prebuiltJarsFlags.Set(envPJ)
		}
	}
	if len(extraJarsFlags) == 0 {
		if envEJ := os.Getenv("EXTRA_JARS"); envEJ != "" {
			_ = extraJarsFlags.Set(envEJ)
		}
	}
	if len(platformClasspathFlags) == 0 {
		if envPC := os.Getenv("PLATFORM_CLASSPATH"); envPC != "" {
			_ = platformClasspathFlags.Set(envPC)
		}
	}
	if androidJarPath == "" {
		if envAJ := os.Getenv("ANDROID_JAR"); envAJ != "" {
			androidJarPath = envAJ
		}
	}
	opts.BootClasspath = bootclasspathFlags
	opts.PrebuiltJarDirs = prebuiltJarsFlags
	opts.ExtraJars = extraJarsFlags
	opts.PlatformClasspath = platformClasspathFlags
	if androidJarPath != "" {
		opts.AndroidJarPath = androidJarPath
	}

	if envCargo := os.Getenv("CARGO"); envCargo != "" && cargoPath == "cargo" {
		cargoPath = envCargo
	}
	opts.Cargo = cargo
	opts.CargoPath = cargoPath
	opts.CargoOutDir = cargoOutDir
	opts.CargoOffline = cargoOffline

	if envBindgen := os.Getenv("BINDGEN"); envBindgen != "" && bindgenPath == "bindgen" {
		bindgenPath = envBindgen
	}
	opts.BindgenPath = bindgenPath

	if envAidl := os.Getenv("AIDL"); envAidl != "" && aidlPath == "aidl" {
		aidlPath = envAidl
	}
	opts.AidlPath = aidlPath

	if envMetalava := os.Getenv("METALAVA"); envMetalava != "" && metalavaPath == "metalava" {
		metalavaPath = envMetalava
	}
	opts.MetalavaPath = metalavaPath

	opts.SkipAbiLevel = skipAbiLevel
	opts.DisableSkipAbis = disableSkipAbis
	opts.IncludeRoot = includeRoot
	opts.AllowMissingDeps = allowMissing
	opts.Siso = siso
	opts.SisoRemote = sisoRemote
	opts.HostCodegenArches = evalCtx.HostCodegenArches
	opts.TargetCodegenArches = evalCtx.TargetCodegenArches
	if activeNDK != nil {
		opts.NDKDir = activeNDK.Path
	}
	opts.BpDir = rootDir
	if sysrootDir != "" && !isHost {
		opts.SysrootDir = sysrootDir
	}
	if prebuiltLibs != "" {
		opts.PrebuiltLibDir = prebuiltLibs
	}
	opts.Prefix = prefix

	// Merge environment variables and CLI extra flags (per-project configuration)
	var combinedCflags []string
	if envCflags := os.Getenv("CFLAGS"); envCflags != "" {
		combinedCflags = append(combinedCflags, strings.Fields(envCflags)...)
	}
	if envCcflags := os.Getenv("CCFLAGS"); envCcflags != "" {
		combinedCflags = append(combinedCflags, strings.Fields(envCcflags)...)
	}
	if extraCflags != "" {
		combinedCflags = append(combinedCflags, strings.Fields(extraCflags)...)
	}
	if extraCcflags != "" {
		combinedCflags = append(combinedCflags, strings.Fields(extraCcflags)...)
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

	var combinedAsflags []string
	if envAsflags := os.Getenv("ASFLAGS"); envAsflags != "" {
		combinedAsflags = append(combinedAsflags, strings.Fields(envAsflags)...)
	}
	if extraAsflags != "" {
		combinedAsflags = append(combinedAsflags, strings.Fields(extraAsflags)...)
	}
	opts.ExtraAsflags = combinedAsflags

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

	if siso {
		starPath := filepath.Join(filepath.Dir(outFile), "siso.star")
		starF, err := os.Create(starPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating %s: %v\n", starPath, err)
			os.Exit(1)
		}
		if err := gen.WriteSisoConfig(starF); err != nil {
			starF.Close()
			fmt.Fprintf(os.Stderr, "Error writing siso config: %v\n", err)
			os.Exit(1)
		}
		starF.Close()
		fmt.Printf("[bp2ninja] Wrote siso config %s (install: mkdir -p build/config/siso && cp %s build/config/siso/main.star; run: siso ninja -C .)\n",
			starPath, starPath)
	}

	fmt.Printf("[bp2ninja] Successfully converted %s -> %s (%d modules)\n",
		bpFile, outFile, len(evalCtx.Modules))
}
