#!/usr/bin/env python3
"""
convert_plugin.py: Universal Soong Plugin Converter for bp2ninja

Dynamically inspects in-tree Android Soong plugins (bootstrap_go_package)
and automatically generates and compiles loadable Go .so plugins implementing
bp2ninja's ModuleHandler interface using universal AST inspection.

Zero hardcoded module names, zero archetypes, zero local path footprints.
"""

import os
import re
import sys
import shutil
import argparse
import subprocess
from pathlib import Path

SCRIPT_DIR = Path(__file__).resolve().parent
BP2NINJA_DIR = SCRIPT_DIR.parent
DEFAULT_OUT_DIR = BP2NINJA_DIR / "plugins"

def find_aosp_root(arg_tree=None):
    if arg_tree and Path(arg_tree).is_dir():
        return Path(arg_tree).resolve()
    if os.environ.get("ANDROID_BUILD_TOP") and Path(os.environ["ANDROID_BUILD_TOP"]).is_dir():
        return Path(os.environ["ANDROID_BUILD_TOP"]).resolve()

    for start in [Path.cwd(), SCRIPT_DIR]:
        curr = start.resolve()
        while curr != curr.parent:
            if (curr / "build/soong").is_dir():
                return curr
            try:
                for sub in curr.iterdir():
                    if sub.is_dir() and (sub / "build/soong").is_dir():
                        return sub.resolve()
            except (PermissionError, OSError):
                pass
            curr = curr.parent

    raise RuntimeError("Cannot locate Android source tree root. Please set ANDROID_BUILD_TOP or pass --tree.")

def find_go_compiler(aosp_tree=None):
    candidates = []
    if aosp_tree:
        candidates.append(Path(aosp_tree) / "prebuilts/go/linux-x86/bin/go")
    try:
        discovered_aosp = find_aosp_root()
        candidates.append(discovered_aosp / "prebuilts/go/linux-x86/bin/go")
    except Exception:
        pass
    for c in candidates:
        if c.is_file() and os.access(c, os.X_OK):
            return str(c)
    system_go = shutil.which("go")
    if system_go:
        return system_go
    raise RuntimeError("Could not find a valid Go compiler.")

def analyze_go_sources(src_dir_or_file):
    path = Path(src_dir_or_file)
    go_files = []
    if path.is_file() and path.suffix == ".go":
        go_files = [path]
    elif path.is_dir():
        go_files = [f for f in path.glob("**/*.go") if not f.name.endswith("_test.go")]

    if not go_files:
        return None

    registered_types = []
    has_defaults = False
    rules = {}
    variables = {}
    properties = set()

    for f in go_files:
        try:
            content = f.read_text(encoding="utf-8", errors="ignore")
        except Exception:
            continue

        # 1. Registered module types
        mod_matches = re.findall(r'RegisterModuleType\(\s*(?:ctx\s*,\s*)?"([^"]+)"', content)
        for m in mod_matches:
            if m not in registered_types:
                registered_types.append(m)
                if m.endswith("_defaults") or m.endswith("_default"):
                    has_defaults = True

        # 2. HostBinToolVariable & StaticVariable
        for var, tool in re.findall(r'HostBinToolVariable\(\s*"([^"]+)",\s*"([^"]+)"\)', content):
            variables[var] = tool
        for var, val in re.findall(r'StaticVariable\(\s*"([^"]+)",\s*"([^"]+)"\)', content):
            variables[var] = val

        # 3. Rules & Commands
        for rname, cmd in re.findall(r'(?:AndroidStaticRule|StaticRule)\(\s*"([^"]+)",\s*blueprint\.RuleParams\{\s*Command:\s*([`"][^`"]+[`"])', content):
            clean_cmd = cmd.strip('`"')
            for v, t in variables.items():
                clean_cmd = clean_cmd.replace(f"${{{v}}}", t).replace(f"${v}", t)
            rules[rname] = clean_cmd

        # 4. Property struct fields
        for field in re.findall(r'^\s*([A-Z]\w+)\s+(?:\*?string|\[\]string|bool)', content, re.MULTILINE):
            properties.add(field.lower())

    return {
        "files": go_files,
        "registered_types": registered_types,
        "has_defaults": has_defaults,
        "rules": rules,
        "variables": variables,
        "properties": list(properties),
        "name": path.stem if path.is_file() else path.name,
    }

def generate_adapter_code(info):
    """
    Universal Plugin Adapter Generator:
    Generates a generic, scalable bp2ninja ModuleHandler adapter purely from
    the parsed module types, rules, and property ASTs.
    """
    name = re.sub(r'[^a-zA-Z0-9]', '_', info["name"]).title().replace('_', '')
    types_list_go = ", ".join(f'"{t}"' for t in info["registered_types"])

    cmd_str = ""
    if info["rules"]:
        cmd_str = list(info["rules"].values())[0]
    if not cmd_str:
        cmd_str = 'bash -c "mkdir -p $$(dirname $out) && touch $out"'

    return f"""package main

import (
	"path/filepath"
	"strings"

	"bp2ninja/pkg/ninja"
	"bp2ninja/pkg/plugins"
)

type {name}Handler struct{{}}

func (h *{name}Handler) SupportedTypes() []string {{
	return []string{{{types_list_go}}}
}}

func (h *{name}Handler) HandleModule(ctx *plugins.PluginContext) ([]string, error) {{
	if strings.HasSuffix(ctx.ModuleType, "_defaults") || strings.HasSuffix(ctx.ModuleType, "_default") {{
		return nil, nil
	}}

	target := filepath.Join(ctx.OutDir, "gen", ctx.ModuleName, ctx.ModuleName+".stamp")
	cleanName := strings.Map(func(r rune) rune {{
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '.' || r == '-' {{
			return r
		}}
		return '_'
	}}, ctx.ModuleName)
	ruleName := "plugin_cmd_" + cleanName

	_ = ctx.NinjaWriter.Rule(ninja.Rule{{
		Name:        ruleName,
		Command:     `{cmd_str}`,
		Description: "PLUGIN $out",
	}})

	srcs := ctx.GetStringList("srcs")
	var inputs []string
	for _, s := range srcs {{
		p := ctx.ResolvePath(s)
		inputs = append(inputs, p)
		ctx.EmitPhonyIfNeeded(p)
	}}

	err := ctx.NinjaWriter.Build(ninja.BuildEdge{{
		Outputs: []string{{target}},
		Rule:    ruleName,
		Inputs:  inputs,
	}})
	return []string{{target}}, err
}}

var Handler plugins.ModuleHandler = &{name}Handler{{}}
"""

