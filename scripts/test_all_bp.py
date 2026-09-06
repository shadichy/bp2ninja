#!/usr/bin/env python3
"""
test_all_bp.py: Comprehensive tree-wide test runner for bp2ninja.
Goal:
"without any known modules: build Android.bp custom definition dependency tree,
test on ALL Android.bp independently (if there's custom Android.bp definition,
build it (after its dependencies) and append -a), report what works and what failed"

Zero hardcoded local paths, zero pre-baked module lists.
Dynamically discovers the dependency tree of custom Soong definitions,
builds them in topological order (dependencies first), and tests all Android.bp files.
"""

import os
import re
import sys
import json
import time
import shutil
import subprocess
import tempfile
from pathlib import Path
from concurrent.futures import ProcessPoolExecutor, as_completed

SCRIPT_DIR = Path(__file__).resolve().parent
BP2NINJA_DIR = SCRIPT_DIR.parent
PLUGINS_DIR = BP2NINJA_DIR / "plugins"

EXCLUDE_DIRS = {".git", ".repo", "out"}

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

def find_bp2ninja_binary():
    candidates = [
        BP2NINJA_DIR / "bin/bp2ninja",
        BP2NINJA_DIR / "bp2ninja",
        Path("bin/bp2ninja").resolve(),
        Path("bp2ninja").resolve(),
    ]
    for c in candidates:
        if c.is_file() and os.access(c, os.X_OK):
            return c
    which_bin = shutil.which("bp2ninja")
    if which_bin:
        return Path(which_bin)
    raise RuntimeError("Cannot find bp2ninja executable. Please run 'make build' first.")

def build_custom_dependency_tree(aosp_root):
    """
    Dynamically scans the entire tree for bootstrap_go_package declarations
    and parses their registered module types and dependencies.
    """
    print(f"[*] Scanning {aosp_root} to build custom definition dependency tree...")
    custom_defs = {}
    mod_type_to_pkg = {}

    for root, dirs, files in os.walk(aosp_root):
        dirs[:] = [d for d in dirs if d not in EXCLUDE_DIRS]
        if "Android.bp" in files:
            rel = os.path.relpath(root, aosp_root)
            if rel.startswith("build/soong") or rel.startswith("build/blueprint"):
                continue

            bp_path = os.path.join(root, "Android.bp")
            try:
                with open(bp_path, "r", encoding="utf-8", errors="ignore") as f:
                    content = f.read()
            except Exception:
                continue

            if "bootstrap_go_package" not in content:
                continue

            for m in re.finditer(r"bootstrap_go_package\s*\{([^}]+)\}", content):
                blk = m.group(1)
                nm = re.search(r"name:\s*\"([^\"]+)\"", blk)
                if not nm:
                    continue
                pkg_name = nm.group(1)
                dm = re.search(r"deps:\s*\[([^\]]*)\]", blk)
                deps = [d.strip("\" \t\n") for d in dm.group(1).split(",") if d.strip("\" \t\n")] if dm else []

                # Scan Go files in this directory for registered module types
                reg_types = []
                for gf in Path(root).glob("*.go"):
                    if gf.name.endswith("_test.go"):
                        continue
                    try:
                        gfc = gf.read_text(encoding="utf-8", errors="ignore")
                        for mt in re.findall(r"RegisterModuleType\(\s*(?:ctx\s*,\s*)?\"([^\"]+)\"", gfc):
                            if mt not in reg_types:
                                reg_types.append(mt)
                                mod_type_to_pkg[mt] = pkg_name
                    except Exception:
                        continue

                custom_defs[pkg_name] = {
                    "dir": root,
                    "deps": deps,
                    "module_types": reg_types,
                }

    print(f"[*] Discovered {len(custom_defs)} custom package definitions.")
    print(f"[*] Discovered {len(mod_type_to_pkg)} custom module types dynamically.")
    return custom_defs, mod_type_to_pkg

def get_topological_build_order(pkg_name, custom_defs, visited=None):
    """
    Returns the topological build order for a custom package (dependencies first).
    """
    if visited is None:
        visited = set()
    order = []
    if pkg_name in visited:
        return order
    visited.add(pkg_name)

    for dep in custom_defs.get(pkg_name, {}).get("deps", []):
        if dep in custom_defs:
            for d in get_topological_build_order(dep, custom_defs, visited):
                if d not in order:
                    order.append(d)
    if pkg_name not in order:
        order.append(pkg_name)
    return order

