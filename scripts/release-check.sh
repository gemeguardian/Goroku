#!/usr/bin/env bash
# M10 / R5: local pre-release sanity check.
# - build with version/commit ldflags
# - run critical test subset
# - write binary + SHA-256 checksum
# - generate SBOM (CycloneDX) under dist/sbom
# - cosign sign-blob (MANDATORY for stable tagged releases, advisory for snapshots)
#
# Stable vs snapshot:
#   - Stable: HEAD is an exact git tag (`git describe --exact-match --tags HEAD`).
#     cosign signing + SBOM + checksums are MANDATORY; COSIGN_YES=1 (and cosign
#     on PATH) is required, otherwise this script fails.
#   - Snapshot: HEAD is not an exact tag (dev build, --snapshot, nightly).
#     cosign signing is advisory (skipped if cosign is absent / COSIGN_YES!=1).
#
# Usage:
#   bash scripts/release-check.sh
#   VERSION=1.0.0 OUT_DIR=dist bash scripts/release-check.sh
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

export TMPDIR="${TMPDIR:-/tmp}"
mkdir -p "$TMPDIR"

# Detect whether HEAD is an exact git tag (stable release) vs a snapshot.
STABLE_TAG=""
if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  STABLE_TAG="$(git describe --exact-match --tags HEAD 2>/dev/null || true)"
fi

if [[ -n "${STABLE_TAG}" ]]; then
  # Stable release: version comes from the tag (strip a single leading "v").
  VERSION="${VERSION:-${STABLE_TAG#v}}"
  COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo none)"
else
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
if [[ -n "${STABLE_TAG}" ]]; then
  echo "==> mode: stable (tag=${STABLE_TAG})"
  # Mandatory signing for stable.
  if [[ "${COSIGN_YES:-0}" != "1" ]]; then
    echo "ERROR: stable release (${STABLE_TAG}) requires artifact signing." >&2
    echo "       Set COSIGN_YES=1 and ensure cosign is on PATH." >&2
    exit 1
  fi
  if ! command -v cosign >/dev/null 2>&1; then
    echo "ERROR: stable release (${STABLE_TAG}) requires cosign on PATH" >&2
    echo "       (COSIGN_YES=1 is set but cosign was not found)." >&2
    exit 1
  fi
  # SBOM is mandatory for stable; ignore an explicit opt-out.
  if [[ "${RELEASE_CHECK_SBOM:-1}" == "0" ]]; then
    echo "WARNING: stable release requires SBOM; ignoring RELEASE_CHECK_SBOM=0" >&2
    RELEASE_CHECK_SBOM=1
  fi
else
  echo "==> mode: snapshot (no exact git tag); cosign signing is advisory"
fi

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
# Checksums are mandatory for stable.
if [[ -n "${STABLE_TAG}" && ( -z "${SUM_FILE}" || ! -s "${SUM_FILE}" ) ]]; then
  echo "ERROR: stable release (${STABLE_TAG}) requires SHA256SUMS, but none was produced." >&2
  exit 1
fi

# SBOM (CycloneDX) next to release artifacts. Mandatory for stable, optional
# (default-on) for snapshots; opt out with RELEASE_CHECK_SBOM=0.
if [[ "${RELEASE_CHECK_SBOM:-1}" == "1" ]]; then
  echo "==> SBOM"
  GOROKU_BIN="${OUT_BIN}" bash scripts/generate-sbom.sh "${OUT_DIR}/sbom"
  if [[ -n "${STABLE_TAG}" && ! -f "${OUT_DIR}/sbom/sbom.cdx.json" ]]; then
    echo "ERROR: stable release (${STABLE_TAG}) requires dist/sbom/sbom.cdx.json, but it was not produced." >&2
    exit 1
  fi
elif [[ -n "${STABLE_TAG}" ]]; then
  echo "ERROR: stable release (${STABLE_TAG}) requires SBOM." >&2
  exit 1
fi

