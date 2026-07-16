<h1>ЭТО ПОЛНЫЙ ВАЙБКОД!</h1>
<div align="center">
  <img src="https://raw.githubusercontent.com/gemeguardian/Goroku/master/goroku/assets/1326-command-window-line-flat.webp" height="80">
  <h1>Юзербот Goroku</h1>
  <p>Продвинутый Telegram юзербот на Golang, основанный на Heroku python-userbot</p>
  
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

### Ручная установка (VPS/VDS сервер)

---

## ⚠️ Уведомление о безопасности

> **Важное уведомление о безопасности**  
> Хотя Goroku реализует расширенные меры безопасности, установка модулей от ненадежных разработчиков всё же может нанести вред вашему серверу/аккаунту.
> 
> **Рекомендации:**
> - ✅ Скачивайте модули исключительно из официальных репозиториев или от доверенных разработчиков
> - ❌ НЕ устанавливайте модули, если не уверены в их безопасности
> - ⚠️ Соблюдайте осторожность с неизвестными командами (`.terminal`, `.eval`, `.ecpp` и т. д.)
> - Go-команда `.eval` доступна только владельцу и всегда включена. Она выполняется **в отдельном worker-процессе через Yaegi**; timeout/cancel **убивает process group worker-а**. Общей памяти с ботом нет (`msg`/`client`/`db` — снимки; `Loader` недоступен). Одновременно допускается только один worker eval.
> - Нативные Go-плагины (`.dlmod` / `.loadmod` / presets) требуют владельца. Неподписанная/недоверенная установка — только с явным `-confirm` (или доверенным SHA-256 содержимого). Плагины **нельзя полностью выгрузить из памяти процесса** после load — unregister снимает только handlers.
> - Удалённые загрузки модулей — только HTTPS; private/loopback/link-local/CGNAT адреса блокируются.

---

## 🚀 Установка

### VPS/VDS
> **Примечание для пользователей VPS/VDS:**  
> Добавьте `--ssh-tunnel`, чтобы открыть веб-панель через SSH reverse tunnel (это **не** MTProto proxy)  
> Добавьте `--no-web` для настройки только через консоль  
> Добавьте `--root` для пользователей root (чтобы избежать ввода force_insecure)
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

### Размещение файлов в production

Оставляйте исполняемый файл и checkout только для чтения, а записываемый runtime
root передавайте через `--data-root /var/lib/goroku`. Загруженные исходники
модулей сохраняются в `/var/lib/goroku/modules`; Goroku не записывает их в пакет
`goroku/modules` внутри checkout.

```text
/opt/goroku/bin/goroku
/var/lib/goroku/config.json
/var/lib/goroku/config-<telegram-id>.json
/var/lib/goroku/goroku-<telegram-id>.session
/var/lib/goroku/modules/
```

Сейчас CLI хранит конфигурацию, JSON-базы отдельных аккаунтов и session-файлы
непосредственно в выбранном data root. Отдельные каталоги `sessions/` и
`database/` пока не создаются.

### CLI-флаги (продуктовый контракт)

| Флаг | Поведение |
|------|----------|
| `--port` | Порт веб-панели (по умолчанию `8080`) |
| `--no-web` | Только консоль: без веб-дашборда |
| `--no-git` | Отключить git-операции (`GOROKU_NO_GIT=1`) |
| `--data-root` | Записываемый runtime root для config/sessions/modules |
| `--ssh-tunnel` | Публикация веб-панели через SSH reverse tunnel (по умолчанию выкл.) |
| `--qr-login` | С `--no-web`: CLI QR-login без запроса y/N |
| `--no-auth` | С `--no-web`: не запускать интерактивный CLI login без сессий |
| `--sandbox` | Не перезапускать процесс после lifecycle restart |
| `--root` | Разрешить запуск от root (проверяется в `main`) |
| `--proxy-host` / `--proxy-port` / `--proxy-secret` | MTProto proxy (нужны все три; secret — hex) |
| `--proxy-pass` | **Устаревший alias** для `--proxy-secret` — **не** SSH tunnel |

Веб по умолчанию слушает `127.0.0.1` (`GOROKU_IP` / Docker могут переопределить).

### Ops / health

При включённой веб-панели:

- `GET /health` — JSON: `status`, `clients`, `setup_completed` (без секретов)
- `GET /healthz` — liveness (`ok`)
- `GET /readyz` — readiness (`ok`; onboarding без Telegram-сессии тоже ready)

Полный operations dashboard (trust UI модулей, sanitized logs, update UI) **пока не** поставляется.

### Документация

