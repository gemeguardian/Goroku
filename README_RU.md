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

---

## 🚀 Установка

### VPS/VDS
> **Примечание для пользователей VPS/VDS:**  
> Добавьте `--proxy-pass` для включения SSH-туннелирования  
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
| 🔄 **Обратная совместимость** | Работает с модулями FTG, GeekTG и Hikka |
| ▶️ **Инлайн-элементы** | Поддержка форм, галерей и списков |

---

## 📋 Требования

- **Go 1.21+**
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
