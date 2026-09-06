#!/bin/bash
# ==============================================================================
# build_plugin.sh: Wrapper for bp2ninja Universal Plugin Converter
#
# Automatically converts in-tree Soong plugins (which do not expose bp2ninja
# handlers or functions) into dynamically loadable Go .so plugins.
#
# Usage:
#   ./build_plugin.sh <source_dir_or_go_file> [output_so_path] [aosp_top]
#   ./build_plugin.sh --all [aosp_top]
# ==============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BP2NINJA_DIR="$(dirname "${SCRIPT_DIR}")"

if [ "$#" -lt 1 ]; then
    echo "Usage: $0 <source_dir_or_go_file> [output_so_path] [aosp_top]"
    echo "       $0 --all [aosp_top]"
    exit 1
fi

if [ "$1" = "--all" ]; then
    AOSP_TOP="${2:-${ANDROID_BUILD_TOP:-}}"
    if [ -n "${AOSP_TOP}" ]; then
        python3 "${SCRIPT_DIR}/convert_plugin.py" --all --tree "${AOSP_TOP}"
    else
        python3 "${SCRIPT_DIR}/convert_plugin.py" --all
    fi
    exit 0
fi

TARGET="$1"
OUT_SO="${2:-}"
AOSP_TOP="${3:-${ANDROID_BUILD_TOP:-}}"

EXTRA_ARGS=()
if [ -n "${OUT_SO}" ]; then
    EXTRA_ARGS+=("-o" "${OUT_SO}")
fi
if [ -n "${AOSP_TOP}" ]; then
    EXTRA_ARGS+=("--tree" "${AOSP_TOP}")
fi

python3 "${SCRIPT_DIR}/convert_plugin.py" "${TARGET}" "${EXTRA_ARGS[@]}"
