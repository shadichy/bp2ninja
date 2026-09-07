package ndk

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// NDKInfo encapsulates metadata for an installed Android NDK.
type NDKInfo struct {
	Path        string `json:"path"`
	Version     string `json:"version"`
	MajorVer    int    `json:"major_version"`
	IsBeta      bool   `json:"is_beta"`
	LLVMDir     string `json:"llvm_dir"`
	LLVMBinDir  string `json:"llvm_bin_dir"`
	SysrootDir  string `json:"sysroot_dir"`
	Source      string `json:"source"`
}

// ToolchainConfig holds resolved compiler and archiver binaries for a target architecture.
type ToolchainConfig struct {
	CC           string
	CXX          string
	AR           string
	Sysroot      string
	TargetTriple string
	APILevel     int
	NDK          *NDKInfo
}

// DiscoverAll discovers all available Android NDK installations on the host system.
func DiscoverAll() []NDKInfo {
	seen := make(map[string]bool)
	var ndks []NDKInfo

	addCandidate := func(path, source string) {
		if path == "" {
			return
		}
		path = filepath.Clean(path)
		// If path has ~ or relative, expand
		if strings.HasPrefix(path, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				path = filepath.Join(home, path[2:])
			}
		}
		absPath, err := filepath.Abs(path)
		if err != nil {
			absPath = path
		}
		if seen[absPath] {
			return
		}

		info, err := InspectNDK(absPath, source)
		if err == nil && info != nil {
			seen[absPath] = true
			ndks = append(ndks, *info)
		}
	}

	// 1. Environment variables
	envVars := []string{
		"ANDROID_NDK",
		"ANDROID_NDK_HOME",
		"ANDROID_NDK_ROOT",
		"NDK_HOME",
		"NDK_ROOT",
	}
	for _, env := range envVars {
		if val := os.Getenv(env); val != "" {
			addCandidate(val, fmt.Sprintf("env:%s", env))
		}
	}

	// 2. Distro packages (Arch Linux, Debian/Ubuntu, generic Linux)
	distroPaths := []struct {
		path   string
		source string
	}{
		{"/opt/android-ndk", "distro:arch-android-ndk"},
		{"/opt/android-ndk-beta", "distro:arch-android-ndk-beta"},
		{"/usr/lib/android-ndk", "distro:system"},
		{"/usr/local/lib/android-ndk", "distro:local"},
	}
	for _, dp := range distroPaths {
		addCandidate(dp.path, dp.source)
	}

	// 3. Android Studio / Android SDK directories
	var sdkRoots []string
	if val := os.Getenv("ANDROID_HOME"); val != "" {
		sdkRoots = append(sdkRoots, val)
	}
	if val := os.Getenv("ANDROID_SDK_ROOT"); val != "" {
		sdkRoots = append(sdkRoots, val)
	}
	if home, err := os.UserHomeDir(); err == nil {
		sdkRoots = append(sdkRoots,
			filepath.Join(home, "Android/Sdk"),
			filepath.Join(home, ".android/sdk"),
			filepath.Join(home, "Library/Android/sdk"), // macOS
		)
	}
	sdkRoots = append(sdkRoots, "/opt/android-sdk")

	for _, sdk := range sdkRoots {
		ndkDir := filepath.Join(sdk, "ndk")
		if entries, err := os.ReadDir(ndkDir); err == nil {
			for _, e := range entries {
				if e.IsDir() {
					addCandidate(filepath.Join(ndkDir, e.Name()), fmt.Sprintf("studio:%s/ndk/%s", sdk, e.Name()))
				}
			}
		}
	}

	// 4. Look in PATH for ndk-build
	if ndkBuildPath, err := exec.LookPath("ndk-build"); err == nil {
		realPath, err := filepath.EvalSymlinks(ndkBuildPath)
		if err == nil {
			addCandidate(filepath.Dir(realPath), "path:ndk-build")
		} else {
			addCandidate(filepath.Dir(ndkBuildPath), "path:ndk-build")
		}
	}

	// Sort NDKs: Stable latest first, then Beta
	sort.Slice(ndks, func(i, j int) bool {
		if ndks[i].IsBeta != ndks[j].IsBeta {
			return !ndks[i].IsBeta // Stable preferred over Beta by default
		}
		if ndks[i].MajorVer != ndks[j].MajorVer {
			return ndks[i].MajorVer > ndks[j].MajorVer
		}
		return ndks[i].Version > ndks[j].Version
	})

	return ndks
}

