#!/usr/bin/env bash
# M10: local pre-release sanity check.
# - build with version/commit ldflags
# - run critical test subset
# - write binary + SHA-256 checksum
#
# Usage:
#   bash scripts/release-check.sh
#   VERSION=1.0.0 OUT_DIR=dist bash scripts/release-check.sh
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

export TMPDIR="${TMPDIR:-/tmp}"
mkdir -p "$TMPDIR"

VERSION="${VERSION:-$(go list -m -f '{{.Version}}' 2>/dev/null || true)}"
if [[ -z "${VERSION}" || "${VERSION}" == "null" || "${VERSION}" == "" ]]; then
  VERSION="1.0.0"
fi
# Prefer git describe / short commit when available.
if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo none)"
  if [[ "${VERSION}" == "1.0.0" ]]; then
    DESCRIBE="$(git describe --tags --always --dirty 2>/dev/null || true)"
    if [[ -n "${DESCRIBE}" ]]; then
      VERSION="${DESCRIBE#v}"
    fi
  fi
else
  COMMIT="none"
fi

OUT_DIR="${OUT_DIR:-dist}"
mkdir -p "$OUT_DIR"
BIN_NAME="${BIN_NAME:-goroku}"
OS="$(go env GOOS)"
ARCH="$(go env GOARCH)"
OUT_BIN="${OUT_DIR}/${BIN_NAME}_${VERSION}_${OS}_${ARCH}"
# Also write a stable local name for operators.
STABLE_BIN="${OUT_DIR}/${BIN_NAME}"

LDFLAGS="-s -w -X goroku/goroku.VersionInfo=${VERSION} -X goroku/goroku.Commit=${COMMIT}"

echo "==> release-check: VERSION=${VERSION} COMMIT=${COMMIT}"
echo "==> build: ${OUT_BIN}"
CGO_ENABLED="${CGO_ENABLED:-0}" go build -trimpath -ldflags "${LDFLAGS}" -o "${OUT_BIN}" .
cp -f "${OUT_BIN}" "${STABLE_BIN}"
chmod +x "${OUT_BIN}" "${STABLE_BIN}"

echo "==> tests: critical subset (./goroku/ ./goroku/web/)"
go test -race -count=1 ./goroku/ ./goroku/web/

echo "==> checksum"
SUM_FILE="${OUT_DIR}/SHA256SUMS"
if command -v sha256sum >/dev/null 2>&1; then
  (cd "$OUT_DIR" && sha256sum "$(basename "$OUT_BIN")" "$(basename "$STABLE_BIN")" >"$(basename "$SUM_FILE")")
elif command -v shasum >/dev/null 2>&1; then
  (cd "$OUT_DIR" && shasum -a 256 "$(basename "$OUT_BIN")" "$(basename "$STABLE_BIN")" >"$(basename "$SUM_FILE")")
else
  echo "no sha256sum/shasum; skip checksum file" >&2
  SUM_FILE=""
fi

# Optional SBOM next to release artifacts.
if [[ "${RELEASE_CHECK_SBOM:-1}" == "1" ]]; then
  echo "==> SBOM"
  GOROKU_BIN="${OUT_BIN}" bash scripts/generate-sbom.sh "${OUT_DIR}/sbom"
fi

echo "==> release-check ok"
echo "BINARY=${OUT_BIN}"
echo "STABLE_BINARY=${STABLE_BIN}"
[[ -n "${SUM_FILE}" ]] && echo "CHECKSUMS=${SUM_FILE}" && cat "${SUM_FILE}"
./"${STABLE_BIN}" -h >/dev/null 2>&1 || true
echo "ldflags VersionInfo=${VERSION} Commit=${COMMIT}"
