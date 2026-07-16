# Goroku: огромный план доработки до 7-8/10

> **Статус: архивный план.** Значительная часть задач ниже уже выполнена, часть
> путей и метрик устарела. Актуальный аудит и порядок дальнейшей работы находятся
> в [`goroku-roadmap-2026.md`](./goroku-roadmap-2026.md).

> **Goal:** Убрать технический долг, повысить типизацию, тестовое покрытие реальными тестами, сделать intentional RCE-фичи контролируемыми и привести git-стейт в порядок.

---

## Phase 0: Привести git-стейт в порядок (1-2 дня)

### Task 0.1: Разобрать staged / unstaged изменения

**Objective:** Понять, что готово к коммиту, а что — экспериментальные правки.

**Files:**
- Проверить: `git status --short`
- Разобрать: `git diff`, `git diff --cached`

**Steps:**
1. Выполнить `git diff --cached --name-only` — это уже в индексе.
2. Для каждого файла в индексе: либо `git commit`, либо `git reset HEAD <file>`.
3. Выполнить `git diff --name-only` — это рабочие правки.
4. Всё незавершённое либо добить, либо `git stash` в отдельный stash с описанием.

**Verify:**
```bash
git status --short
# Ожидаем: либо пусто, либо только осознанные правки в рабочей директории
```

---

### Task 0.2: Сделать pre-commit hook с gofmt + go vet + go test

**Objective:** Не пускать неотформатированный код.

**Files:**
- Create: `.git-hooks/pre-commit`
- Modify: `.git/config` (или документация)

**Steps:**
1. Создать `.git-hooks/pre-commit`:

```bash
#!/bin/sh
set -e

unformatted=$(gofmt -l .)
if [ -n "$unformatted" ]; then
    echo "gofmt needed for:"
    echo "$unformatted"
    exit 1
fi

go vet ./...
go test ./...
```

2. `chmod +x .git-hooks/pre-commit`
3. `git config core.hooksPath .git-hooks`

**Verify:**
```bash
git config core.hooksPath
# Ожидаем: .git-hooks
```

---

## Phase 1: Типизация (1-2 недели)

### Task 1.1: Типизировать ключи кэша

**Objective:** Убрать `map[interface{}]...` из `CustomTelegramClient`.

**Files:**
- Modify: `goroku/types.go:153-156`
- Modify: `goroku/cache/*.go`
- Modify: `goroku/cache_full_*.go`, `goroku/cache_resolve.go`, `goroku/cache_perms.go`
- Test: `goroku/cache_helpers_test.go`, `goroku/types_test.go`

**Steps:**
1. Создать тип `CacheKey`:

```go
type CacheKey struct {
    Kind string // "entity", "perms", "full_channel", "full_user"
    ID   int64
}
```

2. Заменить в `CustomTelegramClient`:

```go
GorokuEntityCache      map[CacheKey]cache.CacheRecordEntity
GorokuPermsCache       map[CacheKey]map[CacheKey]cache.CacheRecordPerms
GorokuFullChannelCache map[CacheKey]cache.CacheRecordFullChannel
GorokuFullUserCache    map[CacheKey]cache.CacheRecordFullUser
```

3. Обновить все функции, которые пишут/читают эти кэши, заменить `interface{}` ключи на `CacheKey`.
4. Запустить `go test ./...` и `go vet ./...`.

---

### Task 1.3: Типизировать `WebConfig` и `Web`

**Objective:** Убрать `interface{}` из `web.WebConfig`, `web.Web`, `web.WebCore` и связанных callback-ов, заменив на типизированные интерфейсы.

**Files:**
- Create: `goroku/webiface/interface.go`
- Modify: `goroku/web/root.go`
- Modify: `goroku/web/core.go`
- Modify: `goroku/bootstrap.go`
- Modify: `goroku/web/web_test.go`
- Modify: `goroku/web/proxypass.go` (при необходимости)

**Steps:**
1. Создать пакет `webiface` с интерфейсами:

