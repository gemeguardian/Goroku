# Release

Practical release path for Goroku (M10 subset). Signed SBOM/canary automation can come later.

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

## Manual release checklist

1. Clean tree; `go test -race ./...` and `go build` pass on CI-equivalent steps.
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

5. Publish binaries + `SHA256SUMS` (+ optional detached signatures).
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

## SBOM (lightweight)

Without adding heavy deps:

```bash
go build -o "${TMPDIR:-/tmp}/goroku_bin" .
GOROKU_BIN="${TMPDIR:-/tmp}/goroku_bin" bash scripts/generate-sbom.sh dist/sbom
# produces go-modules.json, go-modules.txt, binary-version-m.txt
```

For CycloneDX/Syft, install those tools separately and attach artifacts at publish time.

## Out of scope for this subset

- Cosign / full Syft SBOM pipeline
- Hardened systemd unit (document later)
- Automatic promotion / canary
