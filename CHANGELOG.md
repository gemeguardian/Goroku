# Goroku Changelog

## Unreleased

> **SemVer note (next tag):** when cutting the next release, tag as `vX.Y.Z` (e.g. `v1.0.1` or `v1.1.0` depending on break/compat), inject `VersionInfo`/`Commit` via `-ldflags` (see `docs/RELEASE.md`), and move the sections below under that tag heading. Default product string remains `1.0.0` until the first post-remake tag after this Unreleased block.

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
- `ConfigField` / `ModuleWithConfigSchema` on built-ins that expose config (Settings, Loader, Help, Updater, Translate, Terminal, Tester, GorokuInfo, GorokuConfig, APILimiter)
- `webiface.ModulesRegistry` for `RuntimeClient.Loader`
- Compile-time consumer-port asserts (web + inline: `TelegramClient`, `Database`, `ModulesRegistry`, `InlineUserBot`, `SecurityChecker`)
- Typed `/health` JSON payload; cache perms accessors (`AsPrivate` / `AsChannelParticipant` / `AsChatParticipant`)

### M8 — Ops surface
- CLI flags (proxy/QR/no-auth), `/health` `/healthz` `/readyz`
- Honest README / ops docs

### M9 — CI quality gates
- M9.1: pin Go/actions, gofmt/vet/tidy/parity, clean build
- M9.2: pinned advisory `govulncheck`; `govulncheck-direct` hard gate (`GOVULNCHECK_DIRECT_ONLY=1`); optional strict via `GOVULNCHECK_STRICT=1`; tracked-tree `scripts/scan-secrets.sh`; minimal CycloneDX SBOM (`scripts/generate-sbom.sh` → `sbom.cdx.json`)
- M9.3: soft 20% coverage floor on critical packages
- M9.4: `scripts/test-critical.sh`
- **Out of scope:** mass `gotd/td` upgrade, hard vuln gate on stdlib noise, mandatory Syft/cosign

### M10 — Release packaging
- `VersionInfo` / `Commit` via `-ldflags`; `GetVersionString` surfaces VersionInfo
- Multi-stage non-root `Dockerfile` + `HEALTHCHECK` on `/healthz` (port 8080)
- `scripts/release-check.sh` + canary checklist output (`dist/CANARY_CHECKLIST.txt`)
- Optional cosign verify docs in `docs/RELEASE.md`
- Optional `.goreleaser.yml`; docs: SECURITY, QUICKSTART, ARCHITECTURE, OPERATIONS, CONFIGURATION, RELEASE
- Residuals: mandatory signed release pipeline, full Syft in CI

### M0.1 — Secret rotation docs
- `SECURITY.md` threat model + BotFather token rotation checklist (manual)
- Linked from README docs table

# Goroku 1.0.0

- Full remake code. Port from python Heroku 2.0 to Golang.
- Rename to Goroku.