```go
package webiface

import (
    "context"
    "github.com/gotd/td/tg"
)

type TelegramClient interface {
    TGID() int64
    SendMessage(ctx context.Context, peer tg.InputPeerClass, msg string) (tg.UpdatesClass, error)
    GetMe() (*tg.User, error)
    // ... остальные методы, которые используются в web handlers
}

type Database interface {
    Get(owner, key string, defaultValue interface{}) (interface{}, error)
    Set(owner, key string, value interface{}) error
    Save() bool
}

type Modules interface {
    GetModules() []Module
    GetModule(name string) (Module, bool)
}

type Module interface {
    Name() string
}

type ConfigSaver interface {
    SaveConfig(key string, value interface{}) bool
}
```

2. Заменить `interface{}` поля в `WebConfig`:

```go
type WebConfig struct {
    ApiToken   string
    SetupToken string
    DataRoot   string
    Connection interface{} // оставить пока, если нет чёткого типа; пометить TODO
    Proxy      interface{} // оставить пока, если нет чёткого типа; пометить TODO
    SaveConfig func(key string, value interface{}) bool
    Restart    func()
    GetClient  func() webiface.TelegramClient
    OnLogin    func(client webiface.TelegramClient) error
}
```

3. Заменить поля в `Web`:

```go
type Web struct {
    mu             sync.Mutex
    signInClients  map[string]webiface.TelegramClient
    pendingClient  webiface.TelegramClient
    qrLogin        interface{} // TODO: заменить, когда типизируем QR-логин
    sessions       map[string]WebSession
    ratelimit      map[string][]int64
    apiToken       string
    setupToken     string
    dataRoot       string
    saveConfig     func(key string, value interface{}) bool
    restart        func()
    onLogin        func(client webiface.TelegramClient) error
    clientData     map[int64][]interface{} // TODO: заменить на структуру
    getClient      func() webiface.TelegramClient
    pendingAuths   map[string]*PendingAuth
    pendingAuthsMu sync.Mutex
}
```

4. Переписать `web/core.go` методы, чтобы не использовали `reflect.ValueOf(client).FieldByName("TGID")`, а вызывали `client.TGID()`.
5. Обновить `bootstrap.go` — передавать `GetClient`/`OnLogin` с конкретными типами (без `interface{}`).
6. Обновить тесты `web/web_test.go` — моки реализуют `webiface.TelegramClient`.
7. Запустить `go test ./goroku/web/...` и `go build ./...`.

**Verify:**
```bash
cd /root/eblan/Goroku
grep -R 'interface{}' goroku/web/*.go | wc -l
# Ожидаем: < 10 (только QR-логин, connection, proxy — отмеченные TODO)
go test ./goroku/web/...
```

---

### Task 1.4: Типизировать `InlineManager.client`, `db`, `allModules`

**Objective:** Убрать `interface{}` из полей `InlineManager` и конструктора `NewInlineManager`, используя интерфейсы из `inlineiface` и новых пакетов.

**Files:**
- Modify: `goroku/inlineiface/interface.go`
- Create: `goroku/inlineiface/db.go` (опционально)
- Modify: `goroku/inline/core.go`
- Modify: `goroku/inline/types.go` (при необходимости)
- Modify: `goroku/client_init.go`
- Modify: `goroku/inline/*_test.go` (тестовые моки)
- Modify: `goroku/modules/inline_stuff.go`, `goroku/modules/quickstart.go` (если обращаются через `interface{}`)

**Steps:**
1. Расширить `inlineiface`:

```go
type ClientAPI interface {
    TGID() int64
    GetMe() (*tg.User, error)
    SendMessage(ctx context.Context, peer tg.InputPeerClass, msg string) (tg.UpdatesClass, error)
    // методы, которые inline manager использует напрямую
}

type DatabaseAPI interface {
    Get(owner, key string, defaultValue interface{}) (interface{}, error)
    Set(owner, key string, value interface{}) error
}

type ModulesRegistry interface {
    GetModules() []inline.Module
}
```

