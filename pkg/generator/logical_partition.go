package generator

import (
	"fmt"
	"path/filepath"
	"strings"

	"bp2ninja/pkg/eval"
	"bp2ninja/pkg/ninja"
)

func getInt64Val(val interface{}) (int64, bool) {
	switch v := val.(type) {
	case int:
		return int64(v), true
	case int32:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		return int64(v), true
	default:
		return 0, false
	}
}

// generateLogicalPartition handles the upstream `logical_partition` module type (build/soong/filesystem):
// generates composite dynamic super partition images using lpmake.
func (g *Generator) generateLogicalPartition(mod *eval.EvaluatedModule) ([]string, error) {
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

	if size, ok := getInt64Val(mod.Properties["size"]); ok && size > 0 {
		args = append(args, fmt.Sprintf("--device-size %d", size))
	} else if sizeStr := mod.GetString("size"); sizeStr != "" {
		args = append(args, "--device-size", sizeStr)
	}

	if metaSize, ok := getInt64Val(mod.Properties["metadata_size"]); ok && metaSize > 0 {
		args = append(args, fmt.Sprintf("--metadata-size %d", metaSize))
	} else if metaSizeStr := mod.GetString("metadata_size"); metaSizeStr != "" {
		args = append(args, "--metadata-size", metaSizeStr)
	}

	if slots, ok := getInt64Val(mod.Properties["metadata_slots"]); ok && slots > 0 {
		args = append(args, fmt.Sprintf("--metadata-slots %d", slots))
	} else if slotsStr := mod.GetString("metadata_slots"); slotsStr != "" {
		args = append(args, "--metadata-slots", slotsStr)
	}

	if mod.GetBool("sparse") {
		args = append(args, "--sparse")
	}

	// Groups
	if rawGroups, ok := mod.Properties["groups"].([]interface{}); ok && len(rawGroups) > 0 {
		for _, raw := range rawGroups {
			grpMap, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			gName, _ := grpMap["name"].(string)
			if gName == "" {
				continue
			}
			if gSize, ok := getInt64Val(grpMap["size"]); ok {
				args = append(args, fmt.Sprintf("--group %s:%d", gName, gSize))
			} else {
				args = append(args, "--group", gName)
			}
		}
	} else {
		group := mod.GetString("default_group")
		if group == "" {
			group = "default"
		}
		if size, ok := getInt64Val(mod.Properties["default_group_size"]); ok && size > 0 {
			args = append(args, fmt.Sprintf("--group %s:%d", group, size))
		} else if size := mod.GetString("default_group_size"); size != "" {
			args = append(args, fmt.Sprintf("--group %s:%s", group, size))
		} else {
			args = append(args, "--group", group)
		}
	}

	// Dynamic partitions list
	if rawParts, ok := mod.Properties["partitions"].([]interface{}); ok {
		for _, raw := range rawParts {
			partMap, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			pName, _ := partMap["name"].(string)
			pImage, _ := partMap["image"].(string)
			if pName == "" {
				continue
			}
			args = append(args, "--partition", fmt.Sprintf("%s:readonly", pName))
			if pImage != "" {
				imgPath := pImage
				if outs, ok := g.moduleOutputs[pImage]; ok && len(outs) > 0 {
					imgPath = outs[0]
				} else if mod.Dir != "" && mod.Dir != "." && !filepath.IsAbs(pImage) {
					imgPath = filepath.Clean(filepath.Join(mod.Dir, pImage))
				}
				inputs = append(inputs, imgPath)
				args = append(args, "--image", fmt.Sprintf("%s=%s", pName, imgPath))
			}
		}
	}

	if len(inputs) == 0 && len(args) == 0 && g.opts.AllowMissingDeps {
		g.emitPhonyIfNeeded(outImg)
		return []string{outImg}, nil
	}

	err := g.nw.Build(ninja.BuildEdge{
		Outputs: []string{outImg},
		Rule:    "lpmake",
		Inputs:  inputs,
		Variables: map[string]string{
			"args": strings.Join(args, " "),
		},
	})

	return []string{outImg}, err
}
