#!/usr/bin/env python3
"""
gen_gowork.py: Automatic Go Workspace (go.work) Generator for Android Soong/Blueprint Packages

Resolves `android/*` (such as `android/soong/...`) and `github.com/google/blueprint`
packages by generating a `go.work` workspace file.

Supports two modes:
  1. Local Tree Mode: Maps packages to an existing local Android source tree.
  2. Remote Mode (--remote): Clones the missing repositories directly from
     android.googlesource.com into a local cache directory (.deps/ or custom).

Usage:
  python3 scripts/gen_gowork.py [--tree /path/to/aosp] [--remote] [--clean]
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
DEFAULT_DEPS_DIR = BP2NINJA_DIR / ".deps"

# Standard GoogleSource repository mappings for Android build system components
MAPPINGS = {
    "android/soong": {
        "rel_tree": "build/soong",
        "git_url": "https://android.googlesource.com/platform/build/soong",
        "module_name": "android/soong",
    },
    "github.com/google/blueprint": {
        "rel_tree": "build/blueprint",
        "git_url": "https://android.googlesource.com/platform/build/blueprint",
        "module_name": "github.com/google/blueprint",
    },
    "google.golang.org/protobuf": {
        "rel_tree": "external/golang-protobuf",
        "git_url": "https://android.googlesource.com/platform/external/golang-protobuf",
        "module_name": "google.golang.org/protobuf",
    },
    "go.starlark.net": {
        "rel_tree": "external/starlark-go",
        "git_url": "https://android.googlesource.com/platform/external/starlark-go",
        "module_name": "go.starlark.net",
    },
}

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
    return None

def get_go_compiler():
    try:
        sys.path.insert(0, str(SCRIPT_DIR))
        import convert_plugin
        return convert_plugin.find_go_compiler()
    except Exception:
        return shutil.which("go") or "go"

def get_go_version(go_bin):
    try:
        out = subprocess.check_output([go_bin, "version"], text=True)
        m = re.search(r"go(\d+\.\d+(?:\.\d+)?)", out)
        if m:
            # For go.work, Go 1.21+ accepts go 1.21 or exact version
            parts = m.group(1).split(".")
            return f"{parts[0]}.{parts[1]}"
    except Exception:
        pass
    return "1.21"

def clone_repo(git_url, dest_dir, branch="main"):
    dest = Path(dest_dir)
    if (dest / ".git").is_dir():
        print(f"[*] Repository already present: {dest}")
        return True

    dest.parent.mkdir(parents=True, exist_ok=True)
    print(f"[*] Cloning {git_url} -> {dest}...")
    cmd = ["git", "clone", "--depth", "1", git_url, str(dest)]
    res = subprocess.run(cmd, capture_output=True, text=True)
    if res.returncode != 0:
        # Fallback without depth if server restricts it
        cmd_fallback = ["git", "clone", git_url, str(dest)]
        res_fb = subprocess.run(cmd_fallback, capture_output=True, text=True)
        if res_fb.returncode != 0:
            print(f"[-] Failed to clone {git_url}: {res.stderr}")
            return False
    return True

def ensure_go_mod(mod_dir, module_name, go_version="1.21"):
    mod_file = Path(mod_dir) / "go.mod"
    if not mod_file.is_file():
        print(f"[*] Creating minimal go.mod for {module_name} in {mod_dir}")
        mod_file.write_text(f"module {module_name}\n\ngo {go_version}\n", encoding="utf-8")

def generate_gowork(aosp_tree=None, force_remote=False, cache_dir=None, clean=False):
    gowork_path = BP2NINJA_DIR / "go.work"
    gowork_sum = BP2NINJA_DIR / "go.work.sum"

    if clean:
        if gowork_path.exists():
            gowork_path.unlink()
            print(f"[OK] Removed {gowork_path}")
        if gowork_sum.exists():
            gowork_sum.unlink()
            print(f"[OK] Removed {gowork_sum}")
        return

    go_bin = get_go_compiler()
    go_version = get_go_version(go_bin)
    print(f"[*] Using Go toolchain: {go_bin} (version {go_version})")

    resolved_modules = {}
    use_local = (aosp_tree is not None) and not force_remote

    if use_local:
        print(f"[*] Local Android tree detected: {aosp_tree}")
        for mod_id, info in MAPPINGS.items():
            candidate = Path(aosp_tree) / info["rel_tree"]
            if candidate.is_dir():
                resolved_modules[mod_id] = candidate.resolve()
    else:
        print("[*] Remote resolution mode: fetching missing packages from googlesource.com")
        target_cache = Path(cache_dir) if cache_dir else DEFAULT_DEPS_DIR
        target_cache.mkdir(parents=True, exist_ok=True)

        for mod_id, info in MAPPINGS.items():
            sub_name = info["rel_tree"].replace("/", "_")
            dest = target_cache / sub_name
            if clone_repo(info["git_url"], dest):
                resolved_modules[mod_id] = dest.resolve()

    # Ensure all resolved modules have a go.mod
    for mod_id, path in resolved_modules.items():
        info = MAPPINGS.get(mod_id, {})
        mod_name = info.get("module_name", mod_id)
        ensure_go_mod(path, mod_name, go_version)

    # Build use directives (modules present in the workspace)
    use_paths = ["."]
    for path in resolved_modules.values():
        try:
            rel = os.path.relpath(path, BP2NINJA_DIR)
            use_paths.append(rel if not rel.startswith("..") else str(path))
        except Exception:
            use_paths.append(str(path))

    # Build replace directives for versioned resolution (handles Soong requires)
    replaces = []
    for mod_id, path in resolved_modules.items():
        # Add versioned replacement for Soong's require directives
        replaces.append(f"\t{mod_id} v0.0.0 => {path}")

    # Format go.work
    content = f"go {go_version}\n\nuse (\n"
    for u in use_paths:
        content += f"\t{u}\n"
    content += ")\n\nreplace (\n"
    for r in replaces:
        content += f"{r}\n"
    content += ")\n"

    gowork_path.write_text(content, encoding="utf-8")
    print(f"\n[OK] Successfully generated {gowork_path}:")
    print("-" * 60)
    print(content.strip())
    print("-" * 60)
    print("[*] Go toolchain can now resolve android/* packages automatically!")

def main():
    parser = argparse.ArgumentParser(description="Generate go.work automatically for Android/Blueprint packages")
    parser.add_argument("--tree", default=None, help="Path to local Android source tree root")
    parser.add_argument("--remote", action="store_true", help="Force fetching dependencies from googlesource.com")
    parser.add_argument("--cache-dir", default=None, help="Directory to cache cloned repositories (default: .deps)")
    parser.add_argument("--clean", action="store_true", help="Remove generated go.work and go.work.sum")

    args = parser.parse_args()

    if args.clean:
        generate_gowork(clean=True)
        return

    aosp_tree = find_aosp_root(args.tree)
    generate_gowork(
        aosp_tree=aosp_tree,
        force_remote=args.remote,
        cache_dir=args.cache_dir,
    )

if __name__ == "__main__":
    main()