2. Изменить `InlineManager`:

```go
type InlineManager struct {
    mu                   sync.RWMutex
    registerMu           sync.Mutex
    bot                  *tgbotapi.BotAPI
    client               inlineiface.ClientAPI
    db                   inlineiface.DatabaseAPI
    allModules           inlineiface.ModulesRegistry
    // ... остальные поля без изменений
}

func NewInlineManager(client inlineiface.ClientAPI, db inlineiface.DatabaseAPI, allModules inlineiface.ModulesRegistry) *InlineManager {
    // ...
}
```

3. Убрать все `im.client.(type)`, `reflect.ValueOf(im.db)` и подобные касты в `inline/*.go`.
4. В `client_init.go` передавать конкретные `*CustomTelegramClient`, `*Database`, `*Modules` (они реализуют интерфейсы автоматически).
5. Убедиться, что `inlineiface.InlineManager` по-прежнему реализуется `*inline.InlineManager`.
6. Запустить `go test ./goroku/inline/...` и `go build ./...`.

**Verify:**
```bash
cd /root/eblan/Goroku
grep -R 'interface{}' goroku/inline/*.go | wc -l
# Ожидаем: значительно меньше текущего
# go test ./goroku/inline/...
```

---

### Task 1.5: `Database.Get` / `Database.Set` возвращают `error`

**Objective:** Убрать магическое `bool` возвращаемое значение у `Set` и `Get`; сделать контракт явным через `error`.

**Files:**
- Modify: `goroku/database.go`
- Modify: `goroku/pointers.go`
- Modify: `goroku/pointers_test.go`
- Modify: `goroku/modules/*.go` (все вызовы `db.Set`)
- Modify: `goroku/dispatcher.go` (если есть)
- Modify: `goroku/inline/*.go` (если есть)
- Modify: `goroku/web/*.go` (если есть)
- Modify: `goroku/database_test.go`
- Modify: `goroku/inlineiface/*.go` (обновить интерфейсы DatabaseAPI)
- Modify: `goroku/webiface/*.go` (обновить интерфейсы Database)

**Steps:**
1. Изменить сигнатуры:

```go
func (db *Database) Get(owner, key string, defaultValue interface{}) (interface{}, error)
func (db *Database) Set(owner, key string, value interface{}) error
func (db *Database) Delete(owner, key string) error
func (db *Database) Reset(data map[string]map[string]interface{}) error
func (db *Database) Update(items map[string]map[string]interface{}) error
func (db *Database) DeleteOwner(owner string) error
func (db *Database) Save() error
```

2. Убрать `return false` при ошибках; вместо этого возвращать `fmt.Errorf(...)`.
3. Убрать `return true` при успехе; вместо этого `return nil`.
4. Во всех вызовах обрабатывать `error`:

```go
if err := db.Set("owner", "key", value); err != nil {
    L().Error(...)
}

val, err := db.Get("owner", "key", defaultVal)
if err != nil {
    // fallback или логирование
}
```

5. Обновить `Pointer` — игнорировать ошибку `Get` там пока допустимо, но лучше вернуть `(*PointerList, error)` и т.д.
6. Запустить `go test ./...` и `go vet ./...`.

**Verify:**
```bash
cd /root/eblan/Goroku
grep -R 'db.Set(.*) bool\|db.Get(.*) interface{}\b' goroku --include='*.go' | grep -v 'error' | wc -l
# Ожидаем: 0
go test ./goroku/...
```

**Verify:**
```bash
go build ./...
go test ./...
# Ожидаем: 0 ошибок
```

---

### Task 1.2: Типизировать `Message.Media`, `Message.FwdFrom`, `Message.Entities`

**Objective:** Убрать `interface{}` из полей сообщения.

**Files:**
- Modify: `goroku/types.go:21-43`
- Modify: все места, где читаются `msg.Media`, `msg.FwdFrom`
- Test: `goroku/types_test.go`, `goroku/messages_test.go`

