#!/usr/bin/env bash
# Generate lightweight module SBOMs without extra tooling (M9.2 / M10 residual helper).
# Optional richer SBOMs: install Syft/CycloneDX separately; not a CI hard gate.
#
# Usage:
#   bash scripts/generate-sbom.sh [OUT_DIR]
#   GOROKU_BIN=/path/to/binary bash scripts/generate-sbom.sh dist/sbom
#
# Default OUT_DIR: dist/sbom
# Writes a stable artifact index at OUT_DIR/SBOM_ARTIFACTS.txt and prints the
# absolute path of the directory for CI upload.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

OUT_DIR="${1:-dist/sbom}"
mkdir -p "$OUT_DIR"
OUT_ABS="$(cd "$OUT_DIR" && pwd)"

STAMP="$(date -u +%Y%m%dT%H%M%SZ 2>/dev/null || date -u +%Y%m%d)"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo none)"
MODULE_PATH="$(go list -m -f '{{.Path}}')"
MODULE_VERSION="$(go list -m -f '{{.Version}}' 2>/dev/null || true)"
if [[ -z "${MODULE_VERSION}" || "${MODULE_VERSION}" == "null" ]]; then
  MODULE_VERSION="devel"
fi

echo "Writing module list to ${OUT_ABS}/go-modules.json"
go list -m -json all >"${OUT_ABS}/go-modules.json"

echo "Writing module versions to ${OUT_ABS}/go-modules.txt"
go list -m all >"${OUT_ABS}/go-modules.txt"

echo "Writing direct requires to ${OUT_ABS}/go-modules-direct.txt"
go list -m -f '{{if not .Indirect}}{{.Path}} {{.Version}}{{end}}' all | sed '/^$/d' >"${OUT_ABS}/go-modules-direct.txt"

# Minimal CycloneDX-like component list (JSON) for tooling that accepts simple graphs.
# Not a full CycloneDX 1.5 document — usable as a release artifact path.
echo "Writing components inventory to ${OUT_ABS}/sbom-components.json"
python3 - "${OUT_ABS}" "${MODULE_PATH}" "${MODULE_VERSION}" "${COMMIT}" <<'PY'
import json, subprocess, sys, datetime

out_dir, main_path, main_ver, commit = sys.argv[1:5]
raw = subprocess.check_output(["go", "list", "-m", "-json", "all"], text=True)
decoder = json.JSONDecoder()
idx = 0
mods = []
while idx < len(raw):
    while idx < len(raw) and raw[idx].isspace():
        idx += 1
    if idx >= len(raw):
        break
    obj, end = decoder.raw_decode(raw, idx)
    idx = end
    if not isinstance(obj, dict) or "Path" not in obj:
        continue
    mods.append(obj)

components = []
for m in mods:
    path = m["Path"]
    ver = m.get("Version") or ("devel" if path == main_path else "")
    components.append({
        "type": "library",
        "name": path,
        "version": ver,
        "purl": f"pkg:golang/{path}@{ver}" if ver else f"pkg:golang/{path}",
        "scope": "required" if not m.get("Indirect") else "optional",
        "properties": [
            {"name": "go.module.indirect", "value": str(bool(m.get("Indirect")))},
            {"name": "go.module.main", "value": str(path == main_path)},
        ],
    })

doc = {
    "bomFormat": "CycloneDX",
    "specVersion": "1.5",
    "version": 1,
    "metadata": {
        "timestamp": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "component": {
            "type": "application",
            "name": main_path,
            "version": main_ver,
            "purl": f"pkg:golang/{main_path}@{main_ver}",
        },
        "properties": [
            {"name": "goroku.git_commit", "value": commit},
            {"name": "goroku.sbom_generator", "value": "scripts/generate-sbom.sh"},
            {"name": "goroku.sbom_note", "value": "Lightweight go-list inventory; full Syft/CycloneDX optional"},
        ],
    },
    "components": components,
}
path = f"{out_dir}/sbom-components.json"
with open(path, "w", encoding="utf-8") as f:
    json.dump(doc, f, indent=2)
    f.write("\n")
print(f"Wrote {path} ({len(components)} components)")
PY

BIN="${GOROKU_BIN:-}"
if [[ -z "$BIN" ]]; then
  if [[ -x "${TMPDIR:-/tmp}/goroku_bin" ]]; then
    BIN="${TMPDIR:-/tmp}/goroku_bin"
  elif [[ -x ./goroku_bin ]]; then
    BIN=./goroku_bin
  fi
fi

if [[ -n "$BIN" && -x "$BIN" ]]; then
  echo "Writing binary build info to ${OUT_ABS}/binary-version-m.txt"
  go version -m "$BIN" >"${OUT_ABS}/binary-version-m.txt"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$BIN" >"${OUT_ABS}/binary.sha256"
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$BIN" >"${OUT_ABS}/binary.sha256"
  fi
else
  echo "Skip binary version -m (set GOROKU_BIN or build to ${TMPDIR:-/tmp}/goroku_bin first)"
fi

# Stable index for CI artifact upload / release attach.
INDEX="${OUT_ABS}/SBOM_ARTIFACTS.txt"
{
  echo "# Goroku SBOM artifact index"
  echo "# generated_utc=${STAMP}"
  echo "# commit=${COMMIT}"
  echo "# module=${MODULE_PATH}"
  echo "# out_dir=${OUT_ABS}"
  echo "#"
  echo "# Upload this directory as the SBOM artifact (path below)."
  echo "SBOM_DIR=${OUT_ABS}"
  ls -1 "$OUT_ABS" | while read -r f; do
    [[ "$f" == "SBOM_ARTIFACTS.txt" ]] && continue
    echo "FILE=${OUT_ABS}/${f}"
  done
} >"$INDEX"

# Convenience pointer for latest run.
mkdir -p dist
echo "$OUT_ABS" >"${OUT_ABS}/SBOM_PATH.txt"
echo "$OUT_ABS" >"dist/SBOM_LATEST_PATH.txt"

echo "SBOM artifacts in ${OUT_ABS}"
ls -la "$OUT_ABS"
echo "SBOM_ARTIFACT_PATH=${OUT_ABS}"
echo "SBOM_INDEX=${INDEX}"
