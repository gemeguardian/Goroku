# Architecture

High-level map of the current Goroku codebase (Go userbot on [gotd/td](https://github.com/gotd/td)).

## Process entry

```text
main.go
  └─ root-user guard (--root / DOCKER / NO_SUDO)
  └─ goroku.NewApp(module factories) → App.Run(ctx)
       ├─ signal.NotifyContext (SIGINT/SIGTERM)
       ├─ bootstrap: flags, data root, config, clients, web
       ├─ module Init → ConfigReady → register commands/watchers
       └─ coordinated shutdown; optional process restart (syscall.Exec)
```

Factories live in `main.go` (one fresh module instance per client). Core app type: `goroku.Goroku` in `goroku/bootstrap.go`.

## Major packages

| Package / area | Role |
|----------------|------|
| `goroku/` | App lifecycle, Telegram client wrappers, dispatcher, DB, config, session, rate limits |
| `goroku/web/` | HTTP panel: login/onboarding, static UI, health, optional SSH tunnel manager |
| `goroku/modules/` | Built-in modules (eval, loader, backup, updater, security, terminal, …) |
| `goroku/cache/` | Entity / full-user / channel / perms cache |
| `goroku/inline/` | Inline bot forms/galleries (token obtainment, callbacks) |
| `goroku/logger/` | Zap logging |
| `user_modules/`, data-root `modules/` | Runtime module sources (not the tracked package path) |

## Runtime data flow

```text
Telegram updates
  → client / dispatcher (bounded workers + rate limiter)
  → command / watcher handlers (owner-aware registration where enforced)
  → Database (local JSON SoT; optional Redis generation mirror)
  → outbound client API (send/edit/…)
```

Persistence:

- Global: `config.json` under data root
- Per account: `config-<telegram-id>.json`, `goroku-<telegram-id>.session`
- Modules downloaded into data-root modules dir (not into tracked `goroku/modules`)

## Web panel

- Default bind: `127.0.0.1` (`GOROKU_IP` override; with `DOCKER` set, bind `0.0.0.0`)
- Routes include setup/login UI plus:
  - `GET /health` — JSON ops snapshot (`status`, `clients`, `setup_completed`, `version`; no secrets)
  - `GET /healthz` — liveness
  - `GET /readyz` — readiness (onboarding without a Telegram client still ready)
- SSH reverse tunnel: `--ssh-tunnel`, off by default; stopped with the web server

## Extension model

1. **Built-in modules** — compiled into the binary via factories in `main.go`.
2. **Native Go plugins** (Linux) — owner install/load (`.dlmod` / `.loadmod` / presets); trust digests / `-confirm`; cannot fully unload from process memory.
3. **Yaegi `.eval`** — owner-only; runs **in the bot process** with the live `msg`, `client`, `db` and `loader` in the interpreter context. That is the point of the command: an out-of-process snapshot cannot call client methods. The cost is that evaluated code is neither sandboxed nor interruptible — Yaegi offers no cancellation, so an eval that exceeds `yaegiEvalTimeout` (15s) reports a timeout while its goroutine keeps running until the process restarts. Abandoned goroutines are counted and reported as `stuck_evals` by `GET /health`; output buffers are bounded (`externalOutputLimit`) and mutex-protected. `.eval` is owner-only and stays that way: it is equivalent to running code as the process user.

There is **no** Python module loader.

## Security-related modules (built-in)

Examples registered from `main.go`: `APIProtection`, `Eval`, `GorokuPluginSecurity`, `GorokuSecurity`, `LoaderModule`, `TerminalMod`, `GorokuBackup`, `Updater`, `GorokuWeb`, …

## Version

`goroku.VersionInfo` (default `1.0.0`) is injectable via `-ldflags -X goroku/goroku.VersionInfo=…`. Optional `goroku.Commit` for VCS short SHA.

## Out of scope / deferred (as of M10 subset)

- Full ops dashboard (module trust UI, log browser) not shipped
- Full plugin code signing deferred (content digest + confirm is the current gate)
