package generator

import (
	"path/filepath"

	"bp2ninja/pkg/eval"
	"bp2ninja/pkg/ninja"
)

// generatePlatformCompatConfig handles the upstream `platform_compat_config` module type (build/soong/java):
// processes compatibility metadata into platform configuration XML files using process-compat-config.
func (g *Generator) generatePlatformCompatConfig(mod *eval.EvaluatedModule) ([]string, error) {
	src := mod.GetString("src")
	if src == "" {
		srcs := mod.GetStringList("srcs")
		if len(srcs) > 0 {
			src = srcs[0]
		}
	}

	srcPath := src
	if mod.Dir != "" && mod.Dir != "." && !filepath.IsAbs(src) {
		srcPath = filepath.Clean(filepath.Join(mod.Dir, src))
	}

	outXml := filepath.Join(g.opts.OutDir, "etc", "compatconfig", mod.Name+".xml")

	if srcPath == "" {
		if g.opts.AllowMissingDeps {
			g.emitPhonyIfNeeded(outXml)
			return []string{outXml}, nil
		}
		return nil, nil
	}

	err := g.nw.Build(ninja.BuildEdge{
		Outputs: []string{outXml},
		Rule:    "process_compat_config",
		Inputs:  []string{srcPath},
		Variables: map[string]string{
			"args": "--jar " + srcPath,
		},
	})

	return []string{outXml}, err
}
