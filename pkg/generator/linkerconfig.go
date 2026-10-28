package generator

import (
	"path/filepath"

	"bp2ninja/pkg/eval"
	"bp2ninja/pkg/ninja"
)

// generateLinkerConfig handles the upstream `linker_config` module type (build/soong/linkerconfig):
// converts human-readable linker.config.json manifests into binary protobuf linker.config.pb
// consumed by the Bionic dynamic linker (linker64) for namespace isolation.
func (g *Generator) generateLinkerConfig(mod *eval.EvaluatedModule) ([]string, error) {
	src := mod.GetString("src")
	if src == "" {
		src = "linker.config.json"
	}

	srcPath := src
	if mod.Dir != "" && mod.Dir != "." && !filepath.IsAbs(src) {
		srcPath = filepath.Clean(filepath.Join(mod.Dir, src))
	}

	genOut := filepath.Join(g.opts.OutDir, "gen", mod.Name, "linker.config.pb")

	err := g.nw.Build(ninja.BuildEdge{
		Outputs: []string{genOut},
		Rule:    "conv_linker_config",
		Inputs:  []string{srcPath},
	})
	if err != nil {
		return nil, err
	}

	targets := []string{genOut}

	// If installable (defaults to true), stage to out/etc/linker.config.pb
	installable := true
	if b, ok := mod.Properties["installable"].(bool); ok {
		installable = b
	}
	if installable {
		installOut := filepath.Join(g.opts.OutDir, "etc", "linker.config.pb")
		err = g.nw.Build(ninja.BuildEdge{
			Outputs: []string{installOut},
			Rule:    "copy",
			Inputs:  []string{genOut},
		})
		if err == nil {
			targets = append(targets, installOut)
		}
	}

	return targets, nil
}
