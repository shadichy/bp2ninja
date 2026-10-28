package generator

import (
	"path/filepath"
	"strings"

	"bp2ninja/pkg/eval"
	"bp2ninja/pkg/ninja"
)

// generateJavaSystemModules handles the upstream `java_system_modules` module type (build/soong/java):
// converts a collection of bootclasspath jar libraries into an OpenJDK 9+ modular runtime system image.
func (g *Generator) generateJavaSystemModules(mod *eval.EvaluatedModule) ([]string, error) {
	libs := mod.GetStringList("libs")
	var inputs []string

	for _, lib := range libs {
		if outs, ok := g.moduleOutputs[lib]; ok && len(outs) > 0 {
			inputs = append(inputs, outs[0])
		} else {
			jarPath := filepath.Join(g.libDir(), lib+".jar")
			inputs = append(inputs, jarPath)
			if g.opts.AllowMissingDeps {
				g.emitPhonyIfNeeded(jarPath)
			}
		}
	}

	outDir := filepath.Join(g.opts.OutDir, "system_modules", mod.Name)

	if len(inputs) == 0 && g.opts.AllowMissingDeps {
		g.emitPhonyIfNeeded(outDir)
		return []string{outDir}, nil
	}

	modulePath := strings.Join(inputs, ":")

	err := g.nw.Build(ninja.BuildEdge{
		Outputs: []string{outDir},
		Rule:    "system_modules_jlink",
		Inputs:  inputs,
		Variables: map[string]string{
			"module_path": modulePath,
			"add_modules": "java.base",
		},
	})

	return []string{outDir}, err
}