**Steps:**
1. `Media` — заменить на `tg.MessageMediaClass` (или `*tg.MessageMedia`, если такой тип).
2. `FwdFrom` — заменить на `tg.MessageFwdHeader`.
3. `Entities` уже `[]tg.MessageEntityClass` — оставить.
4. Обновить парсинг сообщений в `client.go` и `dispatcher.go`.

**Verify:**
```bash
go test ./goroku/goroku/...
# Ожидаем: проходит
```

---

### Task 1.3: Типизировать `WebConfig` и `Web`

**Objective:** Убрать `interface{}` из web-панели.

**Files:**
- Modify: `goroku/web/core.go`, `goroku/web/root.go`
- Modify: `goroku/web/web_test.go`

**Steps:**
1. Заменить:

```go
type WebConfig struct {
    ApiToken   string
    SetupToken string
    DataRoot   string
    Connection string
    Proxy      string
    SaveConfig func(key string, value interface{}) bool
    Restart    func()
    GetClient  func() *goroku.CustomTelegramClient
    OnLogin    func(client *goroku.CustomTelegramClient) error
}
```

2. Обновить `Web` структуру соответствующими типами.
3. Обновить `goroku/web/core.go`, который вызывает `NewWeb`.

**Verify:**
```bash
go test ./goroku/goroku/web/...
```

---

### Task 1.4: Типизировать `InlineManager.client`, `db`, `allModules`

**Objective:** Убрать `interface{}` из inline-менеджера.

**Files:**
- Modify: `goroku/inline/core.go`
- Modify: все места, где делается type assertion на `im.client`

**Steps:**
1. Создать/использовать интерфейс `TelegramClient` и `Database` из `inlineiface`.
2. Заменить поля в `InlineManager`.

```go
type InlineManager struct {
    client     TelegramClient
    db         Database
    allModules *goroku.Modules
    ...
}
```

3. Убрать runtime type assertions.

**Verify:**
```bash
go test ./goroku/goroku/inline/...
```

---

### Task 1.5: Типизировать `Database.Get` и `Database.Set`

**Objective:** Вернуть `error` вместо `interface{}` и `bool`.

**Files:**
- Modify: `goroku/database.go`
- Modify: все вызовы `db.Get` / `db.Set` в проекте

**Steps:**
1. Изменить сигнатуру:

```go
func (db *Database) Get(section, key string, defaultValue interface{}) (interface{}, error)
func (db *Database) Set(section, key string, value interface{}) error
```

2. Заменить вызовы:

```go
// Было:
val, ok := db.Get("section", "key", default).(float64)

// Станет:
val, err := db.Get("section", "key", default)
if err != nil { ... }
f, ok := val.(float64)
```

3. Запустить `go test ./...`.

**Verify:**
```bash
go test ./...
```

---

## Phase 2: Тесты (2-3 недели)

### Task 2.1: Написать тесты на `Message` методы

**Objective:** Покрыть `Answer`, `Edit`, `Reply`, `Delete`, `GetReplyMessage`.

**Files:**
- Create/Modify: `goroku/messages_test.go`
- Create: `goroku/mocks_test.go` (мок клиента)

**Steps:**
1. Создать мок `CustomTelegramClient` для тестов.
2. Написать тесты, проверяющие:
   - `Message.Answer` вызывает `SendMessageWithOptions` с правильным chat ID
   - `Message.Edit` вызывает `EditMessage` с правильным ID
   - `Message.Delete` возвращает ошибку, если клиент не инициализирован
   - `GetReplyMessage` возвращает `ErrNoReply`, если `ReplyToMsgID == 0`

**Verify:**
```bash
go test ./goroku/goroku -run TestMessage -v
```

---

### Task 2.2: Написать тесты на `CommandDispatcher`

**Objective:** Покрыть фильтры, whitelist, blacklist, ratelimit.

**Files:**
- Modify: `goroku/dispatcher_test.go`