// InspectNDK checks if the directory is a valid NDK and extracts its properties.
func InspectNDK(ndkPath, source string) (*NDKInfo, error) {
	fi, err := os.Stat(ndkPath)
	if err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("invalid directory: %s", ndkPath)
	}

	// Detect host LLVM directory
	hostOS := runtime.GOOS
	hostArch := runtime.GOARCH
	if hostArch == "amd64" {
		hostArch = "x86_64"
	}
	hostPrebuilt := fmt.Sprintf("%s-%s", hostOS, hostArch)

	llvmDir := filepath.Join(ndkPath, "toolchains/llvm/prebuilt", hostPrebuilt)
	if _, err := os.Stat(llvmDir); err != nil {
		// Fallback for macOS or cross-distro layouts
		candidates, _ := filepath.Glob(filepath.Join(ndkPath, "toolchains/llvm/prebuilt/*"))
		for _, c := range candidates {
			if fi, err := os.Stat(c); err == nil && fi.IsDir() {
				llvmDir = c
				break
			}
		}
	}

	binDir := filepath.Join(llvmDir, "bin")
	sysrootDir := filepath.Join(llvmDir, "sysroot")

	if _, err := os.Stat(binDir); err != nil {
		return nil, fmt.Errorf("ndk missing llvm/bin: %s", ndkPath)
	}

	// Read version from source.properties
	version := "unknown"
	majorVer := 0
	isBeta := strings.Contains(strings.ToLower(ndkPath), "beta")

	propPath := filepath.Join(ndkPath, "source.properties")
	if f, err := os.Open(propPath); err == nil {
		defer f.Close()
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if strings.HasPrefix(line, "Pkg.Revision") {
				parts := strings.SplitN(line, "=", 2)
				if len(parts) == 2 {
					version = strings.TrimSpace(parts[1])
					if strings.Contains(strings.ToLower(version), "beta") || strings.Contains(strings.ToLower(version), "rc") {
						isBeta = true
					}
					// Parse major version
					verParts := strings.Split(version, ".")
					if len(verParts) > 0 {
						majorVer, _ = strconv.Atoi(verParts[0])
					}
				}
			}
		}
	}

	return &NDKInfo{
		Path:        ndkPath,
		Version:     version,
		MajorVer:    majorVer,
		IsBeta:      isBeta,
		LLVMDir:     llvmDir,
		LLVMBinDir:  binDir,
		SysrootDir:  sysrootDir,
		Source:      source,
	}, nil
}

// ResolveNDK selects the best NDK based on preferred path or version query.
func ResolveNDK(preferredPath, preferredVersion string) (*NDKInfo, error) {
	if preferredPath != "" && preferredPath != "auto" {
		// Explicit path passed by user
		info, err := InspectNDK(preferredPath, "user:flag")
		if err != nil {
			return nil, fmt.Errorf("specified NDK path is invalid (%s): %w", preferredPath, err)
		}
		return info, nil
	}

	all := DiscoverAll()
	if len(all) == 0 {
		return nil, fmt.Errorf("no Android NDK installation found (checked Android Studio SDK, $ANDROID_NDK, and distro packages /opt/android-ndk)")
	}

	if preferredVersion == "" || preferredVersion == "auto" || preferredVersion == "latest" {
		return &all[0], nil
	}

	prefLower := strings.ToLower(preferredVersion)

	// Filter by beta preference
	if prefLower == "beta" {
		for _, n := range all {
			if n.IsBeta {
				return &n, nil
			}
		}
	}

	// Match by version prefix or major version
	for _, n := range all {
		if strings.HasPrefix(strings.ToLower(n.Version), prefLower) || fmt.Sprintf("r%d", n.MajorVer) == prefLower || fmt.Sprintf("%d", n.MajorVer) == prefLower {
			return &n, nil
		}
	}

	// Match by path or source substring
	for _, n := range all {
		if strings.Contains(strings.ToLower(n.Path), prefLower) || strings.Contains(strings.ToLower(n.Source), prefLower) {
			return &n, nil
		}
	}

	// Fallback to top choice
	return &all[0], nil
}

// ArchitectureToTriple maps an Android target architecture to the canonical NDK target triple.
func ArchitectureToTriple(arch string) string {
	switch strings.ToLower(arch) {
	case "arm64", "arm64-v8a", "aarch64":
		return "aarch64-linux-android"
	case "arm", "armeabi-v7a", "armv7a", "armv7-a":
		return "armv7a-linux-androideabi"
	case "x86_64", "x64":
		return "x86_64-linux-android"
	case "x86", "i686":
		return "i686-linux-android"
	case "riscv64":
		return "riscv64-linux-android"
	default:
		return arch + "-linux-android"
	}
}

// GetToolchain resolves compiler and archiver binaries for the given arch and apiLevel.
func (n *NDKInfo) GetToolchain(arch string, apiLevel int) (*ToolchainConfig, error) {
	if apiLevel <= 0 {
		apiLevel = 34 // Default to Android 14 (API 34)
	}

	triple := ArchitectureToTriple(arch)
	binDir := n.LLVMBinDir

	// Look for wrapper compiler (e.g. x86_64-linux-android34-clang)
	wrapperCC := filepath.Join(binDir, fmt.Sprintf("%s%d-clang", triple, apiLevel))
	wrapperCXX := filepath.Join(binDir, fmt.Sprintf("%s%d-clang++", triple, apiLevel))

	var cc, cxx string
	if _, err := os.Stat(wrapperCC); err == nil {
		cc = wrapperCC
	} else {
		// Fallback to generic clang with target
		genericClang := filepath.Join(binDir, "clang")
		if _, err := os.Stat(genericClang); err == nil {
			cc = fmt.Sprintf("%s -target %s%d --sysroot=%s", genericClang, triple, apiLevel, n.SysrootDir)
		} else {
			cc = "clang"
		}
	}

	if _, err := os.Stat(wrapperCXX); err == nil {
		cxx = wrapperCXX
	} else {
		genericClangCxx := filepath.Join(binDir, "clang++")
		if _, err := os.Stat(genericClangCxx); err == nil {
			cxx = fmt.Sprintf("%s -target %s%d --sysroot=%s", genericClangCxx, triple, apiLevel, n.SysrootDir)
		} else {
			cxx = "clang++"
		}
	}

	ar := filepath.Join(binDir, "llvm-ar")
	if _, err := os.Stat(ar); err != nil {
		ar = "llvm-ar"
	}

	return &ToolchainConfig{
		CC:           cc,
		CXX:          cxx,
		AR:           ar,
		Sysroot:      n.SysrootDir,
		TargetTriple: triple,
		APILevel:     apiLevel,
		NDK:          n,
	}, nil
}
