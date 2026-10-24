package generator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"bp2ninja/pkg/eval"
	"bp2ninja/pkg/ninja"
)

// generateBpf implements the upstream `bpf` module type (build/soong/bpf):
// each source is compiled to a BPF ELF object with `clang --target=bpf`
// against the Bionic UAPI headers, optionally stripped of BTF relocation
// sections (default), and staged under out/etc/bpf[/sub_dir].
// The objects are consumed by APEX payloads (e.g. Connectivity/tethering).
func (g *Generator) generateBpf(mod *eval.EvaluatedModule) ([]string, error) {
	srcs := g.ResolveSrcs(mod)
	if len(srcs) == 0 {
		if g.opts.AllowMissingDeps {
			target := filepath.Join(g.opts.OutDir, "gen", mod.Name, mod.Name+".stamp")
			g.emitPhonyIfNeeded(target)
			return []string{target}, nil
		}
		return nil, nil
	}

	// btf defaults to true in upstream Soong
	btf := true
	if v, ok := mod.Properties["btf"].(bool); ok {
		btf = v
	}

	// Canonical BPF cflags mirrored from build/soong/bpf/bpf.go
	cflags := []string{
		"-nostdlibinc",
		"-no-canonical-prefixes",
		"-O2", "-Wall", "-Werror", "-Wextra",
		"-isystem", "bionic/libc/include",
		"-isystem", "bionic/libc/kernel/uapi",
		"-isystem", "bionic/libc/kernel/uapi/asm-arm64",
		"-isystem", "bionic/libc/kernel/android/uapi",
		"-I", "packages/modules/Connectivity/bpf/headers/include",
		"-I", "system/core/libcutils/include",
		"-I", modDirOrDot(mod.Dir),
	}
	for _, dir := range mod.GetStringList("local_include_dirs") {
		incPath := dir
		if mod.Dir != "" && mod.Dir != "." && !filepath.IsAbs(dir) {
			incPath = filepath.Clean(filepath.Join(mod.Dir, dir))
		}
		cflags = append(cflags, "-I", incPath)
	}
	for _, dir := range mod.GetStringList("include_dirs") {
		incPath := dir
		if g.opts.TopDir != "" && g.opts.TopDir != "." && !filepath.IsAbs(dir) {
			incPath = filepath.Join(g.opts.TopDir, dir)
		}
		cflags = append(cflags, "-I", incPath)
	}
	cflags = append(cflags, mod.GetStringList("cflags")...)
	if btf {
		cflags = append(cflags, "-g", "-fdebug-prefix-map=/proc/self/cwd=")
	}
	cflags = dedup(cflags)

	installDir := filepath.Join(g.opts.OutDir, "etc", "bpf")
	if sub := mod.GetString("sub_dir"); sub != "" {
		installDir = filepath.Join(installDir, sub)
	}

	objDir := g.objDir(mod.Name + ".bpf")
	var outputs []string
	for _, src := range srcs {
		base := filepath.Base(src)
		if strings.ContainsRune(base, '_') {
			fmt.Fprintf(os.Stderr, "warning: bpf module %s: invalid character '_' in source name %s (upstream Soong rejects this)\n", mod.Name, base)
		}
		obj := filepath.Join(objDir, strings.TrimSuffix(base, filepath.Ext(base))+".o")

		_ = g.nw.Build(ninja.BuildEdge{
			Outputs:   []string{obj},
			Rule:      "bpf_compile",
			Inputs:    []string{src},
			Variables: map[string]string{"cflags": shellJoin(cflags)},
		})

		out := obj
		staged := filepath.Join(installDir, filepath.Base(obj))
		if btf {
			// Strip BTF relocation sections like upstream llvm-strip rule.
			_ = g.nw.Build(ninja.BuildEdge{
				Outputs: []string{staged},
				Rule:    "bpf_strip",
				Inputs:  []string{obj},
			})
		} else {
			_ = g.nw.Build(ninja.BuildEdge{
				Outputs: []string{staged},
				Rule:    "copy",
				Inputs:  []string{obj},
			})
		}
		out = staged
		outputs = append(outputs, out)
	}
	return outputs, nil
}

func modDirOrDot(dir string) string {
	if dir == "" {
		return "."
	}
	return dir
}