| Документ | Содержание |
|----------|------------|
| [docs/QUICKSTART.md](docs/QUICKSTART.md) | Сборка, первый запуск, production layout |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | Карта кода |
| [docs/OPERATIONS.md](docs/OPERATIONS.md) | Запуск, health, backup, restart, Docker |
| [docs/CONFIGURATION.md](docs/CONFIGURATION.md) | Флаги, env, data-root |
| [docs/RELEASE.md](docs/RELEASE.md) | ldflags, manual / GoReleaser |
| [SECURITY.md](SECURITY.md) | Threat model + ротация секретов (M0.1, BotFather checklist) |

### Другое
<details>
  <summary><b>На телефоне (Termux / Userland)</b></summary>
  
  1. Установите **Termux** или **UserLAnd** (Ubuntu/Debian) на свой телефон.
  2. Выполните следующую команду:
    
  ```bash
  sudo apt update && sudo apt upgrade -y && sudo apt install golang git -y && \
  git clone https://github.com/gemeguardian/Goroku && \
  cd Goroku && \
  go build -o goroku_bin && \
  ./goroku_bin
  ```

3. Откройте ссылку, отображаемую в конце вывода при запуске, и завершите авторизацию.
</details>


## Дополнительные возможности

<details>
  <summary><b>🔒 Автоматическое резервное копирование базы данных</b></summary>
  <img src="https://raw.githubusercontent.com/gemeguardian/Goroku/master/goroku/assets/202905566-964d2904-f3ce-4a14-8f05-0e7840e1b306.png" width="400">
</details>

<details>
  <summary><b>👋 Приветственные экраны установки</b></summary>
  <img src="https://raw.githubusercontent.com/gemeguardian/Goroku/master/goroku/assets/202905720-6319993b-697c-4b09-a194-209c110c79fd.png" width="300">
  <img src="https://raw.githubusercontent.com/gemeguardian/Goroku/master/goroku/assets/202905746-2a511129-0208-4581-bb27-7539bd7b53c9.png" width="300">
</details>

---

## ✨ Ключевые особенности и улучшения

| Особенность | Описание |
|---------|-------------|
| ⚡ **Написан на Go** | Полностью переписан на Golang для скорости, эффективности и безопасности |
| 🆕 **Последний слой Telegram** | Поддержка форумов и новейших функций Telegram |
| 🔒 **Повышенная безопасность** | Нативное кэширование сущностей и целевые правила безопасности |
| 🎨 **Улучшения UI/UX** | Современный интерфейс и удобство использования |
| 📦 **Ядровые модули** | Улучшенный и новый функционал ядра |
| ⏱ **Быстрое исправление багов** | Более быстрое решение проблем, чем в Hikka/Heroku/FTG/GeekTG |
| 🔌 **Go-плагины + Yaegi** | Нативные Go-плагины (`.dlmod` / `.loadmod`) и out-of-process Yaegi `.eval` — **не** Python runtime |
| ▶️ **Инлайн-элементы** | Поддержка форм, галерей и списков |

### Совместимость модулей (честно)

Goroku — **Go** userbot. Он **не** загружает Python-модули Hikka / FTG / GeekTG.

- **Нативные Go-плагины**: установка/загрузка через owner-команды; неподписанные — с `-confirm` или доверенным SHA-256.
- **Yaegi `.eval`**: только owner, out-of-process worker; timeout убивает worker; только snapshots (без shared memory с ботом).
- **Семантическое сходство**: стиль команд вдохновлён Heroku/Hikka; это **не** бинарная и не Python import-совместимость.

---

## 📋 Требования

- **Go 1.24.4+** (см. `go.mod`)
- **API Credentials** с сайта [Telegram Apps](https://my.telegram.org/apps)

---

## 💬 Поддержка

[![Telegram Support](https://img.shields.io/badge/Telegram-Группа_поддержки-2594cb?logo=telegram)](https://t.me/goroku_forum)

---

## ⚠️ Дисклеймер об использовании

> Этот проект предоставляется «как есть». Разработчик **НЕ несёт ответственности** за:
> - Бан или ограничения аккаунта
> - Удаление сообщений со стороны Telegram
> - Проблемы с безопасностью из-за мошеннических модулей
> - Утечки сессий из-за вредоносных модулей
>
> **Рекомендации по безопасности:**
> - Включите `.api_fw_protection`
> - Избегайте одновременной установки большого количества модулей
> - Ознакомьтесь с [Условиями Telegram](https://core.telegram.org/api/terms)

---

## 🙏 Благодарности

- [**Hikari**](https://gitlab.com/hikariatama) за Hikka (основа проекта)
- [**Coddrago**](https://github.com/coddrago/Heroku) за оригинальную идею и структуру
- [**GoTD Team**](https://github.com/gotd/td) за потрясающую библиотеку MTProto на Golang
