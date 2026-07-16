# Configuration

Short reference for flags, env, and on-disk config. No example secrets.

## CLI flags

| Flag | Default | Notes |
|------|---------|-------|
| `--port` | `8080` | Web panel port |
| `--no-web` | off | Console-only |
| `--no-git` | off | Sets `GOROKU_NO_GIT=1` |
| `--data-root` | (cwd / Docker `/data`) | Writable runtime root |
| `--ssh-tunnel` | **off** | SSH reverse tunnel for web panel |
| `--qr-login` | off | With `--no-web`: QR login without prompt |
| `--no-auth` | off | With `--no-web`: skip interactive login if no sessions |
| `--sandbox` | off | Disable process restart after lifecycle restart |
| `--root` | off | Allow root (also checked in `main`) |
| `--proxy-host` | | MTProto proxy host (with port + secret) |
| `--proxy-port` | `0` | MTProto proxy port |
| `--proxy-secret` | | Hex MTProto secret |
| `--proxy-pass` | | **Deprecated** alias of `--proxy-secret` (not SSH) |

## Environment

| Variable | Notes |
|----------|-------|
| `DOCKER` | Container mode: `/data`, bind `0.0.0.0`, root guard bypass |
| `GOROKU_IP` | Listen / URL host override |
| `GOROKU_NO_GIT` | Disable git |
| `GOROKU_SETUP_TOKEN` | Setup token seed |
| `GOROKU_TRUST_PROXY_HEADERS` | Trust proxy client IP headers |
| `GOROKU_DEBUG` | Debug logs |
| `GOROKU_WEB_RESOURCES` | Static UI resources path |
| `api_id` / `api_hash` | Optional bootstrap from env if config empty (prefer file) |
| `NO_SUDO` | Root-path convenience |

## Files under data root

```text
<data-root>/config.json              # global (api_id, api_hash, …)
<data-root>/config-<tg-id>.json      # per-account module DB
<data-root>/goroku-<tg-id>.session   # MTProto session
<data-root>/modules/                 # downloaded module sources
```

Permissions: keep the directory private (`0700`) and secret files `0600`.

## Module config

Modules expose defaults via `ConfigDefaults` / schema helpers. Secrets should be marked secret in schema so UIs/logs can redact. Prefer changing module options through in-bot config commands rather than hand-editing JSON while the process is running.

## Version at build time

```bash
go build -ldflags "-X goroku/goroku.VersionInfo=1.0.0 -X goroku/goroku.Commit=$(git rev-parse --short HEAD)" -o goroku .
```
