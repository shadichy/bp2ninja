package generator

import (
	"os"
	"path/filepath"
	"strings"

	"bp2ninja/pkg/eval"
	"bp2ninja/pkg/ninja"
)

// autoDeriveFallback is strictly a secondary safety-net fallback when NO plugin
// or standard handler is available for a given module type.
// It relies exclusively on universal AST property heuristics (defaults, commands,
// directory contents, source types, missing dep stamps) with NO repo-specific rules.
func (g *Generator) autoDeriveFallback(mod *eval.EvaluatedModule) ([]string, error) {
	// 1. Defaults modules: provide inherited flags; produce no direct build target
	if strings.HasSuffix(mod.Type, "_defaults") || strings.HasSuffix(mod.Type, "_default") || mod.Type == "defaults" {
		return nil, nil
	}

	// 2. Declarative metadata modules: no direct build outputs
	switch mod.Type {
	case "package", "license", "license_kind", "package_metadata",
		"soong_namespace", "soong_config_module_type", "soong_config_string_variable",
		"soong_config_bool_variable", "soong_config_module_type_import",
		"filegroup", "phony", "ndk_headers", "ndk_library", "vintf_fragment",
		"api_domain", "tree":
		return nil, nil
	}

	// 3. Modules with an explicit command template: treat as generic genrule
	rawCmd := mod.GetString("cmd")
	if rawCmd != "" {
		return g.generateGenrule(mod)
	}

	// 4. Directory collectors / copy modules
	srcDir := mod.GetString("src_dir")
	if srcDir != "" {
		return g.autoDeriveDirCollector(mod, srcDir)
	}

	// 5. Source compilation fallback
	rawSrcs := mod.GetStringList("srcs")
	if len(rawSrcs) > 0 {
		hasCc := false
		hasJava := false
		for _, s := range rawSrcs {
			if strings.HasSuffix(s, ".c") || strings.HasSuffix(s, ".cpp") || strings.HasSuffix(s, ".cc") {
				hasCc = true
				break
			}
			if strings.HasSuffix(s, ".java") || strings.HasSuffix(s, ".kt") {
				hasJava = true
			}
		}
		if hasCc {
			return g.generateCcLibrary(mod)
		}
		if hasJava {
			return g.generateJavaLibrary(mod)
		}
	}

	// 6. Satisfy DAG with phony target if AllowMissingDeps is true
	if g.opts.AllowMissingDeps {
		target := filepath.Join(g.opts.OutDir, "gen", mod.Name, mod.Name+".stamp")
		g.emitPhonyIfNeeded(target)
		return []string{target}, nil
	}

	return nil, nil
}

func (g *Generator) autoDeriveDirCollector(mod *eval.EvaluatedModule, srcDir string) ([]string, error) {
	destDir := mod.GetString("dest_dir")
	targetBase := filepath.Join(g.opts.OutDir, "etc")
	if destDir != "" {
		targetBase = filepath.Join(targetBase, destDir)
	}

	_ = g.nw.Rule(ninja.Rule{
		Name:        "auto_copy_dir",
		Command:     "mkdir -p $$(dirname $out) && cp -f $in $out",
		Description: "COPY $out",
	})

	absSrc := filepath.Join(g.opts.BpDir, srcDir)
	entries, err := os.ReadDir(absSrc)
	var outputs []string
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				srcFile := filepath.Join(absSrc, e.Name())
				outFile := filepath.Join(targetBase, e.Name())
				outputs = append(outputs, outFile)

				_ = g.nw.Build(ninja.BuildEdge{
					Outputs: []string{outFile},
					Rule:    "auto_copy_dir",
					Inputs:  []string{srcFile},
				})
			}
		}
	}

	if len(outputs) == 0 {
		stamp := filepath.Join(targetBase, mod.Name+".stamp")
		outputs = append(outputs, stamp)
		g.emitPhonyIfNeeded(stamp)
	}

	return outputs, nil
}
