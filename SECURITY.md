# Security

Practical threat model and secret-handling notes for Goroku operators.

## Threat model (lite)

| Surface | Risk | Mitigations in tree |
|---------|------|---------------------|
| Web panel | Session theft, CSRF, open bind | Binds `127.0.0.1` by default; Docker uses `0.0.0.0` only with `DOCKER` set. Setup token, CSRF, session rotation. Prefer reverse proxy + TLS if exposed. |
| SSH reverse tunnel (`--ssh-tunnel`) | Exposes panel to public tunnel providers | **Off by default.** Only enable when you accept third-party tunnel risk. |
| MTProto proxy flags | Wrong secret / MITM path | `--proxy-host` + `--proxy-port` + `--proxy-secret` required together; `--proxy-pass` is a deprecated alias for the secret, **not** SSH. |
| Yaegi `.eval` | RCE as the bot owner | Owner-only; single concurrent worker; eval runs in a **child process** killed on timeout/cancel (process group SIGKILL). No shared memory with the bot (snapshots only). |
| Native Go plugins | In-process code load | Owner-only; untrusted installs need `-confirm` or trusted content SHA-256; plugins cannot be fully unloaded from memory. |
| Remote module download | SSRF / hostile payload | HTTPS-only; private/loopback/link-local/CGNAT targets blocked. |
| Terminal module | Shell RCE | Owner-only; treat as full host compromise capability. |
| Git updater | Local worktree mutation | Prefer binary installs with `--no-git`. See residual below. |
| Config / sessions | Credential leak from disk or backups | Store under a dedicated `--data-root` with tight permissions (`0600` files). Never commit runtime files. |

Attacker goals that matter: steal Telegram session/API credentials, run code as the process user, or take over the web setup flow.

## Secrets inventory (runtime, not git)

Typical sensitive material under the data root (default: cwd / `DOCKER` → `/data` / `--data-root`):

- `config.json` — `api_id`, `api_hash`, and other global config
- `config-<telegram-id>.json` — per-account DB (may include bot tokens and module secrets)
- `goroku-<telegram-id>.session` — MTProto session (account access)
- Setup token via `GOROKU_SETUP_TOKEN` (cleared after successful setup)
- Optional MTProto proxy secret on the CLI (prefer not to put secrets in shell history)

Tracked source must not contain real tokens. Runtime files are gitignored.

## M0.1 — secret rotation procedure

If a token or credential may have been exposed (logs, backups, shared host, leaked config):

1. **Bot API token** — see [BotFather token rotation checklist](#botfather-token-rotation-checklist-manual) below. Maintainers **cannot** rotate your token for you.
2. **Telegram user session** — if session file leaked: log out other sessions in Telegram settings, delete `goroku-<id>.session` (and related config if needed), re-authenticate via web or CLI.
3. **API id/hash** — if compromised or shared widely: create a new app at [my.telegram.org/apps](https://my.telegram.org/apps), update `api_id`/`api_hash` in `config.json`, re-login if sessions break.
4. **Web setup token** — restart with a fresh `GOROKU_SETUP_TOKEN` only before setup completes; after setup it is not used.
5. **Backups** — treat backup archives as secret-bearing. Rotate credentials **before** restoring an old backup that still contains the old secrets, or re-rotate after restore.
6. **Do not** paste real secrets into issues, commits, CI logs, or chat.

Rotation completeness criteria:

- Old bot token fails against Bot API
- New secrets live only under the data root with restricted permissions
- Git history and tracked tree have no token signatures

### BotFather token rotation checklist (manual)

Operators own this end-to-end. Goroku cannot revoke or reissue Bot API tokens.

1. Open Telegram and message [@BotFather](https://t.me/BotFather).
2. Send `/mybots` → select the bot → **API Token** (or use `/revoke` / **Revoke current token** if offered).
3. Confirm revoke so the **old** token is invalidated immediately.
4. Copy the **new** token once; store it only in the runtime data root (never in git, CI logs, or chat).
5. Stop Goroku (`SIGTERM` / process manager stop).
6. Update the bot token field in the relevant runtime config under the data root (typically `config-<telegram-id>.json` or the module/settings path where the bot token was stored). Keep file mode `0600`.
7. Start Goroku and verify bot features that use Bot API (e.g. notifications / web login helpers if enabled).
8. Confirm the old token is dead: a Bot API call with the old value must fail (`401 Unauthorized`).
9. If the old token may have appeared in backups, logs, or shared hosts: treat those artifacts as compromised, delete or re-encrypt them, and re-run this checklist after any restore of an archive that still holds the old token.
10. Optionally rotate related secrets in the same incident window (session file, `api_hash`, setup token) using the steps above.

## Residual risks (honest)

- **Yaegi eval** runs out-of-process and is killable on timeout, but still full RCE as the process user while the worker is alive. Snapshots only: no live `msg`/`client`/`db`/`loader` shared with the parent; `Loader` is unavailable in the worker.
- **Native plugins** cannot be fully unloaded after `plugin.Open`.
- **`CheckBranch` / `ResetToMaster`** can hard-reset a non-master git worktree for non-allowlisted accounts when git is enabled. For production binary deploys use `--no-git` (or `GOROKU_NO_GIT=1`) so the process does not perform destructive git operations.
- Running as root is discouraged (`--root` / `force_insecure` / `DOCKER` / `NO_SUDO` only when intentional).

## Dependency / supply-chain checks (M9.2)

- CI runs pinned **govulncheck** via `scripts/govulncheck.sh`:
  - main job: full scan **advisory** (stdlib / transitive noise does not block merge)
  - `govulncheck-direct` job: fails only on vulns whose vulnerable module is a **direct** `go.mod` require (`GOVULNCHECK_DIRECT_ONLY=1`, stdlib ignored via `-json` filter)
  - optional full strict job on schedule / `workflow_dispatch` (`GOVULNCHECK_STRICT=1`)
- **Secret scanning (tracked tree):** `bash scripts/scan-secrets.sh` — high-entropy / known secret filename patterns on `git ls-files` only; fails CI if hits. Does **not** scan untracked runtime files (`config.json`, sessions) — keep those gitignored.
- Lightweight SBOM: `bash scripts/generate-sbom.sh [OUT_DIR]` → default `dist/sbom`; minimal CycloneDX 1.5 JSON (`sbom.cdx.json` / `sbom-components.json` from `go list -m -json`); prints `SBOM_ARTIFACT_PATH=…` and writes `SBOM_ARTIFACTS.txt` / `dist/SBOM_LATEST_PATH.txt`.
- **Not yet automated:** host GitHub secret-scanning org policy, full Syft pipeline, PR dependency-review, license policy, mandatory cosign (optional — see `docs/RELEASE.md`).
- Do **not** mass-upgrade `gotd/td` as part of routine security scans — treat that as a separate migration (`docs/RELEASE.md`).

## Reporting

Open a private security report via the project maintainers or GitHub security advisory if available. Do not attach live session files or tokens.