# cosign sign-blob. MANDATORY for stable (gated above), advisory for snapshots.
# Static key: set COSIGN_KEY (and COSIGN_PASSWORD if encrypted). Keyless OIDC if unset.
COSIGN_SIG=""
COSIGN_BUNDLE=""
if [[ "${COSIGN_YES:-0}" == "1" ]]; then
  if command -v cosign >/dev/null 2>&1; then
    echo "==> cosign sign-blob (${OUT_BIN})"
    if [[ -n "${COSIGN_KEY:-}" ]]; then
      COSIGN_SIG="${OUT_BIN}.sig"
      cosign sign-blob --yes --key "${COSIGN_KEY}" --output-signature "${COSIGN_SIG}" "${OUT_BIN}"
      echo "COSIGN_SIGNATURE=${COSIGN_SIG}"
    else
      COSIGN_BUNDLE="${OUT_BIN}.sigbundle"
      cosign sign-blob --yes --bundle "${COSIGN_BUNDLE}" "${OUT_BIN}"
      echo "COSIGN_BUNDLE=${COSIGN_BUNDLE}"
    fi
  else
    echo "COSIGN_YES=1 but cosign not installed; skip signing" >&2
  fi
fi
# Stable release must produce a signature/bundle.
if [[ -n "${STABLE_TAG}" && -z "${COSIGN_SIG}" && -z "${COSIGN_BUNDLE}" ]]; then
  echo "ERROR: stable release (${STABLE_TAG}) requires a cosign signature/bundle, but none was produced." >&2
  exit 1
fi

# Canary checklist (printed for operators; not auto-executed against a live host).
CANARY_FILE="${OUT_DIR}/CANARY_CHECKLIST.txt"
{
  echo "# Goroku canary checklist (M10)"
  echo "# version=${VERSION} commit=${COMMIT}"
  if [[ -n "${STABLE_TAG}" ]]; then
    echo "# mode=stable tag=${STABLE_TAG}"
  else
    echo "# mode=snapshot"
  fi
  echo "# generated by scripts/release-check.sh"
  echo "#"
  echo "[ ] 1. Snapshot data root (config, sessions, modules, DB) before install"
  echo "[ ] 2. Keep previous binary as goroku.prev (or last SHA256SUMS artifact)"
  echo "[ ] 3. Install ${OUT_BIN} (or ${STABLE_BIN}) with same --data-root flags"
  echo "[ ] 4. Prefer --no-git / GOROKU_NO_GIT=1 on production binary hosts"
  echo "[ ] 5. Smoke: curl -fsS http://127.0.0.1:\${PORT:-8080}/healthz"
  echo "[ ] 6. Smoke: curl -fsS http://127.0.0.1:\${PORT:-8080}/readyz"
  echo "[ ] 7. Smoke: GET /health JSON has status=ok, version, and no secrets"
  echo "[ ] 8. Owner .info / critical modules; watch logs for panic/auth loops"
  echo "[ ] 9. Soak window (minutes–hours) before promoting other hosts"
  echo "[ ] 10. On failure: stop → restore binary (+ data snapshot if needed) → re-check healthz"
  if [[ -n "${STABLE_TAG}" ]]; then
    echo "[ ] 11. MANDATORY: cosign verify-blob of the installed binary"
    echo "[ ] 12. MANDATORY: sha256sum -c SHA256SUMS"
    echo "[ ] 13. MANDATORY: review published SBOM (dist/sbom/sbom.cdx.json)"
  else
    echo "[ ] 11. Optional cosign verify-blob if you published signatures"
  fi
  echo "[ ] 14. If credentials may have leaked during failed rollout: SECURITY.md rotation"
} >"${CANARY_FILE}"

echo "==> canary checklist"
cat "${CANARY_FILE}"

echo "==> release-check ok"
echo "MODE=$([[ -n "${STABLE_TAG}" ]] && echo stable || echo snapshot)"
echo "BINARY=${OUT_BIN}"
echo "STABLE_BINARY=${STABLE_BIN}"
[[ -n "${SUM_FILE}" ]] && echo "CHECKSUMS=${SUM_FILE}" && cat "${SUM_FILE}"
echo "CANARY_CHECKLIST=${CANARY_FILE}"
[[ -n "${COSIGN_SIG}" ]] && echo "COSIGN_SIGNATURE=${COSIGN_SIG}"
[[ -n "${COSIGN_BUNDLE}" ]] && echo "COSIGN_BUNDLE=${COSIGN_BUNDLE}"
./"${STABLE_BIN}" -h >/dev/null 2>&1 || true
echo "ldflags VersionInfo=${VERSION} Commit=${COMMIT}"
