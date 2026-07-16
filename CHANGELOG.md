# Goroku Changelog

## Unreleased

### M4.2 — Yaegi isolation
- Go `.eval` runs in an out-of-process Yaegi worker (`--yaegi-worker` re-exec)
- Timeout/cancel kills the worker process group via `ProcessExecutor`
- Snapshots only: no shared `msg`/`client`/`db`/`loader` memory with the bot

### M6 — Architecture hardening
- Module factories + lifecycle/SendReady sequencing
- DocumentStore + AssetRepository (no DB→full client)
- Dispatch pipeline with reason codes; regex compiled at register
- Web service split (no package-global `web.Instance`)
- Client file split

### M7 — Typing / schema
- Typed web runtime registry (`RegisterClient`, no fake TGID)
- `ConfigField` / `ModuleWithConfigSchema` + Settings module schema adoption
- `webiface.ModulesRegistry` for `RuntimeClient.Loader`
- Compile-time consumer-port asserts (`TelegramClient`, `Database`, `ModulesRegistry`)

### M8 — Ops surface
- CLI flags (proxy/QR/no-auth), `/health` `/healthz` `/readyz`
- Honest README / ops docs

### M9 — CI quality gates
- M9.1: pin Go/actions, gofmt/vet/tidy/parity, clean build
- M9.2: pinned advisory `govulncheck`; optional strict via `GOVULNCHECK_STRICT=1`; SBOM helper script (`scripts/generate-sbom.sh`)
- M9.3: soft 20% coverage floor on critical packages
- M9.4: `scripts/test-critical.sh`
- **Out of scope:** mass `gotd/td` upgrade, secret-scan automation, hard vuln gate on stdlib noise

### M10 — Release packaging
- `VersionInfo` / `Commit` via `-ldflags`; `GetVersionString` surfaces VersionInfo
- Multi-stage non-root `Dockerfile` + `HEALTHCHECK` on `/healthz`
- Optional `.goreleaser.yml`; docs: SECURITY, QUICKSTART, ARCHITECTURE, OPERATIONS, CONFIGURATION, RELEASE
- Residuals: signed release, canary, full SBOM/Syft pipeline

# Goroku 1.0.0

- Full remake code. Port from python Heroku 2.0 to Golang.
- Rename to Goroku.
