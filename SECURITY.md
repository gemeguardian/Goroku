# Security

Practical threat model and secret-handling notes for Goroku operators.

## Threat model (lite)

| Surface | Risk | Mitigations in tree |
|---------|------|---------------------|
| Web panel | Session theft, CSRF, open bind | Binds `127.0.0.1` by default; Docker uses `0.0.0.0` only with `DOCKER` set. `--web-bind` / `GOROKU_IP` can override. Non-loopback binds emit a startup warning. Forwarding headers (`CF-Connecting-IP`, `X-Forwarded-For`) are **fail-closed** unless `GOROKU_TRUSTED_PROXIES` CIDRs are configured and `RemoteAddr` matches. Setup token, CSRF, session rotation. Prefer reverse proxy + TLS if exposed. |
| SSH reverse tunnel (`--ssh-tunnel`) | Exposes panel to public tunnel providers | **Off by default.** Only enable when you accept third-party tunnel risk. |
| MTProto proxy flags | Wrong secret / MITM path | `--proxy-host` + `--proxy-port` + `--proxy-secret` required together; `--proxy-pass` is a deprecated alias for the secret, **not** SSH. |
| Yaegi `.eval` | RCE as the bot owner | Owner-only; single concurrent worker; eval runs in a **child process** killed on timeout/cancel (process group SIGKILL). No shared memory with the bot (snapshots only). |
| Native Go plugins | In-process code load | Owner-only install commands and callback owner rechecks; plugins are arbitrary code and cannot be fully unloaded from memory. Review source before installation. |
| Remote module download | SSRF / hostile payload | HTTPS-only; private/loopback/link-local/CGNAT targets blocked; redirects and response size are bounded. Persisted restores require an exact recorded source SHA-256 match. |
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

## Incident response

Actionable playbook for the common Goroku incident classes. Every sub-play
starts from "stop the process / freeze the bot" and ends with "verify the old
secret is dead and the new one works".

### Leaked bot token (Bot API)

