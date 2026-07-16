# Release

Practical release path for Goroku (M10 subset).

## Versioning

- Product version string: `goroku.VersionInfo` (default `1.0.0`).
- Inject at build:

```bash
VERSION=1.0.0
COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo none)
go build -trimpath -ldflags "-s -w -X goroku/goroku.VersionInfo=${VERSION} -X goroku/goroku.Commit=${COMMIT}" -o goroku_bin .
./goroku_bin  # version surfaces in .info / startup messages via GetVersionString
```

Tag releases as SemVer (`v1.0.0`). Keep `CHANGELOG.md` in sync with the tag.

## Pre-release check script

```bash
export TMPDIR=/root/.cache/go-tmp   # or any large temp dir for -race
bash scripts/release-check.sh
# VERSION=1.2.3 OUT_DIR=dist bash scripts/release-check.sh
```

What it does:

1. Builds with `-ldflags` (`VersionInfo` + `Commit`) into `dist/goroku_<ver>_<os>_<arch>` and `dist/goroku`
2. Runs critical tests: `go test -race -count=1 ./goroku/ ./goroku/web/`
3. Writes `dist/SHA256SUMS`
4. Optionally generates SBOM under `dist/sbom` (`RELEASE_CHECK_SBOM=0` to skip)
5. Writes `dist/CANARY_CHECKLIST.txt` (operator canary / rollback smoke steps)

## Manual release checklist

1. Clean tree; `bash scripts/release-check.sh` (or CI-equivalent) passes.
2. Update `CHANGELOG.md` for the tag.
3. Tag: `git tag -a vX.Y.Z -m "vX.Y.Z"`.
4. Build artifacts (example Linux):

```bash
VERSION=1.0.0
COMMIT=$(git rev-parse --short HEAD)
for pair in linux/amd64 linux/arm64; do
  OS=${pair%/*}; ARCH=${pair#*/}
  out="dist/goroku_${VERSION}_${OS}_${ARCH}"
  GOOS=$OS GOARCH=$ARCH CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X goroku/goroku.VersionInfo=${VERSION} -X goroku/goroku.Commit=${COMMIT}" \
    -o "$out" .
done
(cd dist && sha256sum goroku_* > SHA256SUMS)
```

5. Publish binaries + `SHA256SUMS` (+ optional detached signatures / cosign — see below).
6. Optional: `docker build -t ghcr.io/<org>/goroku:vX.Y.Z .`

## GoReleaser (optional)

A minimal `.goreleaser.yml` is provided. If `goreleaser` is installed:

```bash
goreleaser release --snapshot --clean   # local dry-run
goreleaser release --clean              # on tag push with GITHUB_TOKEN
```

CI release job is **not** required for this subset; manual or GoReleaser both OK.

## Production install notes

- Prefer prebuilt binary + `--data-root` + **`--no-git`** so the process never runs destructive git helpers.
- Do not enable `--ssh-tunnel` unless you accept public tunnel exposure.
- Verify `/healthz` and `/readyz` after deploy.
- Backup data root before upgrade; restore tested path: stop → restore files → start.

## Canary and rollback

### Canary (single host or small cohort)

1. Snapshot data root (config, sessions, modules, DB). See [OPERATIONS.md](OPERATIONS.md).
2. Install the new binary beside the old one (keep the previous path or rename `goroku` → `goroku.prev`).
3. Start with the same `--data-root` flags as production; prefer `--no-git`.
4. Smoke: `curl -fsS http://127.0.0.1:${PORT:-8080}/healthz` and `/readyz`; owner `.info` / critical modules; watch logs for panic/auth loops.
5. Hold canary for a soak window (minutes to hours depending on risk). Only then replace other hosts / promote the tag.

### Rollback

1. Stop the new process (`SIGTERM`).
2. Restore the previous binary (`goroku.prev` or last known-good artifact from `SHA256SUMS`).
3. If the new version migrated on-disk state and is incompatible: restore the data-root snapshot taken before the canary.
4. Start the old binary with the same flags; re-check `/healthz` + `/readyz`.
5. If credentials may have been exposed during the failed rollout, rotate per [SECURITY.md](../SECURITY.md).

