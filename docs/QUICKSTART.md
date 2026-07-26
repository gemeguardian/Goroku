# Quickstart

Requirements: **Go 1.25.0+** (see `go.mod`), Telegram API credentials from [my.telegram.org/apps](https://my.telegram.org/apps).

## Build and run (local)

```bash
git clone https://github.com/gemeguardian/Goroku
cd Goroku
go build -o goroku_bin .
./goroku_bin
```

Open the printed web URL (default `http://127.0.0.1:8080`), complete setup, then use Telegram.

## Recommended production layout

```bash
go build -ldflags "-X goroku/goroku.VersionInfo=1.0.0" -o /opt/goroku/bin/goroku .
install -d -m 700 /var/lib/goroku
/opt/goroku/bin/goroku --data-root /var/lib/goroku --no-git
```

| Path | Purpose |
|------|---------|
| `/opt/goroku/bin/goroku` | Binary (read-only tree) |
| `/var/lib/goroku/config.json` | API credentials / global config |
| `/var/lib/goroku/config-<id>.json` | Per-account DB |
| `/var/lib/goroku/goroku-<id>.session` | Session |
| `/var/lib/goroku/modules/` | Downloaded module sources |

## Useful flags

| Flag | Meaning |
|------|---------|
| `--port 8080` | Web panel port |
| `--no-web` | Console-only (no dashboard) |
| `--data-root DIR` | Writable runtime root |
| `--no-git` | Disable git ops (`GOROKU_NO_GIT=1`) |
| `--ssh-tunnel` | Expose panel via SSH reverse tunnel (**off by default**) |
| `--qr-login` | With `--no-web`, CLI QR login without y/N prompt |
| `--sandbox` | Do not process-restart after lifecycle restart requests |
| `--root` | Allow running as root |

`--proxy-pass` is a **deprecated alias for `--proxy-secret`** (MTProto), not SSH.

## Health (web enabled)

```bash
curl -sS http://127.0.0.1:8080/healthz   # liveness: ok
curl -sS http://127.0.0.1:8080/readyz    # readiness: ok
curl -sS http://127.0.0.1:8080/health    # JSON: status, clients, setup_completed, version
```

## Honest compatibility

Goroku is **Go**. It does **not** load Python Hikka/FTG modules. Native Go plugins and owner-only Yaegi `.eval` are the extension paths.

More: [ARCHITECTURE.md](ARCHITECTURE.md), [OPERATIONS.md](OPERATIONS.md), [CONFIGURATION.md](CONFIGURATION.md), [../SECURITY.md](../SECURITY.md).