**Steps:**
1. Написать тесты на:
   - blacklist chat отсекает сообщение
   - whitelist chat пропускает только нужные
   - `OnlyOwner` работает
   - ratelimit срабатывает после N вызовов
   - layout translation

**Verify:**
```bash
go test ./goroku/goroku -run TestDispatcher -v
```

---

### Task 2.3: Написать тесты на `client.go` core flows

**Objective:** Покрыть `Connect`, `Disconnect`, `GetMe`, `SendMessage`, `ResolvePeer`.

**Files:**
- Create/Modify: `goroku/client_test.go`

**Steps:**
1. Создать моки для `telegram.Client` и `tg.Client`.
2. Тестировать через публичные методы, а не через приватные.
3. Проверить: `Connect` возвращает ошибку, если не задан API_ID/API_HASH.

**Verify:**
```bash
go test ./goroku/goroku -run TestClient -v
```

---

### Task 2.4: Написать тесты на `modules/eval.go` безопасность

**Objective:** Проверить, что eval работает с таймаутом и не падает на пустом коде.

**Files:**
- Modify: `goroku/modules/eval_test.go`

**Steps:**
1. Написать тесты на:
   - пустой код возвращает "No code to evaluate"
   - Python eval с простым выражением
   - timeout срабатывает на бесконечном цикле
2. Проверить, что `censor` убирает API_HASH и phone из вывода.

**Verify:**
```bash
go test ./goroku/goroku/modules -run TestEval -v
```

---

### Task 2.5: Написать тесты на `web` ручки

**Objective:** Покрыть `/login`, `/api/*`, `/setup` через `httptest`.

**Files:**
- Modify: `goroku/web/web_test.go`

**Steps:**
1. Создать `httptest.NewServer` с `NewWeb(...)`.
2. Написать тесты:
   - `/setup` с валидным setup token создаёт сессию
   - `/login` без сессии возвращает 401
   - `/api/restart` с сессией вызывает `Restart`
3. Проверить cookie flags.

**Verify:**
```bash
go test ./goroku/goroku/web -v
```

---

### Task 2.6: Написать тесты на `SecurityManager`

**Objective:** Покрыть проверки владельца, групп, сроков.

**Files:**
- Modify: `goroku/security_test.go`

**Steps:**
1. Написать тесты на:
   - владелец проходит всегда
   - пользователь не из whitelist отклоняется
   - срок действия `sudo` истекает

**Verify:**
```bash
go test ./goroku/goroku -run TestSecurity -v
```

---

## Phase 3: RCE-фичи под контроль (1 неделя)

### Task 3.1: Добавить глобальные лимиты на eval

**Objective:** Предотвратить зависания/DoS через `.eval`.

**Files:**
- Modify: `goroku/modules/eval.go`
- Modify: `goroku/types.go` (добавить `EvalTimeout`?)
- Test: `goroku/modules/eval_test.go`

**Steps:**
1. Добавить в `Eval` структуру:

```go
type EvalLimits struct {
    MaxOutputBytes int
    Timeout        time.Duration
    MaxMemoryMB    int
}
```

2. Для `runYaegiEval` использовать `context.WithTimeout`.
3. Для `evalpy` и остальных — использовать `rlimit` / `timeout` в `exec.Command`.

```go
cmd := exec.CommandContext(ctx, "python3", "-c", py)
```

4. Ограничить stdout/stderr через `io.LimitReader`.

**Verify:**
```bash
go test ./goroku/goroku/modules -run TestEval -v
```

---

### Task 3.2: Добавить аудит-лог eval

**Objective:** Видеть, кто и что выполнял.

**Files:**
- Modify: `goroku/modules/eval.go`
- Create: `goroku/audit.go` или использовать существующий логгер

**Steps:**
1. Логировать: chat_id, user_id, first 80 chars кода, язык, timestamp.
2. Не логировать полный stdout/stderr (приватность).