def prebuild_custom_plugins(custom_defs, mod_type_to_pkg, aosp_root):
    """
    Builds all custom definitions in topological order (dependencies first)
    using convert_plugin.
    """
    PLUGINS_DIR.mkdir(parents=True, exist_ok=True)
    sys.path.insert(0, str(SCRIPT_DIR))
    import convert_plugin

    print("[*] Compiling custom definition plugins in topological dependency order...")
    # Find all packages that actually register custom module types
    active_pkgs = set(mod_type_to_pkg.values())

    # Build full dependency closure in topological order
    full_order = []
    for pkg in sorted(active_pkgs):
        for item in get_topological_build_order(pkg, custom_defs):
            if item not in full_order:
                full_order.append(item)

    built_plugins = {}
    for pkg in full_order:
        pinfo = custom_defs.get(pkg)
        if not pinfo:
            continue
        pdir = Path(pinfo["dir"])
        # Skip core build/soong internal packages which are standard Soong libraries
        rel = os.path.relpath(pdir, aosp_root)
        if rel.startswith("build/soong") or rel.startswith("build/blueprint"):
            continue

        out_so = PLUGINS_DIR / f"{pkg}.so"
        if not out_so.is_file():
            print(f"    Building custom plugin: {pkg} (deps: {pinfo['deps']})")
            convert_plugin.convert_plugin(pdir, out_so_path=out_so, aosp_tree=aosp_root)

        if out_so.is_file():
            built_plugins[pkg] = str(out_so)

    print(f"[*] Successfully built {len(built_plugins)} custom plugins in {PLUGINS_DIR}.\n")
    return built_plugins

def find_all_bps(aosp_root):
    print(f"[*] Scanning AOSP tree at {aosp_root} for all Android.bp files...")
    bp_list = []
    for root, dirs, files in os.walk(aosp_root):
        dirs[:] = [d for d in dirs if d not in EXCLUDE_DIRS]
        if "Android.bp" in files:
            bp_list.append(os.path.join(root, "Android.bp"))
    bp_list.sort()
    print(f"[*] Found {len(bp_list)} total Android.bp files.")
    return bp_list

