<h1>THIS IS FULL VIBECODE!</h1>
<div align="center">
  <img src="https://raw.githubusercontent.com/gemeguardian/Goroku/master/goroku/assets/1326-command-window-line-flat.webp" height="80">
  <h1>Goroku Userbot</h1>
  <p>Advanced Telegram userbot written in Golang, based on the Heroku python-userbot</p>
  
  <p>
    <a href="#">
      <img src="https://img.shields.io/github/languages/code-size/gemeguardian/Goroku" alt="Code Size">
    </a>
    <a href="#">
      <img src="https://img.shields.io/github/issues-raw/gemeguardian/Goroku" alt="Open Issues">
    </a>
    <a href="#">
      <img src="https://img.shields.io/github/license/gemeguardian/Goroku" alt="License">
    </a>
    <a href="#">
      <img src="https://img.shields.io/github/commit-activity/m/gemeguardian/Goroku" alt="Commit Activity">
    </a>
    <br>
    <a href="#">
      <img src="https://img.shields.io/github/forks/gemeguardian/Goroku?style=flat" alt="Forks">
    </a>
    <a href="#">
      <img src="https://img.shields.io/github/stars/gemeguardian/Goroku" alt="Stars">
    </a>
    <a href="https://go.dev">
      <img src="https://img.shields.io/badge/Language-Go-00ADD8.svg?style=flat&logo=go" alt="Language: Go">
    </a>
    <br>
    <a href="https://github.com/gemeguardian/Goroku/blob/master/README.md">
      <img src="https://img.shields.io/badge/lang-en-red.svg" alt="En">
    </a>
    <a href="https://github.com/gemeguardian/Goroku/blob/master/README_RU.md">
      <img src="https://img.shields.io/badge/lang-ru-green.svg" alt="Ru">
    </a>
  </p>
  
</div>

### Manual Installation (VPS/VDS Server)

---

## ⚠️ Security Notice

> **Important Security Advisory**  
> While Goroku implements extended security measures, installing modules from untrusted developers may still cause damage to your server/account.
> 
> **Recommendations:**
> - ✅ Download modules exclusively from official repositories or trusted developers
> - ❌ Do NOT install modules if unsure about their safety
> - ⚠️ Exercise caution with unknown commands (`.terminal`, `.eval`, `.ecpp`, etc.)
> - Go `.eval` is owner-only and always enabled. It runs **out-of-process via Yaegi** (worker child process); timeout/cancel **kills the worker process group**. No shared memory with the bot (`msg`/`client`/`db` are snapshots; `Loader` unavailable). Concurrency is limited to one worker eval.
> - Native Go plugins (`.dlmod` / `.loadmod` / presets) require owner identity. Unsigned/untrusted installs need explicit `-confirm` (or a trusted content SHA-256). Plugins **cannot be fully unloaded from process memory** after load—unregister only removes handlers.
> - Remote module downloads are HTTPS-only; private/loopback/link-local/CGNAT targets are blocked.

---

## 🚀 Installation

### VPS/VDS
> **Note for VPS/VDS Users:**  
> Add `--ssh-tunnel` to expose the web panel via SSH reverse tunnel (not an MTProto proxy)  
> Add `--no-web` for console-only setup  
> Add `--root` for root users (to avoid entering force_insecure)
<details> <summary><b>Ubuntu / Debian</b></summary>

  ```bash
  sudo apt update && sudo apt install git golang -y && \
  git clone https://github.com/gemeguardian/Goroku && \
  cd Goroku && \
  go build -o goroku_bin && \
  ./goroku_bin
  ```
</details>

<details>
<summary><b>Fedora</b></summary>
  
  ```bash
  sudo dnf update -y && sudo dnf install git golang -y && \
  git clone https://github.com/gemeguardian/Goroku && \
  cd Goroku && \
  go build -o goroku_bin && \
  ./goroku_bin
  ```
</details>

<details>
<summary><b>Arch Linux</b></summary>
  
```bash
sudo pacman -Syu --noconfirm && sudo pacman -S git go --noconfirm --needed && \
git clone https://github.com/gemeguardian/Goroku && \
cd Goroku && \
go build -o goroku_bin && \
./goroku_bin
```
</details>

### Production filesystem layout

Keep the executable and checkout read-only, and pass a writable runtime root with
`--data-root /var/lib/goroku`. Downloaded module sources are stored under
`/var/lib/goroku/modules`; Goroku does not write them into the checked-out
`goroku/modules` package.

```text
/opt/goroku/bin/goroku
/var/lib/goroku/config.json
/var/lib/goroku/config-<telegram-id>.json
/var/lib/goroku/goroku-<telegram-id>.session
/var/lib/goroku/modules/
```

The current CLI stores configuration, per-account JSON databases, and session
files directly in the selected data root. It does not currently create separate
`sessions/` or `database/` directories.

### CLI flags (product contract)

| Flag | Behavior |
|------|----------|
| `--port` | Web panel port (default `8080`) |
| `--no-web` | Console-only: no web dashboard |
| `--no-git` | Disable git operations (`GOROKU_NO_GIT=1`) |
| `--data-root` | Writable runtime root for config/sessions/modules |
| `--ssh-tunnel` | Publish web panel via SSH reverse tunnel (default off) |
| `--qr-login` | With `--no-web`, start CLI QR login without the y/N prompt |
| `--no-auth` | With `--no-web`, skip interactive CLI login when no sessions exist |
| `--sandbox` | Disable process restarts after lifecycle restart requests |
| `--root` | Allow running as root (checked in `main`) |
| `--proxy-host` / `--proxy-port` / `--proxy-secret` | MTProto proxy (all three required together; secret is hex) |
| `--proxy-pass` | **Deprecated alias** for `--proxy-secret` — **not** SSH tunnel |