**Verify:**
```bash
# В тесте проверить, что логгер вызывается
go test ./goroku/goroku/modules -run TestEvalAudit -v
```

---

### Task 3.3: Подпись hot plugin .so

**Objective:** Гарантировать, что .so соответствует исходнику.

**Files:**
- Modify: `goroku/modules/hot_plugin_linux.go`

**Steps:**
1. При сборке `.so` записывать рядом файл `<plugin>.sha256` от исходников.
2. При `plugin.Open` проверять хэш.
3. Если не совпадает — ошибка.

**Verify:**
```bash
go test ./goroku/goroku/modules -run TestHotPlugin -v
```

---

## Phase 4: Concurrency и reliability (1 неделя)

### Task 4.1: Починить rate limiter (sleep под mutex)

**Objective:** Убрать долгий sleep внутри `RatelimitMu`.

**Files:**
- Modify: `goroku/client.go:60-160` (forbiddenInvoker)

**Steps:**
1. Вынести `SuspendUntil` проверку в отдельную функцию, которая возвращает `dur` и не держит мьютекс во время sleep.

```go
func (c *CustomTelegramClient) waitIfSuspended() {
    c.RatelimitMu.Lock()
    until := c.SuspendUntil
    c.RatelimitMu.Unlock()
    if time.Now().Before(until) {
        time.Sleep(time.Until(until))
    }
}
```

2. Использовать `defer` на unlock везде.

**Verify:**
```bash
go test -race ./goroku/goroku -run TestRateLimiter -v
```

---

### Task 4.2: Убрать `time.Sleep` в `AnimateMessage`

**Objective:** Сделать cancellable.

**Files:**
- Modify: `goroku/types.go:243-250`

**Steps:**
1. Добавить `context.Context` в `AnimateMessage`:

```go
func AnimateMessage(ctx context.Context, msg *Message, frames []string, interval time.Duration) error
```

2. Использовать `select { case <-ctx.Done(): case <-time.After(interval) }`.

**Verify:**
```bash
go test ./goroku/goroku -run TestAnimate -v
```

---

### Task 4.3: Graceful shutdown горутин inline-бота

**Objective:** `startPolling` и `ttlCleaner` должны завершаться по `stopCh`.

**Files:**
- Modify: `goroku/inline/core.go`

**Steps:**
1. Проверить, что все `select` в polling проверяют `im.stopCh`.
2. Добавить `sync.WaitGroup` для ожидания завершения.
3. В `RegisterManager` не создавать новый `stopCh`, если старый активен.

**Verify:**
```bash
go test -race ./goroku/goroku/inline -run TestLifecycle -v
```

---

## Phase 5: Web security (3-5 дней)

### Task 5.1: Single-use setup token

**Objective:** Setup token не должен работать после первого успешного логина.

**Files:**
- Modify: `goroku/web/root.go`

**Steps:**
1. После успешного `/setup` очистить `w.setupToken`.
2. Проверить в `checkSetupToken`, что `setupToken != ""`.

**Verify:**
```bash
go test ./goroku/goroku/web -run TestSetupToken -v
```

---

### Task 5.2: Rate limiting на web-ручки

**Objective:** Защита от брутфорса setup token и сессий.

**Files:**
- Modify: `goroku/web/root.go`

**Steps:**
1. Добавить `map[string][]time.Time` для попыток по IP.
2. Для `/setup` и `/login` — max 10 попыток в минуту.

**Verify:**
```bash
go test ./goroku/goroku/web -run TestWebRateLimit -v
```

---

### Task 5.3: CSRF для state-changing ручек

**Objective:** Защита от CSRF, если web-панель будет exposed наружу.

**Files:**
- Modify: `goroku/web/root.go`
- Modify: web-ресурсы (JS)

**Steps:**
1. Генерировать `csrf_token` при создании сессии.
2. Для POST/PUT/DELETE проверять `X-CSRF-Token` header или `_csrf` form field.
3. Возвращать `csrf_token` в `/api/me`.