def test_single_bp(args):
    idx, bp_path, tmp_root, aosp_root, bp2ninja_bin, mod_type_to_pkg, pkg_to_so, custom_defs = args
    rel_path = os.path.relpath(bp_path, aosp_root)
    bp_dir = os.path.dirname(bp_path)
    
    out_dir = os.path.join(tmp_root, f"test_{idx}")
    ninja_file = os.path.join(out_dir, "build.ninja")

    try:
        with open(bp_path, "r", encoding="utf-8", errors="ignore") as f:
            content = f.read()
    except Exception:
        content = ""

    # Check if this Android.bp defines or uses any custom definitions
    used_custom_types = []
    plugins_to_append = []

    # 1. If this Android.bp defines a custom definition:
    for pkg, pinfo in custom_defs.items():
        if os.path.abspath(pinfo["dir"]) == os.path.abspath(bp_dir):
            for dep_pkg in get_topological_build_order(pkg, custom_defs):
                so_path = pkg_to_so.get(dep_pkg)
                if so_path and so_path not in plugins_to_append:
                    plugins_to_append.append(so_path)

    # 2. If this Android.bp uses any custom module types:
    for mod_type, pkg in mod_type_to_pkg.items():
        if re.search(r'\b' + re.escape(mod_type) + r'\s*\{', content):
            used_custom_types.append(mod_type)
            # Find dependencies in topological order and append their .so
            for dep_pkg in get_topological_build_order(pkg, custom_defs):
                so_path = pkg_to_so.get(dep_pkg)
                if so_path and so_path not in plugins_to_append:
                    plugins_to_append.append(so_path)

    # Step 1: Run bp2ninja (with -a appended if custom definition is used)
    cmd_bp = [
        str(bp2ninja_bin),
        "-bp", bp_path,
        "-o", ninja_file,
        "-out", out_dir,
        "-top", str(aosp_root),
        "--allow-missing-deps",
    ]
    for p in plugins_to_append:
        cmd_bp.extend(["-a", p])

    try:
        res_bp = subprocess.run(
            cmd_bp,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            timeout=30,
        )
    except subprocess.TimeoutExpired:
        return {
            "file": rel_path,
            "status": "TIMEOUT_BP2NINJA",
            "error": "bp2ninja timeout (30s)",
            "actions": 0,
            "custom_types": used_custom_types,
        }
    except Exception as e:
        return {
            "file": rel_path,
            "status": "ERROR_BP2NINJA",
            "error": str(e),
            "actions": 0,
            "custom_types": used_custom_types,
        }

    if res_bp.returncode != 0:
        err_out = (res_bp.stderr.strip() or res_bp.stdout.strip()).splitlines()
        first_err = err_out[0] if err_out else "Unknown bp2ninja error"
        
        status = "FAIL_GEN"
        if "Error parsing" in res_bp.stderr:
            status = "FAIL_PARSE"
        elif "Error evaluating" in res_bp.stderr:
            status = "FAIL_EVAL"

        return {
            "file": rel_path,
            "status": status,
            "error": first_err,
            "actions": 0,
            "custom_types": used_custom_types,
        }

    # Step 2: Validate with ninja -n (dry run)
    cmd_ninja = [
        "ninja",
        "-f", ninja_file,
        "-C", bp_dir,
        "-n",
    ]
    try:
        res_ninja = subprocess.run(
            cmd_ninja,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            timeout=30,
        )
    except subprocess.TimeoutExpired:
        return {
            "file": rel_path,
            "status": "TIMEOUT_NINJA",
            "error": "ninja -n timeout (30s)",
            "actions": 0,
            "custom_types": used_custom_types,
        }
    except Exception as e:
        return {
            "file": rel_path,
            "status": "ERROR_NINJA",
            "error": str(e),
            "actions": 0,
            "custom_types": used_custom_types,
        }

    if res_ninja.returncode != 0:
        err_lines = [l for l in res_ninja.stderr.splitlines() if "ninja: error:" in l]
        first_err = err_lines[0] if err_lines else (res_ninja.stderr.strip() or "ninja validation failed")
        return {
            "file": rel_path,
            "status": "FAIL_NINJA",
            "error": first_err,
            "actions": 0,
            "custom_types": used_custom_types,
        }

    actions = 0
    try:
        with open(ninja_file, "r", encoding="utf-8", errors="ignore") as f:
            for line in f:
                if line.startswith("build "):
                    actions += 1
    except Exception:
        pass

    return {
        "file": rel_path,
        "status": "PASS",
        "error": "",
        "actions": actions,
        "custom_types": used_custom_types,
    }

