# Goroku Changelog

All notable changes to Goroku are recorded here. The format is based on
[Keep a Changelog](https://keepachangelog.com/), and the project adheres to
[Semantic Versioning](https://semver.org/).

Until the first real SemVer tag is cut, the default product version string
remains `1.0.0` (see `goroku/version.go`).

## [Unreleased]

> **SemVer note (next tag):** when cutting the next release, tag as `vX.Y.Z`
> (e.g. `v0.9.0-beta` or `v1.0.1`/`v1.1.0` depending on break/compat), inject
> `VersionInfo`/`Commit` via `-ldflags`, and move the
> sections below under that tag heading. The default product string remains
> `1.0.0` until the first post-remake tag after this Unreleased block.

### Security
- A module-scoped `tsec`/`sgroups` rule on a privileged module (`GorokuSecurity`,
  `GorokuBackup`, `GorokuConfig`, `Updater`, `Loader`, `Eval`, `Terminal`) no
  longer confers that module's owner-only commands. Delegating `GorokuSecurity`
  used to include `.owneradd`, which let the delegate promote themselves to
  owner and reach `.eval`/`.terminal`. Command-scoped rules are unchanged
- Every `GorokuSecurity` command that rewrites policy is `OnlyOwner`;
  `.owneradd` and its inline confirmation button re-check ownership themselves
- The bounding mask now bounds delegated rules too: a command it clips to no
  permissions cannot be revived by a `tsec`/`sgroups` match
- A message with no sender (`SenderID == 0` — anonymous admins, channel-signed
  posts) no longer matches an unauthorized client's zero TGID and pass as owner
- `.evalpy` no longer puts the database dump in `python3 -c` argv, where
  `/proc/<pid>/cmdline` exposed the bot token, Redis URI and api_hash to any
  local user. Script goes on stdin, context in the environment, and bot
  token / DB URIs / loader token are redacted from the eval context entirely
- `.eval` output is censored like `.terminal` output was

### Reliability
- The MTProto connection is supervised: when `client.Run` returns, it
  reconnects with exponential backoff (1s..60s, jittered) and after 10
  consecutive failures asks the process to restart. `AUTH_KEY_UNREGISTERED` is
  terminal and not retried
- `GET /readyz` answers 503 with a reason once setup has completed and no
  registered client is connected; it used to answer a static `ok` while the bot
  stood dead. `/healthz` stays static (liveness); `/health` gained
  `clients_connected` and `stuck_evals`
- Entity, permission, full-user and full-channel caches are swept and hard
  capped; the package previously contained no `delete` at all and grew until a
  restart
- The web endpoint rate-limit map drops expired keys and is bounded
- The single global subprocess slot is split into independent `interactive`,
  `build` and `eval` pools, so `.terminal` no longer blocks plugin builds. A
  saturated pool answers "busy" instead of queueing the caller silently
- Outgoing Telegram calls made from the update handler (double-prefix edit,
  "busy" reply) moved off the update-reading goroutine
- Floodwait sleeps in the API limiter honour context cancellation

### Data races
- `ForbiddenConstructors` is an atomic snapshot behind
  `SetForbiddenConstructors`/`ForbiddenConstructors()`; it was rewritten by the
  config reload goroutine while every RPC read it
- Account identity (`TGID`, `Username`, `GorokuMe`) is guarded and published
  atomically through `TGIDValue()`, `Username()`, `Me()` and `SetIdentity()`.
  **Breaking for module authors:** the fields are no longer exported

### Fixes
- Anonymous admins and channel-signed posts are identified via
  `SenderChannelID`/`SenderIsChannel` instead of arriving with no sender at all
- Terminal output is truncated on rune boundaries; multi-byte output could
  produce invalid UTF-8
- The root guard detects a missing terminal and prints how to proceed instead
  of failing with a bare "refusing to run as root" under systemd/cron
- `[SecurityDebug]` logs dropped from Info to Debug (they were shipped to the
  Telegram log channel on every button press)
- Removed the dead `goroku/secure` package, whose `EncryptMessageData` logged
  "Skipping encryption" and returned plaintext

### Yaegi `.eval` execution model
- `.eval` runs **in the bot process** with the live `msg`/`client`/`db`/`loader`.
  The out-of-process worker described here previously was never wired in; the
  dead `--yaegi-worker` re-exec path has been removed rather than documented.
- Eval output buffers are bounded and mutex-protected; a hung eval no longer
  races the reader or grows memory without limit
- Goroutines abandoned at the eval timeout are reported as `stuck_evals`
  on `GET /health` — Yaegi cannot be cancelled, so they need a restart

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
- `VersionInfo` / `Commit` via `-ldflags`; `GetVersionString` surfaces VersionInfo; `IsReleaseBuild()` distinguishes release vs dev/default build
- Multi-stage non-root `Dockerfile` + `HEALTHCHECK` on `/healthz` (port 8080)
- `scripts/release-check.sh` + canary checklist; cosign is **mandatory for stable tagged releases** (`COSIGN_YES=1` required) and advisory for snapshots
- `/health` JSON includes `version`; README ops one-liner
- CI: required `generate-sbom.sh` artifact upload; optional pinned Syft (continue-on-error)
- `.goreleaser.yml` injects `VersionInfo`/`Commit` from the tag, builds Linux amd64+arm64, emits `SHA256SUMS`, and attaches the CycloneDX SBOM
- Cosign sign+verify support for release binaries
- Security policy and secret-rotation instructions in `SECURITY.md`

### M0.1 — Secret rotation docs
- `SECURITY.md` threat model + BotFather token rotation checklist (manual)
- Linked from README docs table

## [X.Y.Z] — YYYY-MM-DD (template for the first real SemVer tag)

> Copy this template when cutting the first real SemVer tag (e.g. `v0.9.0-beta`
> or `v1.0.0-rc1`, at the operator's discretion). Replace `X.Y.Z` with the tag
> version (without the leading `v`) and `YYYY-MM-DD` with the release date, then
> move entries from `[Unreleased]` into the appropriate subsections below. Do
> **not** fill this in until the tag is actually cut — no invented dates and no
> fake version numbers.

### Added
-

### Changed
-

### Fixed
-

### Removed
-

# Goroku 1.0.0

- Full remake code. Port from python Heroku 2.0 to Golang.
- Rename to Goroku.
