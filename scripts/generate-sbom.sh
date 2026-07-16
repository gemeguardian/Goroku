#!/usr/bin/env bash
# Generate lightweight module SBOMs without extra tooling (M9.2 / M10 residual helper).
# Optional richer SBOMs: install Syft/CycloneDX separately; not a CI hard gate.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

OUT_DIR="${1:-dist/sbom}"
mkdir -p "$OUT_DIR"

echo "Writing module list to ${OUT_DIR}/go-modules.json"
go list -m -json all >"${OUT_DIR}/go-modules.json"

echo "Writing module versions to ${OUT_DIR}/go-modules.txt"
go list -m all >"${OUT_DIR}/go-modules.txt"

BIN="${GOROKU_BIN:-}"
if [[ -z "$BIN" ]]; then
  if [[ -x "${TMPDIR:-/tmp}/goroku_bin" ]]; then
    BIN="${TMPDIR:-/tmp}/goroku_bin"
  elif [[ -x ./goroku_bin ]]; then
    BIN=./goroku_bin
  fi
fi

if [[ -n "$BIN" && -x "$BIN" ]]; then
  echo "Writing binary build info to ${OUT_DIR}/binary-version-m.txt"
  go version -m "$BIN" >"${OUT_DIR}/binary-version-m.txt"
else
  echo "Skip binary version -m (set GOROKU_BIN or build to ${TMPDIR:-/tmp}/goroku_bin first)"
fi

echo "SBOM artifacts in ${OUT_DIR}"
ls -la "$OUT_DIR"
