package generator

import (
	"path/filepath"
	"strings"

	"bp2ninja/pkg/eval"
	"bp2ninja/pkg/ninja"
)

// generateVbmeta handles the upstream `vbmeta` and `avb_gen_vbmeta_image` module types (build/soong/filesystem):
// constructs Android Verified Boot metadata descriptors and output vbmeta.img using avbtool.
func (g *Generator) generateVbmeta(mod *eval.EvaluatedModule) ([]string, error) {
	stem := mod.GetString("stem")
	if stem == "" {
		stem = mod.Name + ".img"
	}
	if !strings.HasSuffix(stem, ".img") {
		stem += ".img"
	}

	outImg := filepath.Join(g.opts.OutDir, "images", stem)

	var inputs []string
	var args []string

	partName := mod.GetString("partition_name")
	if partName == "" {
		partName = strings.TrimSuffix(stem, ".img")
	}
	args = append(args, "--partition_name", partName)

	if key := mod.GetString("key"); key != "" {
		keyPath := key
		if mod.Dir != "" && mod.Dir != "." && !filepath.IsAbs(key) {
			keyPath = filepath.Clean(filepath.Join(mod.Dir, key))
		}
		inputs = append(inputs, keyPath)
		args = append(args, "--key", keyPath)
	}

	if alg := mod.GetString("algorithm"); alg != "" {
		args = append(args, "--algorithm", alg)
	}

	for _, img := range mod.GetStringList("include_descriptors_from_images") {
		imgPath := img
		if outs, ok := g.moduleOutputs[img]; ok && len(outs) > 0 {
			imgPath = outs[0]
		} else if mod.Dir != "" && mod.Dir != "." && !filepath.IsAbs(img) {
			imgPath = filepath.Clean(filepath.Join(mod.Dir, img))
		}
		inputs = append(inputs, imgPath)
		args = append(args, "--include_descriptors_from_image", imgPath)
	}

	if len(inputs) == 0 && g.opts.AllowMissingDeps {
		g.emitPhonyIfNeeded(outImg)
		return []string{outImg}, nil
	}

	err := g.nw.Build(ninja.BuildEdge{
		Outputs: []string{outImg},
		Rule:    "avbtool_vbmeta",
		Inputs:  inputs,
		Variables: map[string]string{
			"args": strings.Join(args, " "),
		},
	})

	return []string{outImg}, err
}
