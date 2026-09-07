package gowork

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

type PackageMapping struct {
	RelTree    string
	GitURL     string
	ModuleName string
}

var DefaultMappings = map[string]PackageMapping{
	"android/soong": {
		RelTree:    "build/soong",
		GitURL:     "https://android.googlesource.com/platform/build/soong",
		ModuleName: "android/soong",
	},
	"github.com/google/blueprint": {
		RelTree:    "build/blueprint",
		GitURL:     "https://android.googlesource.com/platform/build/blueprint",
		ModuleName: "github.com/google/blueprint",
	},
	"google.golang.org/protobuf": {
		RelTree:    "external/golang-protobuf",
		GitURL:     "https://android.googlesource.com/platform/external/golang-protobuf",
		ModuleName: "google.golang.org/protobuf",
	},
	"go.starlark.net": {
		RelTree:    "external/starlark-go",
		GitURL:     "https://android.googlesource.com/platform/external/starlark-go",
		ModuleName: "go.starlark.net",
	},
}

// FindAospRoot discovers the Android source tree root if present.
func FindAospRoot(argTree string) string {
	if argTree != "" {
		if fi, err := os.Stat(argTree); err == nil && fi.IsDir() {
			abs, _ := filepath.Abs(argTree)
			return abs
		}
	}
	if top := os.Getenv("ANDROID_BUILD_TOP"); top != "" {
		if fi, err := os.Stat(top); err == nil && fi.IsDir() {
			abs, _ := filepath.Abs(top)
			return abs
		}
	}

	cwd, _ := os.Getwd()
	curr := cwd
	for {
		if fi, err := os.Stat(filepath.Join(curr, "build", "soong")); err == nil && fi.IsDir() {
			return curr
		}
		// Look into subdirectories
		entries, err := os.ReadDir(curr)
		if err == nil {
			for _, e := range entries {
				if e.IsDir() {
					cand := filepath.Join(curr, e.Name(), "build", "soong")
					if fi, err := os.Stat(cand); err == nil && fi.IsDir() {
						return filepath.Join(curr, e.Name())
					}
				}
			}
		}
		parent := filepath.Dir(curr)
		if parent == curr {
			break
		}
		curr = parent
	}
	return ""
}

// GetGoVersion returns Go minor version like "1.21" or "1.22" for go.work header.
func GetGoVersion() string {
	cmd := exec.Command("go", "version")
	out, err := cmd.Output()
	if err == nil {
		re := regexp.MustCompile(`go(\d+\.\d+)`)
		m := re.FindStringSubmatch(string(out))
		if len(m) > 1 {
			return m[1]
		}
	}
	return "1.21"
}

// GenerateGoWork generates go.work resolving android/* packages.
func GenerateGoWork(treeDir, workDir string, remote bool) error {
	if workDir == "" {
		workDir = "."
	}
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		return err
	}

	aospRoot := FindAospRoot(treeDir)
	if aospRoot != "" {
		fmt.Printf("[*] Found Android source tree at: %s\n", aospRoot)
	}

	depsDir := filepath.Join(absWorkDir, ".deps")
	var useDirs []string
	useDirs = append(useDirs, ".")

	for pkg, mapping := range DefaultMappings {
		var resolvedPath string

		// 1. Check local AOSP tree
		if aospRoot != "" {
			cand := filepath.Join(aospRoot, mapping.RelTree)
			if fi, err := os.Stat(cand); err == nil && fi.IsDir() {
				resolvedPath = cand
				fmt.Printf("[+] Resolved %s -> %s (local tree)\n", pkg, cand)
			}
		}

		// 2. Remote clone mode
		if resolvedPath == "" && remote {
			targetDir := filepath.Join(depsDir, filepath.Base(mapping.RelTree))
			gitDir := filepath.Join(targetDir, ".git")
			if fi, err := os.Stat(gitDir); err == nil && fi.IsDir() {
				resolvedPath = targetDir
				fmt.Printf("[+] Using cached remote repo: %s -> %s\n", pkg, targetDir)
			} else {
				fmt.Printf("[*] Cloning %s -> %s...\n", mapping.GitURL, targetDir)
				_ = os.MkdirAll(filepath.Dir(targetDir), 0755)
				cloneCmd := exec.Command("git", "clone", "--depth=1", mapping.GitURL, targetDir)
				cloneCmd.Stdout = os.Stdout
				cloneCmd.Stderr = os.Stderr
				if err := cloneCmd.Run(); err == nil {
					resolvedPath = targetDir
					fmt.Printf("[OK] Successfully cloned %s\n", pkg)
				} else {
					fmt.Printf("[-] Failed to clone %s: %v\n", mapping.GitURL, err)
				}
			}
		}

		if resolvedPath != "" {
			// Ensure go.mod exists in target
			modFile := filepath.Join(resolvedPath, "go.mod")
			if _, err := os.Stat(modFile); os.IsNotExist(err) {
				initMod := fmt.Sprintf("module %s\n\ngo 1.21\n", mapping.ModuleName)
				_ = os.WriteFile(modFile, []byte(initMod), 0644)
				fmt.Printf("    Created minimal %s for %s\n", modFile, mapping.ModuleName)
			}

			// Add relative path from workDir to resolvedPath
			relPath, err := filepath.Rel(absWorkDir, resolvedPath)
			if err != nil {
				relPath = resolvedPath
			}
			useDirs = append(useDirs, relPath)
		} else if !remote && aospRoot == "" {
			fmt.Printf("[-] Unresolved package %s (specify --tree or --remote)\n", pkg)
		}
	}

	goVer := GetGoVersion()
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("go %s\n\nuse (\n", goVer))
	for _, d := range useDirs {
		sb.WriteString(fmt.Sprintf("\t%s\n", d))
	}
	sb.WriteString(")\n")

	outFile := filepath.Join(absWorkDir, "go.work")
	if err := os.WriteFile(outFile, []byte(sb.String()), 0644); err != nil {
		return err
	}

	fmt.Printf("[OK] Successfully generated %s with %d directories.\n", outFile, len(useDirs))
	return nil
}

// CleanGoWork removes go.work and go.work.sum.
func CleanGoWork(workDir string) error {
	if workDir == "" {
		workDir = "."
	}
	f1 := filepath.Join(workDir, "go.work")
	f2 := filepath.Join(workDir, "go.work.sum")
	_ = os.Remove(f1)
	_ = os.Remove(f2)
	fmt.Println("[OK] Cleaned go.work and go.work.sum")
	return nil
}