Do **not** `git reset` production hosts that use `--no-git` binary installs; replace artifacts only.

## SBOM (lightweight)

Without adding heavy deps:

```bash
go build -o "${TMPDIR:-/tmp}/goroku_bin" .
GOROKU_BIN="${TMPDIR:-/tmp}/goroku_bin" bash scripts/generate-sbom.sh dist/sbom
# SBOM_ARTIFACT_PATH printed; index at dist/sbom/SBOM_ARTIFACTS.txt
# also: go-modules.json, go-modules.txt, go-modules-direct.txt,
#       sbom.cdx.json / sbom-components.json (minimal CycloneDX 1.5 from go list),
#       optional binary-version-m.txt
# path pointer: dist/SBOM_LATEST_PATH.txt
```

For richer Syft/CDX tooling, install those separately and attach artifacts at publish time.

## Cosign (optional)

Artifact signing with [cosign](https://github.com/sigstore/cosign) is **optional** and not required for a Goroku release.

```bash
# keyless (OIDC) example — requires cosign + identity provider setup
cosign sign-blob --bundle dist/goroku.sigbundle dist/goroku_1.0.0_linux_amd64
cosign verify-blob --bundle dist/goroku.sigbundle \
  --certificate-identity-regexp '.*' \
  --certificate-oidc-issuer-regexp '.*' \
  dist/goroku_1.0.0_linux_amd64

# or static key pair
cosign sign-blob --key cosign.key dist/goroku_1.0.0_linux_amd64 > dist/goroku.sig
cosign verify-blob --key cosign.pub --signature dist/goroku.sig dist/goroku_1.0.0_linux_amd64
```

Operator verify (after download):

```bash
# 1) always check SHA-256 first
sha256sum -c SHA256SUMS

# 2) if the publisher attached a cosign bundle / signature:
cosign verify-blob --key cosign.pub --signature goroku.sig goroku_1.0.0_linux_amd64
# or keyless bundle (identity/issuer must match the publisher's policy):
cosign verify-blob --bundle goroku.sigbundle \
  --certificate-identity-regexp '<publisher-identity>' \
  --certificate-oidc-issuer-regexp '<issuer>' \
  goroku_1.0.0_linux_amd64
```

Publish `SHA256SUMS` either way; cosign bundles are additive. CI does not hard-require cosign.
`scripts/release-check.sh` prints a canary checklist (`dist/CANARY_CHECKLIST.txt`) that includes optional cosign verify.

## gotd/td pin (do not casual-upgrade)

`github.com/gotd/td` is pinned at **v0.120.0** in `go.mod` on purpose.

Why:

- Goroku’s MTProto surface (session, dispatcher, cache TL types, HTML entities, mtproxy) is tightly coupled to gotd APIs and generated `tg` types.
- A mass bump often changes Layer constants, optional field flags, auth/update delivery, and package layout — high regression risk for userbots.
- Version strings in modules (e.g. `.info`) still document the expected gotd baseline; keep them honest when you do upgrade.

How to upgrade later (dedicated PR, not bundled with security noise fixes):

1. Read gotd release notes / changelog from v0.120.0 → target.
2. Bump only `github.com/gotd/td` (and minimal required gotd/* companions) in `go.mod`.
3. `go test -race -count=1 ./goroku/ ./goroku/web/ ./goroku/modules/` plus manual login/send/update smoke.
4. Fix compile breaks (TL unions, auth helpers, uploader/downloader APIs).
5. Update displayed gotd version strings and `CHANGELOG.md`.
6. Do **not** combine with persistence/dispatcher refactors or broad `x/*` bumps in the same PR.

Routine `govulncheck` / SBOM work must not force a gotd upgrade.

## Out of scope for this subset

- Mandatory cosign / full Syft SBOM pipeline in CI
- Hardened systemd unit (see ops residual)
- Automatic multi-region promotion