1. Revoke + reissue via BotFather immediately — see
   [BotFather token rotation checklist](#botfather-token-rotation-checklist-manual).
   Maintainers cannot revoke it for you; the old token stays valid until
   BotFather revokes it.
2. Stop Goroku (`SIGTERM` / `systemctl stop goroku`).
3. Update the bot token in the runtime config under the data root (typically
   `config-<telegram-id>.json` or the module settings path that stores it).
   Keep file mode `0600`.
4. Start Goroku and verify Bot-API-dependent features.
5. Confirm the old token is dead: a Bot API call with the old value must fail
   (`401 Unauthorized`).
6. Treat any backup / log / shared host holding the old token as compromised:
   delete or re-encrypt, and re-run this checklist after restoring any archive
   that still contains the old token.

### Leaked / compromised setup or session token

- **Web setup token (`GOROKU_SETUP_TOKEN`):** it is cleared after successful
  setup and is not used post-setup. If it leaked before setup completed,
  restart with a fresh `GOROKU_SETUP_TOKEN`; the old value is no longer
  authoritative once setup finishes. If setup was completed with a leaked
  token, treat the resulting web session as untrusted and rotate session
  state (next bullet).
- **Telegram user session (`goroku-<id>.session`):** log out other sessions in
  Telegram settings, delete `goroku-<id>.session`, and re-authenticate via
  web or CLI. Treat the account as potentially compromised — review recent
  activity. See [M0.1 — secret rotation procedure](#m01--secret-rotation-procedure),
  step 2.
- **Web session cookie / setup session:** session expiry and rotation already
  exist in the web auth layer. To force-rotate, stop the process, remove the
  on-disk session state under the data root (or restart with a fresh setup
  token to re-bootstrap), and have the owner re-authenticate.

### Rotate `GOROKU_TRUSTED_PROXIES`

Use this when a reverse proxy host changed, a proxy IP rotated, or the CIDR
set was found to be too wide / too narrow.

1. Stop Goroku (or, if you can tolerate a brief forwarding-header gap, keep it
   running — without `GOROKU_TRUSTED_PROXIES` the panel is fail-closed and
   `clientIP` falls back to `RemoteAddr`).
2. Update `GOROKU_TRUSTED_PROXIES` in the environment /
   `/etc/goroku/goroku.env` to the new CSV of CIDRs (e.g.
   `10.0.0.0/8,fd00::/8`). Empty/unset = fail-closed (forwarding headers
   ignored).
3. Start Goroku. Verify from a client behind the proxy that `clientIP` is
   derived correctly; verify from a spoofing client (left-injected XFF) that
   the right-most untrusted hop is used.
4. Do **not** set `GOROKU_TRUST_PROXY_HEADERS=1` as a workaround — it is
   deprecated and does not enable header trust without
   `GOROKU_TRUSTED_PROXIES`.

## Public bind and trusted proxy CIDRs

When the web panel binds to anything other than `127.0.0.1` / `::1` / `localhost`
(via `--web-bind`, `GOROKU_IP`, or Docker `0.0.0.0`), Goroku logs a startup
warning. A public bind means the panel is reachable from other hosts; you must
place it behind a reverse proxy that sets forwarding headers and configure
`GOROKU_TRUSTED_PROXIES` with the CIDR(s) of that proxy.

Without `GOROKU_TRUSTED_PROXIES`, forwarding headers (`CF-Connecting-IP`,
`X-Forwarded-For`) are **always ignored** (fail-closed) and `clientIP` falls
back to `RemoteAddr`. This prevents spoofing when no trusted proxy is
configured. When CIDRs are set, `clientIP` walks `X-Forwarded-For` from
right to left and returns the first untrusted hop, defeating spoofing by
clients that inject values on the left.

`GOROKU_TRUST_PROXY_HEADERS` is **deprecated**: setting it without
`GOROKU_TRUSTED_PROXIES` logs a one-time warning and does **not** enable
header trust.

## Delegating rights (`tsec`, `sgroups`)

`owner` is the only unbounded role: an owner reaches `.eval`, `.terminal`,
`.loadmod` and the session file. Everything else is delegation, and delegation
is deliberately not transitive.

- **Command-scoped rules** (`.tsec <user> command <cmd>`, and sgroup
  permissions with `rule_type: command`) grant exactly the named command.
- **Module-scoped rules** (`.tsec <user> module <Module>`) grant the module's
  commands — except in *privileged modules*, where they grant only the
  read-only ones. The privileged set is `GorokuSecurity`, `GorokuBackup`,
  `GorokuConfig`, `Updater`, `Loader`, `Eval`, `Terminal`
  (`privilegedModules` in `goroku/security.go`); a wholesale grant on any of
  them would otherwise be equivalent to handing out owner rights. Inline
  buttons belonging to a privileged module are owner-only outright, because a
  button carries no per-command metadata.
- **Owner-only commands** — everything in `GorokuSecurity` that rewrites the
  `owner`/`sudo`/`tsec`/`sgroups` lists, plus `.eval`, `.terminal` and the
  loader's install commands — are refused for non-owners regardless of which
  rule matched. The handlers that hand out owner rights re-check ownership
  themselves rather than trusting the dispatcher.
- **The bounding mask is a ceiling, including for delegation.** A command the
  bounding mask clips to no permissions at all cannot be revived by a tsec or
  sgroups rule.

Practical consequence: delegating `GorokuSecurity` gives a user the ability to
*read* the owner/sudo/tsec lists, never to change them. To let someone change
policy, make them an owner and accept that this is full host access.

## Native plugin safety

Native Go modules are arbitrary code loaded into the Goroku process. Owner-only
commands and callback owner checks control who may initiate an install, but they
do not sandbox module behavior. Review source before installation and treat a
malicious module execution as full process-user compromise.

Remote downloads remain HTTPS-only with SSRF, redirect, and response-size
controls. Installation records the exact source SHA-256 in
`Loader.module_digests`; persisted restore refuses changed or unrecorded remote
source. Hot-load registry and source changes are transactional with rollback,
and install execution is audited. Native code mapped by `plugin.Open` still
cannot be fully unloaded from process memory.

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
- Lightweight SBOM: `bash scripts/generate-sbom.sh [OUT_DIR]` → default `dist/sbom`; minimal CycloneDX 1.5 JSON (`sbom.cdx.json` / `sbom-components.json` from `go list -m -json`); prints `SBOM_ARTIFACT_PATH=…` and writes `SBOM_ARTIFACTS.txt` / `dist/SBOM_LATEST_PATH.txt`. Uploaded as a required CI artifact; optional Syft step is continue-on-error.
- **Not yet automated:** host GitHub secret-scanning org policy, PR dependency-review, license policy. Cosign is optional local (`COSIGN_YES=1`), not a CI hard gate.
- Do **not** mass-upgrade `gotd/td` as part of routine security scans; treat that as a separate migration.

## Reporting

Open a private security report via the project maintainers or GitHub security advisory if available. Do not attach live session files or tokens.
