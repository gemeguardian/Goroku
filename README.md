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

---

## 🚀 Installation

### VPS/VDS
> **Note for VPS/VDS Users:**  
> Add `--proxy-pass` to enable SSH tunneling  
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
| 🔄 **Backward Compatibility** | Works with FTG, GeekTG and Hikka modules |
| ▶️ **Inline Elements** | Forms, galleries and lists support |

---

## 📋 Requirements

- **Go 1.21+**
- **API Credentials** from [Telegram Apps](https://my.telegram.org/apps)

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