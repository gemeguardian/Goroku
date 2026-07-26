# Operations

Runbook for operators. No secrets in this file.

## Run

```bash
# Dev / simple
./goroku_bin

# Production-oriented
./goroku_bin --data-root /var/lib/goroku --no-git --port 8080
```

Environment (common):

| Variable | Effect |
|----------|--------|
| `DOCKER` | Treat as container: data default `/data`, bind `0.0.0.0`, skip some root prompts |
| `GOROKU_IP` | Override listen / advertised host |
| `GOROKU_NO_GIT=1` | Same as `--no-git` |
| `GOROKU_SETUP_TOKEN` | Pre-seed web setup token (cleared after setup) |
| `GOROKU_TRUSTED_PROXIES` | Comma-separated CIDRs of trusted reverse proxies. **Required** to honour `X-Forwarded-For` / `CF-Connecting-IP`: without it forwarding headers are ignored and `RemoteAddr` is used. See SECURITY.md |
| `GOROKU_TRUST_PROXY_HEADERS` | **Deprecated and inert on its own.** Kept for compatibility; without `GOROKU_TRUSTED_PROXIES` it enables nothing |
| `GOROKU_WEB_BIND` | Listen address for the web panel (default loopback). Bind non-loopback only behind a TLS proxy |
| `GOROKU_DEBUG=1` | Debug logging |
| `GOROKU_WEB_RESOURCES` | Override web static resources directory |
| `NO_SUDO` | Allow root path without interactive prompt (with root guard logic) |

## Health endpoints

When web is enabled (not `--no-web`):

| Endpoint | Response |
|----------|----------|
| `GET /healthz` | `200` + plain `ok` (liveness: the process is serving HTTP). Static by design — it stays `200` even with Telegram down |
| `GET /readyz` | `200` + `ok` while onboarding, or once at least one client is connected. **`503` + reason** once setup has completed and no registered client has a live MTProto connection |
| `GET /health` | `200` JSON: `status`, `clients`, `clients_connected`, `stuck_evals`, `setup_completed`, `version` — **no secrets** |

`clients_connected` below `clients` means an account is registered but its
MTProto transport is down; the supervisor is reconnecting or the process is
about to restart itself. `stuck_evals` counts `.eval` goroutines abandoned at
their timeout — Yaegi cannot be cancelled, so a number that only grows means a
restart is due.

**Point external monitoring at `/readyz`, not only `/healthz`:** a dead
MTProto connection leaves the process happily serving HTTP.

HEAD is accepted on these routes. Example probe:

```bash
curl -fsS "http://127.0.0.1:${PORT:-8080}/healthz"
curl -fsS "http://127.0.0.1:${PORT:-8080}/readyz"
curl -fsS "http://127.0.0.1:${PORT:-8080}/health"    # includes version string
```

## Restart

- Telegram: owner `.restart` (Updater module) requests coordinated shutdown then process replace.
- Process: send `SIGTERM` / `SIGINT`; app shuts down workers then exits (or restarts if restart was requested and not `--sandbox`).
- Restart guard env: `GOROKU_DO_NOT_RESTART` / `GOROKU_DO_NOT_RESTART2` limit restart loops from `main`.
- If `main.go` sits next to the binary, restart may run `go build` before `exec` (dev layout). Production binary installs should not rely on a local toolchain.

## Backup and restore

Built-in module `GorokuBackup` (owner commands; prefix depends on user settings, often `.`):

| Command | Purpose |
|---------|---------|
| `backupdb` | Database backup |
| `backupmods` | Modules backup |
| `backupall` / `backup` | Combined |
| `set_backup_period` | Schedule automatic backups |
| restore commands | Restore from backup archives (validated; module sources compile-checked) |

Operational notes:

- Prefer scheduled backups **before** upgrades.
- Backup archives contain secrets — store offline with restricted access.
- After restore, rotate credentials if the archive may have been shared (see [SECURITY.md](../SECURITY.md)).
- Automatic period is configured in-bot; `0` / never disables the loop.

## Updates

| Mode | Behavior |
|------|----------|
| Git checkout + `.update` | `git pull` then restart (requires git, not `--no-git`) |
| Binary / Docker | Replace artifact + restart process; use `--no-git` |
| Autoupdate config | Optional; still git-based when enabled |

**Do not** run production from a dirty writable git worktree if you care about accidental resets. With git enabled, non-allowlisted accounts on non-master branches may trigger hard reset helpers in `CheckBranch`. Prefer:

```bash
./goroku --data-root /var/lib/goroku --no-git
```

## Docker

```bash
docker build -t goroku:local .
docker run --rm -e DOCKER=1 \
  -v /var/lib/goroku:/data \
  -p 8080:8080 \
  goroku:local
```

Runs as non-root user inside the image. Persist `/data` (or your data root).

## Logs

- Process logs to files under the working/data layout (e.g. `goroku.log` / rotated names depending on logger config).
- Do not ship log bundles that may contain message content or tokens without redaction.

## Incident checklist

1. Stop process (`SIGTERM`).
2. Snapshot data root (config, sessions, modules).
3. Rotate secrets ([SECURITY.md](../SECURITY.md)).
4. Restore known-good backup if integrity is in doubt.
5. Restart with `--no-git` and localhost web unless tunnel is required.

## Canary deploy and rollback

Short path; full checklist lives in [RELEASE.md](RELEASE.md).

**Canary**

1. Backup data root.
2. Keep previous binary as `goroku.prev` (or known-good artifact verified via `SHA256SUMS`).
3. Install new binary; start with same `--data-root` / `--no-git` flags.
4. Probe `GET /healthz` and `GET /readyz`; exercise owner smoke commands; watch logs.

**Rollback**

1. Stop new process.
2. Restore previous binary; restore data-root snapshot if on-disk format is incompatible.
3. Start previous binary; re-check health endpoints.
4. Rotate credentials if the failed rollout may have leaked secrets.

Binary installs should not rely on `git reset` for rollback when running with `--no-git`.
