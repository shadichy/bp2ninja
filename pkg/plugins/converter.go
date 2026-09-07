package plugins

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// PluginInfo holds metadata extracted from Soong Go sources.
type PluginInfo struct {
	Name            string
	RegisteredTypes []string
	HasDefaults     bool
	Rules           map[string]string
	Variables       map[string]string
}

var (
	reRegisterModuleType = regexp.MustCompile(`RegisterModuleType\(\s*(?:ctx\s*,\s*)?"([^"]+)"`)
	reHostBinToolVar     = regexp.MustCompile(`HostBinToolVariable\(\s*"([^"]+)",\s*"([^"]+)"\)`)
	reStaticVar          = regexp.MustCompile(`StaticVariable\(\s*"([^"]+)",\s*"([^"]+)"\)`)
	reStaticRule         = regexp.MustCompile(`(?:AndroidStaticRule|StaticRule)\(\s*"([^"]+)",\s*blueprint\.RuleParams\{\s*Command:\s*([` + "`" + `"][^` + "`" + `"]+[` + "`" + `"])`)
)

// FindBp2ninjaDir locates the bp2ninja root directory containing go.mod and pkg/.
func FindBp2ninjaDir(customDir string) (string, error) {
	if customDir != "" {
		if fi, err := os.Stat(customDir); err == nil && fi.IsDir() {
			return filepath.Abs(customDir)
		}
	}
	if envDir := os.Getenv("BP2NINJA_DIR"); envDir != "" {
		if fi, err := os.Stat(envDir); err == nil && fi.IsDir() {
			return filepath.Abs(envDir)
		}
	}

	execPath, err := os.Executable()
	if err == nil {
		execDir := filepath.Dir(execPath)
		// 1. In source tree: bin/../pkg
		parent := filepath.Dir(execDir)
		if fi, err := os.Stat(filepath.Join(parent, "pkg")); err == nil && fi.IsDir() {
			return filepath.Abs(parent)
		}
		// 2. Installed: /usr/bin/../share/bp2ninja
		shareDir := filepath.Join(parent, "share", "bp2ninja")
		if fi, err := os.Stat(filepath.Join(shareDir, "pkg")); err == nil && fi.IsDir() {
			return filepath.Abs(shareDir)
		}
	}

	// System fallbacks
	for _, cand := range []string{"/usr/share/bp2ninja", "/usr/local/share/bp2ninja"} {
		if fi, err := os.Stat(filepath.Join(cand, "pkg")); err == nil && fi.IsDir() {
			return cand, nil
		}
	}

	// Current directory check
	if fi, err := os.Stat("pkg"); err == nil && fi.IsDir() {
		return filepath.Abs(".")
	}

	return "", fmt.Errorf("could not locate bp2ninja module root (checked BP2NINJA_DIR, executable parent, and /usr/share/bp2ninja)")
}

