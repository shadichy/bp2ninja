package generator

import (
	"path/filepath"
	"strings"

	"bp2ninja/pkg/eval"
	"bp2ninja/pkg/ninja"
)

// generateBootimg handles the upstream `bootimg` module type (build/soong/filesystem):
// packages kernel, ramdisk, and DTB into boot.img or vendor_boot.img using mkbootimg.
func (g *Generator) generateBootimg(mod *eval.EvaluatedModule) ([]string, error) {
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

	if kernel := mod.GetString("kernel_prebuilt"); kernel != "" {
		kernelPath := kernel
		if mod.Dir != "" && mod.Dir != "." && !filepath.IsAbs(kernel) {
			kernelPath = filepath.Clean(filepath.Join(mod.Dir, kernel))
		}
		inputs = append(inputs, kernelPath)
		args = append(args, "--kernel", kernelPath)
	}

	if ramdiskMod := mod.GetString("ramdisk_module"); ramdiskMod != "" {
		if outs, ok := g.moduleOutputs[ramdiskMod]; ok && len(outs) > 0 {
			inputs = append(inputs, outs[0])
			args = append(args, "--ramdisk", outs[0])
		} else if g.opts.AllowMissingDeps {
			phonyRamdisk := filepath.Join(g.opts.OutDir, "images", ramdiskMod+".img")
			g.emitPhonyIfNeeded(phonyRamdisk)
			inputs = append(inputs, phonyRamdisk)
			args = append(args, "--ramdisk", phonyRamdisk)
		}
	}

	if dtb := mod.GetString("dtb_prebuilt"); dtb != "" {
		dtbPath := dtb
		if mod.Dir != "" && mod.Dir != "." && !filepath.IsAbs(dtb) {
			dtbPath = filepath.Clean(filepath.Join(mod.Dir, dtb))
		}
		inputs = append(inputs, dtbPath)
		args = append(args, "--dtb", dtbPath)
	}

	if hv := mod.GetString("header_version"); hv != "" {
		args = append(args, "--header_version", hv)
	}

	if cmdline := mod.GetString("cmdline"); cmdline != "" {
		args = append(args, "--cmdline", "\""+strings.ReplaceAll(cmdline, "\"", "\\\"")+"\"")
	}

	if board := mod.GetString("board_name"); board != "" {
		args = append(args, "--board", "\""+strings.ReplaceAll(board, "\"", "\\\"")+"\"")
	}

	if len(inputs) == 0 && g.opts.AllowMissingDeps {
		g.emitPhonyIfNeeded(outImg)
		return []string{outImg}, nil
	}

	err := g.nw.Build(ninja.BuildEdge{
		Outputs: []string{outImg},
		Rule:    "mkbootimg",
		Inputs:  inputs,
		Variables: map[string]string{
			"args": strings.Join(args, " "),
		},
	})

	return []string{outImg}, err
}
