#!/usr/bin/env python3
import os
import sys
import random
import subprocess
import tempfile
import time
from pathlib import Path

SCRIPT_DIR = Path(__file__).resolve().parent
BP2NINJA_DIR = SCRIPT_DIR.parent

def find_aosp_root():
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
    raise RuntimeError("Cannot locate Android source tree root. Please set ANDROID_BUILD_TOP.")

def find_bp2ninja():
    for c in [BP2NINJA_DIR / "bin/bp2ninja", BP2NINJA_DIR / "bp2ninja"]:
        if c.is_file() and os.access(c, os.X_OK):
            return c
    raise RuntimeError("Cannot locate bp2ninja binary. Run make build first.")

AOSP_ROOT = find_aosp_root()
BP2NINJA = find_bp2ninja()

TIER4_EXCLUDES = [
    "art/",
    "bionic/",
    "frameworks/base/",
    "system/sepolicy/",
    "build/make/",
    "build/soong/",
    "out/",
    ".repo/",
]

def is_excluded(rel_path: str) -> bool:
    for exc in TIER4_EXCLUDES:
        if rel_path.startswith(exc):
            return True
    return False

def find_candidate_bps():
    candidates = []
    print("[*] Scanning AOSP tree for Android.bp files...")
    for root, dirs, files in os.walk(AOSP_ROOT):
        # Prune excluded directories early
        rel_root = os.path.relpath(root, AOSP_ROOT)
        if rel_root != ".":
            rel_root_slash = rel_root + "/"
            if any(rel_root_slash.startswith(exc) for exc in TIER4_EXCLUDES):
                dirs.clear()
                continue
            if ".git" in dirs:
                dirs.remove(".git")
            if "out" in dirs:
                dirs.remove("out")

        if "Android.bp" in files:
            full_path = os.path.join(root, "Android.bp")
            rel_path = os.path.relpath(full_path, AOSP_ROOT)
            if not is_excluded(rel_path):
                candidates.append(full_path)

    print(f"[*] Found {len(candidates)} candidate Android.bp files (excluding Tier 4/monoliths).")
    return candidates

def test_bp(bp_path: str, tmp_root: str):
    bp_dir = os.path.dirname(bp_path)
    h = str(abs(hash(bp_path)))[:8]
    out_dir = os.path.join(tmp_root, f"out_{h}")
    ninja_file = os.path.join(out_dir, "build.ninja")

    # Step 1: Run bp2ninja
    cmd_bp2ninja = [
        str(BP2NINJA),
        "-bp", bp_path,
        "-o", ninja_file,
        "-out", out_dir,
        "-top", str(AOSP_ROOT),
        "--allow-missing-deps",
    ]
    try:
        res_bp = subprocess.run(
            cmd_bp2ninja,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            timeout=10,
        )
    except subprocess.TimeoutExpired:
        return {
            "status": "TIMEOUT_BP2NINJA",
            "error": "bp2ninja timeout (10s)",
            "actions": 0,
        }
    except Exception as e:
        return {
            "status": "ERROR_BP2NINJA",
            "error": str(e),
            "actions": 0,
        }

    if res_bp.returncode != 0:
        err_msg = res_bp.stderr.strip() or res_bp.stdout.strip()
        first_err = err_msg.splitlines()[-1] if err_msg else "non-zero exit"
        return {
            "status": "FAIL_TRANSLATION",
            "error": first_err,
            "actions": 0,
        }

    # Step 2: Run ninja -n (dry run)
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
            timeout=10,
        )
    except subprocess.TimeoutExpired:
        return {
            "status": "TIMEOUT_NINJA",
            "error": "ninja -n timeout (10s)",
            "actions": 0,
        }
    except Exception as e:
        return {
            "status": "ERROR_NINJA",
            "error": str(e),
            "actions": 0,
        }

    if res_ninja.returncode != 0:
        err_msg = res_ninja.stderr.strip() or res_ninja.stdout.strip()
        first_err = err_msg.splitlines()[-1] if err_msg else "ninja non-zero exit"
        return {
            "status": "FAIL_NINJA",
            "error": first_err,
            "actions": 0,
        }

    # Count actions
    actions = len([line for line in res_ninja.stdout.splitlines() if line.startswith("[") and "]" in line])
    return {
        "status": "PASS",
        "error": "",
        "actions": actions,
    }

def main():
    random.seed(42)  # Deterministic seed for reproducibility
    candidates = find_candidate_bps()
    if not candidates:
        print("[!] No candidate files found.")
        sys.exit(1)

    sample_size = min(150, len(candidates))
    sample = random.sample(candidates, sample_size)
    print(f"[*] Selected {sample_size} random Android.bp targets for benchmark.\n")

    results = []
    tmp_root = tempfile.mkdtemp(prefix="bp2ninja_bench_")

    start_time = time.time()
    for idx, bp in enumerate(sample, 1):
        rel = os.path.relpath(bp, AOSP_ROOT)
        res = test_bp(bp, tmp_root)
        results.append((rel, res))
        mark = "✓" if res["status"] == "PASS" else "✗"
        print(f"[{idx:3d}/{sample_size}] {mark} {res['status']:<16} ({res['actions']:4d} actions) - {rel}")
        if res["error"]:
            print(f"      Reason: {res['error'][:120]}")

    elapsed = time.time() - start_time

    # Compute stats
    total = len(results)
    passed = sum(1 for _, r in results if r["status"] == "PASS")
    fail_translation = sum(1 for _, r in results if r["status"] == "FAIL_TRANSLATION")
    fail_ninja = sum(1 for _, r in results if r["status"] == "FAIL_NINJA")
    timeouts = sum(1 for _, r in results if "TIMEOUT" in r["status"])
    total_actions = sum(r["actions"] for _, r in results)

    pass_rate = (passed / total) * 100.0

    print("\n" + "="*60)
    print("           BP2NINJA COVERAGE BENCHMARK REPORT")
    print("="*60)
    print(f"Total Targets Tested:      {total}")
    print(f"Passed (Verified exit 0):  {passed} ({pass_rate:.1f}%)")
    print(f"Failed Translation:        {fail_translation}")
    print(f"Failed Ninja Validation:   {fail_ninja}")
    print(f"Timeouts:                  {timeouts}")
    print(f"Total Ninja Actions Gen:   {total_actions}")
    print(f"Total Benchmark Time:      {elapsed:.2f}s")
    print("="*60)

    # Breakdown of errors if any
    if fail_translation + fail_ninja > 0:
        print("\nFailure breakdown by category:")
        error_counts = {}
        for _, r in results:
            if r["status"] != "PASS":
                err = r["error"]
                cat = err.split(":")[0] if ":" in err else err
                error_counts[cat] = error_counts.get(cat, 0) + 1
        for cat, cnt in sorted(error_counts.items(), key=lambda x: x[1], reverse=True)[:10]:
            print(f"  [{cnt:2d}] {cat}")

if __name__ == "__main__":
    main()