// AnalyzeGoSources scans targetPath (.go file or directory) and extracts registered module types and rules.
func AnalyzeGoSources(targetPath string) (*PluginInfo, error) {
	fi, err := os.Stat(targetPath)
	if err != nil {
		return nil, err
	}

	var goFiles []string
	if fi.IsDir() {
		err = filepath.Walk(targetPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if !info.IsDir() && strings.HasSuffix(info.Name(), ".go") && !strings.HasSuffix(info.Name(), "_test.go") {
				goFiles = append(goFiles, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	} else if strings.HasSuffix(targetPath, ".go") {
		goFiles = append(goFiles, targetPath)
	}

	if len(goFiles) == 0 {
		return nil, fmt.Errorf("no .go source files found in %s", targetPath)
	}

	info := &PluginInfo{
		Name:      filepath.Base(targetPath),
		Rules:     make(map[string]string),
		Variables: make(map[string]string),
	}
	typeSet := make(map[string]bool)

	for _, f := range goFiles {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		content := string(data)

		// 1. Registered Module Types
		for _, m := range reRegisterModuleType.FindAllStringSubmatch(content, -1) {
			if len(m) > 1 && !typeSet[m[1]] {
				typeSet[m[1]] = true
				info.RegisteredTypes = append(info.RegisteredTypes, m[1])
				if strings.HasSuffix(m[1], "_defaults") || strings.HasSuffix(m[1], "_default") {
					info.HasDefaults = true
				}
			}
		}

		// 2. HostBinToolVariable & StaticVariable
		for _, m := range reHostBinToolVar.FindAllStringSubmatch(content, -1) {
			if len(m) > 2 {
				info.Variables[m[1]] = m[2]
			}
		}
		for _, m := range reStaticVar.FindAllStringSubmatch(content, -1) {
			if len(m) > 2 {
				info.Variables[m[1]] = m[2]
			}
		}

		// 3. Rules & Commands
		for _, m := range reStaticRule.FindAllStringSubmatch(content, -1) {
			if len(m) > 2 {
				rname := m[1]
				cmd := strings.Trim(m[2], "`\"")
				for k, v := range info.Variables {
					cmd = strings.ReplaceAll(cmd, fmt.Sprintf("${%s}", k), v)
					cmd = strings.ReplaceAll(cmd, fmt.Sprintf("$%s", k), v)
				}
				info.Rules[rname] = cmd
			}
		}
	}

	if len(info.RegisteredTypes) == 0 {
		return nil, fmt.Errorf("no registered Soong module types found in %s", targetPath)
	}

	return info, nil
}

// GenerateAdapterSource produces the loadable Go plugin source code.
func GenerateAdapterSource(info *PluginInfo) string {
	baseName := info.Name
	if baseName == "" || baseName == "." || baseName == "src" || baseName == "build" {
		if len(info.RegisteredTypes) > 0 {
			baseName = info.RegisteredTypes[0]
		} else {
			baseName = "Custom"
		}
	}

	var sb strings.Builder
	for _, r := range baseName {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('_')
		}
	}
	parts := strings.Split(sb.String(), "_")
	var titleParts []string
	for _, p := range parts {
		if p != "" {
			titleParts = append(titleParts, strings.Title(strings.ToLower(p)))
		}
	}
	structName := strings.Join(titleParts, "") + "ModuleHandler"

	var typeLiterals []string
	for _, t := range info.RegisteredTypes {
		typeLiterals = append(typeLiterals, fmt.Sprintf("%q", t))
	}

	cmdStr := ""
	for _, c := range info.Rules {
		cmdStr = c
		break
	}
	if cmdStr == "" {
		cmdStr = `bash -c "mkdir -p $$(dirname $out) && touch $out"`
	}

	return fmt.Sprintf(`package main

import (
	"path/filepath"
	"strings"

	"bp2ninja/pkg/ninja"
	"bp2ninja/pkg/plugins"
)

type %s struct{}

func (h *%s) SupportedTypes() []string {
	return []string{%s}
}

func (h *%s) HandleModule(ctx *plugins.PluginContext) ([]string, error) {
	if strings.HasSuffix(ctx.ModuleType, "_defaults") || strings.HasSuffix(ctx.ModuleType, "_default") {
		return nil, nil
	}

	target := filepath.Join(ctx.OutDir, "gen", ctx.ModuleName, ctx.ModuleName+".stamp")
	cleanName := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '.' || r == '-' {
			return r
		}
		return '_'
	}, ctx.ModuleName)
	ruleName := "plugin_cmd_" + cleanName

	_ = ctx.NinjaWriter.Rule(ninja.Rule{
		Name:        ruleName,
		Command:     %q,
		Description: "PLUGIN $out",
	})

	srcs := ctx.GetStringList("srcs")
	var inputs []string
	for _, s := range srcs {
		p := ctx.ResolvePath(s)
		inputs = append(inputs, p)
		ctx.EmitPhonyIfNeeded(p)
	}

	err := ctx.NinjaWriter.Build(ninja.BuildEdge{
		Outputs: []string{target},
		Rule:    ruleName,
		Inputs:  inputs,
	})
	return []string{target}, err
}

var Handler plugins.ModuleHandler = &%s{}
`, structName, structName, strings.Join(typeLiterals, ", "), structName, cmdStr, structName)
}

// ConvertPlugin converts Soong plugin sources into a loadable bp2ninja .so plugin.
func ConvertPlugin(targetPath, outSoPath, customBp2ninjaDir string) error {
	info, err := AnalyzeGoSources(targetPath)
	if err != nil {
		return err
	}

	fmt.Printf("[+] Found %d module types in %s: %v\n", len(info.RegisteredTypes), targetPath, info.RegisteredTypes)

	bp2ninjaDir, err := FindBp2ninjaDir(customBp2ninjaDir)
	if err != nil {
		return err
	}

	if outSoPath == "" {
		outSoPath = filepath.Join(bp2ninjaDir, "plugins", info.Name+".so")
	}
	absOutSo, err := filepath.Abs(outSoPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(absOutSo), 0755); err != nil {
		return err
	}

	tmpDir, err := os.MkdirTemp("", "bp2ninja_plugin_*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	goModSrc := filepath.Join(bp2ninjaDir, "go.mod")
	pkgSrc := filepath.Join(bp2ninjaDir, "pkg")

	if fi, err := os.Stat(goModSrc); err == nil && !fi.IsDir() {
		_ = os.Symlink(goModSrc, filepath.Join(tmpDir, "go.mod"))
	}
	if fi, err := os.Stat(pkgSrc); err == nil && fi.IsDir() {
		_ = os.Symlink(pkgSrc, filepath.Join(tmpDir, "pkg"))
	}

	adapterCode := GenerateAdapterSource(info)
	adapterFile := filepath.Join(tmpDir, "plugin.go")
	if err := os.WriteFile(adapterFile, []byte(adapterCode), 0644); err != nil {
		return err
	}

	fmt.Printf("    Compiling plugin to: %s...\n", absOutSo)
	cmd := exec.Command("go", "build", "-trimpath", "-buildmode=plugin", "-o", absOutSo, "plugin.go")
	cmd.Dir = tmpDir
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("plugin build failed: %v\n%s", err, string(out))
	}

	if fi, err := os.Stat(absOutSo); err == nil {
		sizeMb := float64(fi.Size()) / (1024 * 1024)
		fmt.Printf("[OK] Successfully built plugin: %s (%.1f MB)\n", absOutSo, sizeMb)
	}
	return nil
}