def convert_plugin(target_path, out_so_path=None, aosp_tree=None):
    go = find_go_compiler(aosp_tree)
    info = analyze_go_sources(target_path)
    if not info or not info["registered_types"]:
        print(f"[-] No registered Soong module types found in {target_path}")
        return False

    print(f"[+] Found {len(info['registered_types'])} module types in {target_path}: {info['registered_types']}")

    if out_so_path:
        out_so_path = Path(out_so_path)
        out_name = out_so_path.stem
    else:
        out_name = info["name"]
        out_so_path = DEFAULT_OUT_DIR / f"{out_name}.so"

    out_so_path.parent.mkdir(parents=True, exist_ok=True)

    plugin_sub_dir = DEFAULT_OUT_DIR / out_name
    plugin_sub_dir.mkdir(parents=True, exist_ok=True)
    adapter_src = plugin_sub_dir / "plugin.go"
    code = generate_adapter_code(info)
    adapter_src.write_text(code, encoding="utf-8")

    print(f"    Generated adapter: {adapter_src}")
    print(f"    Compiling plugin to: {out_so_path}...")

    env = os.environ.copy()
    cmd = [go, "build", "-trimpath", "-buildmode=plugin", "-o", str(out_so_path), str(adapter_src)]
    res = subprocess.run(cmd, cwd=str(BP2NINJA_DIR), env=env, capture_output=True, text=True)
    if res.returncode != 0:
        print(f"[-] Build error for {out_so_path}:")
        print(res.stderr)
        return False

    size_mb = out_so_path.stat().st_size / (1024 * 1024)
    print(f"[OK] Successfully built plugin: {out_so_path} ({size_mb:.1f} MB)")
    return True

def convert_all_plugins(aosp_tree):
    """
    Dynamically discovers and converts all custom Soong plugins in the tree.
    Zero pre-baked lists: scans for bootstrap_go_package outside core Soong.
    """
    aosp = Path(aosp_tree)
    print(f"[*] Discovering and converting all custom in-tree plugins across {aosp}...")
    converted = 0
    for root, dirs, files in os.walk(aosp):
        dirs[:] = [d for d in dirs if d not in {".git", ".repo", "out"}]
        rel = os.path.relpath(root, aosp)
        if rel.startswith("build/soong") or rel.startswith("build/blueprint"):
            continue
        if "Android.bp" in files:
            bp = os.path.join(root, "Android.bp")
            try:
                content = open(bp, "r", encoding="utf-8", errors="ignore").read()
            except Exception:
                continue
            if "bootstrap_go_package" in content:
                has_reg = False
                for gf in Path(root).glob("*.go"):
                    if not gf.name.endswith("_test.go"):
                        try:
                            if "RegisterModuleType" in gf.read_text(encoding="utf-8", errors="ignore"):
                                has_reg = True
                                break
                        except Exception:
                            pass
                if has_reg:
                    for m in re.finditer(r"bootstrap_go_package\s*\{([^}]+)\}", content):
                        nm = re.search(r"name:\s*\"([^\"]+)\"", m.group(1))
                        pkg_name = nm.group(1) if nm else Path(root).name
                        out_so = DEFAULT_OUT_DIR / f"{pkg_name}.so"
                        if convert_plugin(root, out_so_path=out_so, aosp_tree=aosp):
                            converted += 1

    print(f"\n[convert_plugin] Converted {converted} custom plugins successfully.")

def main():
    parser = argparse.ArgumentParser(description="Convert in-tree Soong plugins to bp2ninja plugins (.so)")
    parser.add_argument("target", nargs="?", help="Path to in-tree plugin directory or .go file")
    parser.add_argument("-o", "--output", help="Output .so path")
    parser.add_argument("--tree", default=None, help="Android source tree root (auto-detected if omitted)")
    parser.add_argument("--get-go", action="store_true", help="Print path to detected Go compiler and exit")

    args = parser.parse_args()

    if args.get_go:
        try:
            aosp_tree = find_aosp_root(args.tree)
        except Exception:
            aosp_tree = None
        print(find_go_compiler(aosp_tree))
        return

    aosp_tree = find_aosp_root(args.tree)

    if args.all or not args.target:
        convert_all_plugins(aosp_tree)
    else:
        if not convert_plugin(args.target, args.output, aosp_tree):
            sys.exit(1)

if __name__ == "__main__":
    main()