Web binds to `127.0.0.1` by default (`GOROKU_IP` / Docker may override).

### Ops / health

When the web panel is enabled:

- `GET /health` — JSON: `status`, `clients`, `setup_completed`, `version` (no secrets)
- `GET /healthz` — liveness (`ok`)
- `GET /readyz` — readiness (`ok`; onboarding without a Telegram session is still ready)

```bash
curl -fsS "http://127.0.0.1:${PORT:-8080}/health"   # {"status":"ok",...,"version":"1.0.0"}
curl -fsS "http://127.0.0.1:${PORT:-8080}/healthz"  # ok
curl -fsS "http://127.0.0.1:${PORT:-8080}/readyz"   # ok
```

Full operations dashboard (module trust UI, sanitized log browser, update UI) is **not** shipped yet.

### Docs

| Doc | Contents |
|-----|----------|
| [docs/QUICKSTART.md](docs/QUICKSTART.md) | Build, first run, production layout |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | High-level code map |
| [docs/OPERATIONS.md](docs/OPERATIONS.md) | Run, health, backup, restart, Docker |
| [docs/CONFIGURATION.md](docs/CONFIGURATION.md) | Flags, env, data-root files |
| [docs/RELEASE.md](docs/RELEASE.md) | Version ldflags, manual / GoReleaser |
| [SECURITY.md](SECURITY.md) | Threat model lite + secret rotation (M0.1, BotFather checklist) |

### Other
<details>
  <summary><b>Phone (Termux / Userland)</b></summary>
  
  1. Install **Termux** or **UserLAnd** (Ubuntu/Debian) on your phone.
  2. Run the following command:
    
  ```bash
  sudo apt update && sudo apt upgrade -y && sudo apt install golang git -y && \
  git clone https://github.com/gemeguardian/Goroku && \
  cd Goroku && \
  go build -o goroku_bin && \
  ./goroku_bin
  ```

3. Open the link displayed at the end of the startup output and complete authorization.
</details>


## Additional Features

<details>
  <summary><b>🔒 Automatic Database Backuper</b></summary>
  <img src="https://raw.githubusercontent.com/gemeguardian/Goroku/master/goroku/assets/202905566-964d2904-f3ce-4a14-8f05-0e7840e1b306.png" width="400">
</details>

<details>
  <summary><b>👋 Welcome Installation Screens</b></summary>
  <img src="https://raw.githubusercontent.com/gemeguardian/Goroku/master/goroku/assets/202905720-6319993b-697c-4b09-a194-209c110c79fd.png" width="300">
  <img src="https://raw.githubusercontent.com/gemeguardian/Goroku/master/goroku/assets/202905746-2a511129-0208-4581-bb27-7539bd7b53c9.png" width="300">
</details>

---

## ✨ Key Features & Improvements

| Feature | Description |
|---------|-------------|
| ⚡ **Written in Go** | Completely rewritten in Golang for speed, efficiency and safety |
| 🆕 **Latest Telegram Layer** | Support for forums and newest Telegram features |
| 🔒 **Enhanced Security** | Native entity caching and targeted security rules |
| 🎨 **UI/UX Improvements** | Modern interface and user experience |
| 📦 **Core Modules** | Improved and new core functionality |
| ⏱ **Rapid Bug Fixes** | Faster resolution than Hikka/Heroku/FTG/GeekTG |
| 🔌 **Go plugins + Yaegi** | Native Go plugins (`.dlmod` / `.loadmod`) and out-of-process Yaegi `.eval` — **not** a Python module runtime |
| ▶️ **Inline Elements** | Forms, galleries and lists support |

### Module compatibility (honest)

Goroku is a **Go** userbot. It does **not** load Python Hikka / FTG / GeekTG modules.

- **Native Go plugins**: install/load via owner commands; unsigned installs need `-confirm` or a trusted content SHA-256.
- **Yaegi `.eval`**: owner-only, out-of-process worker; timeout kills the worker; snapshots only (no shared bot memory).
- **Semantic familiarity**: command style and UX are inspired by Heroku/Hikka; that is **not** binary or Python import compatibility.

---

## 📋 Requirements

- **Go 1.24.4+** (see `go.mod`)
- **API Credentials** from [Telegram Apps](https://my.telegram.org/apps)

---

## 🧪 Development / CI tests

Critical race suites (M9.4):

```bash
export TMPDIR=/root/.cache/go-tmp   # large temp dir recommended for -race
bash scripts/test-critical.sh
# packages: ./goroku/ ./goroku/web/ ./goroku/inline/ ./goroku/modules/
```

Package parity (M9.1) and full suite:

```bash
bash scripts/check-package-parity.sh
go test -race ./...
```

Coverage policy and security-check residuals: [docs/CI.md](docs/CI.md). Soft project coverage floor in CI is **20%** (not a quality target).

---

## 💬 Support

[![Telegram Support](https://img.shields.io/badge/Telegram-Support_Group-2594cb?logo=telegram)](https://t.me/goroku_forum)

---

## ⚠️ Usage Disclaimer

> This project is provided as-is. The developer takes **NO responsibility** for:
> - Account bans or restrictions
> - Message deletions by Telegram
> - Security issues from scam modules
> - Session leaks from malicious modules
>
> **Security Recommendations:**
> - Enable `.api_fw_protection`
> - Avoid installing many modules at once
> - Review [Telegram's Terms](https://core.telegram.org/api/terms)

---

## 🙏 Acknowledgements

- [**Hikari**](https://gitlab.com/hikariatama) for Hikka (project foundation)
- [**Coddrago**](https://github.com/coddrago/Heroku) for the original idea and structure
- [**GoTD Team**](https://github.com/gotd/td) for the amazing Golang MTProto library