def main():
    import argparse
    parser = argparse.ArgumentParser(description="Test bp2ninja on all Android.bp files using dynamic custom definition dependency tree")
    parser.add_argument("--tree", default=None, help="Android source tree root (auto-detected if omitted)")
    parser.add_argument("--max", type=int, default=0, help="Maximum number of files to test (0 for all)")
    args = parser.parse_args()

    aosp_root = find_aosp_root(args.tree)
    bp2ninja_bin = find_bp2ninja_binary()

    # 1. Build custom definition dependency tree dynamically
    custom_defs, mod_type_to_pkg = build_custom_dependency_tree(aosp_root)

    # 2. Build custom plugins in topological dependency order (dependencies first)
    pkg_to_so = prebuild_custom_plugins(custom_defs, mod_type_to_pkg, aosp_root)

    # 3. Discover all Android.bp files
    bp_list = find_all_bps(aosp_root)
    if args.max > 0 and args.max < len(bp_list):
        import random
        random.seed(42)
        bp_list = random.sample(bp_list, args.max)

    total = len(bp_list)
    if total == 0:
        print("[!] No Android.bp files found.")
        sys.exit(1)

    tmp_dir = os.environ.get("TMPDIR")
    tmp_root = tempfile.mkdtemp(prefix="bp2ninja_deptree_test_", dir=tmp_dir)
    print(f"[*] Testing {total} Android.bp files across {aosp_root}...")
    t0 = time.time()

    workers = max(1, os.cpu_count() or 4) * 2
    tasks = [
        (i, path, tmp_root, aosp_root, bp2ninja_bin, mod_type_to_pkg, pkg_to_so, custom_defs)
        for i, path in enumerate(bp_list)
    ]

    results = []
    completed = 0
    pass_count = 0

    with ProcessPoolExecutor(max_workers=workers) as executor:
        futures = {executor.submit(test_single_bp, t): t for t in tasks}
        for future in as_completed(futures):
            res = future.result()
            results.append(res)
            completed += 1
            if res["status"] == "PASS":
                pass_count += 1

            if completed % 1000 == 0 or completed == total:
                rate = (pass_count / completed) * 100.0
                elapsed = time.time() - t0
                fps = completed / elapsed if elapsed > 0 else 0
                print(f"[{completed:5d}/{total:5d}] {rate:5.1f}% PASS ({pass_count} passed, {completed - pass_count} failed) - {fps:.1f} bp/s")

    total_time = time.time() - t0

    try:
        shutil.rmtree(tmp_root, ignore_errors=True)
    except Exception:
        pass

    report_file = BP2NINJA_DIR / "all_bp_test_results.json"
    with open(report_file, "w") as f:
        json.dump(results, f, indent=2)
    print(f"\n[*] Full per-file results saved to {report_file.resolve()}")

    status_counts = {}
    top_dir_stats = {}
    error_counts = {}
    custom_type_stats = {}

    total_actions = 0
    for r in results:
        st = r["status"]
        status_counts[st] = status_counts.get(st, 0) + 1
        total_actions += r["actions"]

        for ct in r.get("custom_types", []):
            if ct not in custom_type_stats:
                custom_type_stats[ct] = {"total": 0, "pass": 0, "fail": 0}
            custom_type_stats[ct]["total"] += 1
            if st == "PASS":
                custom_type_stats[ct]["pass"] += 1
            else:
                custom_type_stats[ct]["fail"] += 1

        top_dir = r["file"].split("/")[0] if "/" in r["file"] else "."
        if top_dir not in top_dir_stats:
            top_dir_stats[top_dir] = {"total": 0, "pass": 0, "fail": 0}
        top_dir_stats[top_dir]["total"] += 1
        if st == "PASS":
            top_dir_stats[top_dir]["pass"] += 1
        else:
            top_dir_stats[top_dir]["fail"] += 1
            err = r["error"]
            cat = err
            if ":" in err:
                cat = err.split(":")[0] + ": " + err.split(":")[-1].strip()
            error_counts[cat] = error_counts.get(cat, 0) + 1

    overall_pass_rate = (pass_count / total) * 100.0

    print("\n" + "="*75)
    print("   TREE-WIDE ALL-ANDROID.BP VERIFICATION REPORT (WITH DYNAMIC PLUGINS)")
    print("="*75)
    print(f"Total Android.bp files tested: {total}")
    print(f"Overall PASS (Valid Ninja):   {pass_count} ({overall_pass_rate:.2f}%)")
    print(f"Overall FAIL:                  {total - pass_count} ({100.0 - overall_pass_rate:.2f}%)")
    print(f"Total Ninja Build Actions:     {total_actions:,}")
    print(f"Total Benchmark Time:          {total_time:.2f} seconds ({total / total_time:.1f} files/sec)")
    print("="*75)

    print("\nBreakdown by Outcome:")
    for st, count in sorted(status_counts.items(), key=lambda x: x[1], reverse=True):
        print(f"  {st:<18}: {count:5d} ({count/total*100:5.2f}%)")

    if custom_type_stats:
        print("\n" + "="*75)
        print(f"{'Custom Module Type (-a)':<35} {'Total':>7} {'Pass':>7} {'Fail':>7} {'Pass Rate':>10}")
        print("-"*75)
        for ct, s in sorted(custom_type_stats.items(), key=lambda x: x[1]["total"], reverse=True):
            rate = (s["pass"] / s["total"]) * 100.0
            print(f"{ct:<35} {s['total']:>7d} {s['pass']:>7d} {s['fail']:>7d} {rate:>9.1f}%")
        print("="*75)

    print("\n" + "="*75)
    print(f"{'Top-Level Directory':<25} {'Total':>7} {'Pass':>7} {'Fail':>7} {'Pass Rate':>10}")
    print("-"*75)
    for td, s in sorted(top_dir_stats.items(), key=lambda x: x[1]["total"], reverse=True):
        rate = (s["pass"] / s["total"]) * 100.0
        print(f"{td:<25} {s['total']:>7d} {s['pass']:>7d} {s['fail']:>7d} {rate:>9.1f}%")
    print("="*75)

    if error_counts:
        print("\nFailure Root Causes:")
        for err, cnt in sorted(error_counts.items(), key=lambda x: x[1], reverse=True)[:15]:
            print(f"  [{cnt:4d}] {err[:100]}")

if __name__ == "__main__":
    main()