**Verify:**
```bash
go test ./goroku/goroku/web -run TestCSRF -v
```

---

## Phase 6: Инструменты и CI (2-3 дня)

### Task 6.1: Установить golangci-lint и настроить `.golangci.yml`

**Objective:** Автоматическая проверка качества.

**Files:**
- Modify: `.golangci.yml`
- Modify: `.github/workflows/*.yml`

**Steps:**
1. Установить `golangci-lint`.
2. Добавить линтеры: `gofmt`, `govet`, `staticcheck`, `errcheck`, `gosec`, `unconvert`, `ineffassign`, `misspell`.
3. В CI запускать `golangci-lint run ./...`.

**Verify:**
```bash
golangci-lint run ./...
# Ожидаем: 0 issues
```

---

### Task 6.2: Добавить coverage gate в CI

**Objective:** Не падать ниже текущего покрытия.

**Files:**
- Modify: `.github/workflows/*.yml`

**Steps:**
1. В CI после `go test -coverprofile=coverage.out` запускать `go tool cover -func=coverage.out`.
2. Проверять, что total >= 40% (начальная цель).

**Verify:**
```bash
# В CI выводит total coverage
```

---

### Task 6.3: Добавить `go test -race` в CI

**Objective:** Ловить race conditions.

**Files:**
- Modify: `.github/workflows/*.yml`

**Steps:**
1. Добавить шаг `go test -race ./...`.

**Verify:**
```bash
go test -race ./...
```

---

## Phase 7: Документация (2-3 дня)

### Task 7.1: Написать `ARCHITECTURE.md`

**Objective:** Объяснить структуру проекта.

**Files:**
- Create: `ARCHITECTURE.md`

**Sections:**
- Package layout
- Module lifecycle (Init → ClientReady → Commands → OnUnload)
- Message flow
- Security model
- Plugin model
- Web panel

---

### Task 7.2: Написать `SECURITY.md`

**Objective:** Документировать intentional риски.

**Files:**
- Create: `SECURITY.md`

**Sections:**
- `.eval` / `.evalpy` — RCE for owner only
- Hot plugin — native code execution
- Web panel — trusted network assumption
- SSH tunnels — accept-new behavior
- Rate limiting

---

## Phase 8: Финальная проверка (1 день)

### Task 8.1: Полный прогон

**Objective:** Убедиться, что всё работает.

```bash
gofmt -l .
go vet ./...
go test -race ./...
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
go build -o goroku_bin .
```

### Task 8.2: Развёртывание

**Objective:** Перезапустить сервис с новым бинарником.

```bash
cd /root/eblan/Goroku
go build -o goroku_bin .
systemctl restart goroku.service
systemctl is-active goroku.service
journalctl -u goroku.service -n 50 --no-pager
```

---

## Приоритеты (в порядке)

1. **Phase 0** — git-стейт, pre-commit hook
2. **Phase 1** — типизация cache keys + InlineManager
3. **Phase 2** — тесты на Message, Dispatcher, Security, Web, Eval
4. **Phase 3** — eval limits, audit log, plugin signature
5. **Phase 4** — rate limiter concurrency + animate shutdown
6. **Phase 5** — web security
7. **Phase 6** — golangci-lint + CI coverage
8. **Phase 7** — документация
9. **Phase 8** — финальный прогон + деплой

---

## Целевые метрики после плана

| Метрика | Сейчас | Цель |
|---|---|---|
| `gofmt -l .` | ✅ 0 | ✅ 0 |
| `go test ./...` | ✅ pass | ✅ pass |
| `go test -race ./...` | ✅ pass | ✅ pass |
| `go vet ./...` | ✅ clean | ✅ clean |
| Покрытие тестами | ~12.6% | >= 40% |
| `interface{}` использования | 571 | <= 250 |
| Непроверенные ошибки | 359 | <= 150 |
| golangci-lint | not installed | 0 issues |
| Финальная оценка | 5/10 | 7-8/10 |
