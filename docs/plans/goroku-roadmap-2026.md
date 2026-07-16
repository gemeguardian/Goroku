# Goroku: актуальный технический аудит и roadmap

Дата аудита: 15 июля 2026 года.

## Текущий прогресс на 16 июля 2026 года

Этот раздел отделяет **исходный baseline аудита**, зафиксированный ниже, от
**текущего локального worktree**. Исторические выводы и критерии milestone-ов
ниже не переписаны: они описывают состояние на момент аудита и полный объём
работ. Tracking-коммит **`a07c96a`** (master, ahead of origin by 1): product/tests/CI/docs
зафиксированы. Clean worktree verification **PASS** (parity, tidy, gofmt,
vet, build, `go test -race ./...`). Local runtime (`user_modules/`,
`.goroku_plugins/`) остаётся untracked/ignored. Push на origin — только по
запросу.

### Легенда статусов

- **Завершено**: выполнены все критерии задачи, изменения и tests tracked и
  проверены на clean checkout.
- **Частично**: реализация или regression tests начаты в текущем worktree, но
  критерии задачи ещё не выполнены полностью.
- **Не начато**: целевая реализация задачи отсутствует; incidental изменения в
  соседних файлах статус не повышают.
- **Ручной блокер**: действие нельзя завершить или подтвердить только изменением
  репозитория.

### Строгая матрица

| Задача | Статус | Краткое свидетельство/результат |
|---|---|---|
| M0.1 | **Ручной блокер** | Ротация действующего token остаётся внешним ручным действием. |
| M0.2 | **Завершено** | Samples вне package path (`/user_modules/` ignored); runtime `dataRoot/modules`; parity clean on tracked tree (`a07c96a`). |
| M1.1 | **Частично** | journal phases + recovery (`aed650e`); still not joint FS+DB atomic. |
| M1.2 | **Завершено** | limits/validation/redaction; M1.1 residual separate. |
| M1.3 | **Завершено** | Account-scoped AssetChannel + race tests in `a07c96a` + clean race suite. |
| M1.4 | **Завершено** | Typed web runtime registry/lifecycle tracked + verified. |
| M1.5 | **Завершено** | Owner-aware registrations/leases tracked + verified. |
| M2.1 | **Завершено** | Local DB SoT + Redis generation mirror tracked + verified. |
| M2.2 | **Завершено** | Generation-safe Redis flush/Close tracked + verified. |
| M2.3 | **Завершено** | last-valid + corrupt recovery + atomic config tracked + clean gate. |
| M2.4 | **Завершено** | write/error contract tracked; intentional `Save`/`SaveContext` API kept. |
| M2.5 | **Завершено** | defensive copies tracked + verified. |
| M3.1-M3.3 | **Завершено** | IP trust, pending auth, localhost/timeouts, SSH-off-by-default tracked. |
| M3.4 | **Завершено** | CSRF/setup/session rotation tracked + clean gate. |
| M4.1 | **Завершено** | ProcessExecutor tracked; cgroups/rlimit product-deferred (out of scope). |
| M4.2 | **Завершено** | Out-of-process Yaegi worker via ProcessExecutor; timeout kills process group. |
| M4.3 | **Завершено** | OnlyOwner + audit digests tracked + verified. |
| M4.4 | **Завершено** | SSRF + content digests + confirm strip tracked; full signer deferred. |
| M5.1 | **Завершено** | App.Run lifecycle tracked; embedded stdin residual documented/accepted. |
| M5.2 | **Завершено** | Inline generation lifecycle tracked + verified. |
| M5.3 | **Завершено** | Bounded rate limiter tracked + verified. |
| M5.4 | **Завершено** | Bounded command/watcher executors + Message ctx tracked + verified. |
| **M9.1** | **Завершено** | `a07c96a` + clean worktree: parity/tidy/gofmt/vet/build/`go test -race ./...` PASS. CI workflow criteria 1–7. golangci-lint in CI (local binary optional). |
| **M6.1** | **Завершено** | `4699404` + clean race: factories, lifecycle, sequential SendReady. |
| **M6.2** | **Завершено** | DocumentStore + AssetRepository; no DB→full client. |
| **M6.3** | **Завершено** | Dispatch pipeline + reason codes + regex-at-register. |
| **M6.4** | **Завершено** | Web service split; no `web.Instance`. |
| **M6.5** | **Завершено** | Client file split; EntityCache ownership residual (non-blocking). |
| **M7** | **Частично** | Loader typed + Settings schema (`aed650e`); more modules still ConfigDefaults. |
| **M8** | **Завершено** | CLI proxy/qr/no-auth, `/health`, honest README (ops UI residual OK). |
| **M9.2** | **Частично** | soft+optional strict govulncheck, SBOM script; hard gate residual. |
| **M9.3** | **Завершено** | Soft coverage floor + policy docs. |
| **M9.4** | **Завершено** | `test-critical.sh` + critical suites green on clean tree. |
| **M10** | **Частично** | HEALTHCHECK/CHANGELOG; signed/canary residual. |

### Следующий порядок исполнения

1. ~~M6–M10 backlog~~ `4699404`; ~~residuals M4.2/M1.1 polish/M7-M10~~ `aed650e`.
2. Push only on user ask; optional tag after CHANGELOG review.
3. True leftovers: M0.1 token, M1.1 joint FS+DB atomic, M7 full typing, M9.2 hard gate, M10 signed/canary.
4. **gotd upgrade** separate PR.

### Worktree snapshot (для handoff)

- Branch: `master` **ahead 5** (…`aed650e`); product clean after residual commit.
- Local only (ignored): `user_modules/`, `.goroku_plugins/`, secrets/runtime.
- `gotd/td` **not** upgraded.
- Prefer `TMPDIR=/root/.cache/go-tmp`.

### Constraints for next orchestrator

- Push/tag only when user asks.
- Do not rollback; do not move `user_modules/` into package path.
- Optional polish residuals; release-ready baseline is in `4699404`.

## 1. Цель документа

Этот план заменяет прежний cleanup-план как источник актуальных задач. Он
описывает не абстрактное «улучшение кода», а порядок доведения Goroku до
состояния, в котором проект:

- не теряет пользовательские данные;
- не допускает рассинхронизацию handler-ов и security policy;
- штатно завершает фоновые процессы;
- безопасно ограничивает intentional RCE-функции;
- имеет честный публичный контракт и воспроизводимый релиз;
- проверяется на чистом checkout теми же тестами, что и локальная сборка.

Документ рассчитан на последовательную работу одного разработчика. Оценки в
днях приблизительные и включают реализацию, тесты и ревью, но не включают
ожидание внешней инфраструктуры Telegram.

## 2. Краткий вывод аудита

Текущая зрелость проекта: примерно **4/10 для публичного production-релиза**.

В проекте уже есть значительный рабочий объём:

- MTProto-клиент на `gotd/td`;
- система команд, watchers, security masks и module metadata;
- inline bot, формы, списки и галереи;
- web onboarding с phone/QR/2FA;
- JSON/Redis persistence;
- backup/restore и динамические Go plugins;
- базовый CI с formatting, vet, lint и race detector;
- 35 665 строк tracked Go-кода и 212 tracked-файлов.

Но ближайшая работа должна быть направлена не на новые функции. До расширения
продукта нужно устранить блокеры целостности, security routing и lifecycle.

### Главные блокеры

1. В `goroku/modules/goroku_backup.go:92-99` находятся две бесконечно
   рекурсивные функции. `.backupmods` и restore-сценарии способны завершить
   процесс stack overflow.
2. Redis и локальный JSON не образуют корректную durable persistence model.
   Успешная запись Redis может оставить локальный файл устаревшим, а flush loop
   способен потерять dirty update.
3. Registry команд не хранит владельца handler-а. При совпадении имён handler,
   metadata и security mask могут относиться к разным модулям.
4. `Web.clientData` читается и пишется под разными mutex; глобальный
   `channelsCache` вообще не синхронизирован.
5. Web auth доверяет spoofable proxy headers, использует IP в HTML без escaping
   и не ограничивает общий объём pending auth.
6. Timeout Yaegi прекращает ожидание, но не выполнение кода. Eval и terminal
   используют неограниченные output buffers.
7. Shutdown останавливает только Telegram clients и вызывает `os.Exit`, не
   закрывая web, inline, modules, security и Redis.
8. README обещает Python module compatibility, которую loader явно отвергает.
   Документированный `--proxy-pass` также не соответствует реализации.
9. Правило `.gitignore` скрывает все `goroku/modules/*_test.go`, поэтому CI не
   запускает локально существующие тесты модулей.
10. Локальный source/data root содержит действующие секреты в ignored config.
    Их нельзя публиковать, а обнаруженные bot tokens нужно перевыпустить.

## 3. Проверенный baseline

### Репозиторий

- Ветка: `master`, tracking `origin/master`.
- До аудита были untracked runtime artifacts: `goroku.db` и
  `goroku_bin.backup-before-refactor`.
- `goroku.db` не покрыт `.gitignore` и имеет права `0644`.
- Backup binary не покрыт `.gitignore` и занимает около 96 MB.
- Локальные `config.json` и `config-*.json` ignored и имеют `0600`, но содержат
  реальные credentials.

### Проверки

- `gofmt` для tracked Go files: clean.
- `go mod verify`: pass.
- `go test -vet=off ./...`: pass.
- `go test -vet=off -race ./...`: pass на покрытых сценариях.
- `go test ./...`: fail из-за ignored local module
  `goroku/modules/SimpleAI.go:736` (`fmt.Errorf` с dynamic format).
- `go vet ./...`: fail по той же причине.
- `go test -vet=off -shuffle=on -count=5 ./...`: нестабилен из-за глобального
  `channelsCache` в `TestAssetChannel`.
- Общее statement coverage: около 16%.
- Coverage `goroku`: около 25%; `cache`: 78%; `inline`: 39%; `web`: 17%;
  tracked core modules фактически почти не покрыты.

Pass race detector не означает отсутствие гонок: тесты исполняют лишь малую
часть production-путей и не создают конкурентную нагрузку на найденные maps.

### Документация и toolchain

- `go.mod` требует Go `1.24.4`.
- README заявляет Go `1.21+`.
- CI использует Go `1.24`, но `setup-go` запускается до checkout.
- `golangci-lint` имеет mutable версию `latest`.
- Версия приложения жёстко остаётся `1.0.0`, содержательный release process
  отсутствует.

## 4. Карта архитектуры

```text
main.go
  -> goroku.Main
      -> config/logging/web bootstrap
      -> session discovery
      -> CustomTelegramClient per account
          -> Database
          -> Modules registry
          -> CommandDispatcher
          -> SecurityManager
          -> InlineManager
      -> signal handler

Telegram update
  -> message conversion
  -> CommandDispatcher
      -> command lookup
      -> chat/module policy
      -> security mask
      -> metadata filters
      -> rate limiter
      -> asynchronous handler

Modules
  -> static prototypes from main.go
  -> reflection clone per client
  -> config callback
  -> Init
  -> registry commit
  -> ClientReady
  -> optional native hot plugin

Persistence
  -> JSON document
  -> optional Redis mirror
  -> in-memory revisions
  -> Telegram asset storage
```

Основная архитектурная проблема: application core, infrastructure и runtime
container находятся в одном пакете и имеют взаимные ссылки. Не нужно начинать с
массового переписывания. Сначала исправляются инварианты, затем выделяются
компоненты по уже стабилизированным контрактам.

## 5. Правила выполнения roadmap

Каждая задача считается завершённой только при одновременном выполнении четырёх
условий:

1. Поведение исправлено, а не только скрыта ошибка.
2. Добавлен regression test, который падал до исправления.
3. `gofmt`, `go vet` и целевые тесты проходят на clean checkout.
4. Изменённый публичный контракт отражён в документации.

Дополнительные правила:

- один PR решает одну связанную проблему;
- сначала correctness, затем рефакторинг;
- не смешивать массовое переименование с изменением поведения;
- dependency upgrades выполнять отдельно от P0 fixes;
- новые `any`, globals и background goroutines требуют явного обоснования;
- любой новый background worker обязан иметь `Start`, `Stop/Close` и тест;
- любой persistence write обязан возвращать диагностируемую ошибку;
- dangerous capability не должна становиться доступнее при ошибке конфигурации.

## 6. Milestone M0: немедленная гигиена и секреты

Оценка: 0.5-1 день. Приоритет: P0. Зависимости: нет.

### M0.1. Отозвать локальные секреты

Проблема:

- ignored `config-1184610266.json` содержит Telegram Bot API token и другие
  секреты;
- `config.json` содержит Telegram API credentials;
- значения не находятся в tracked history, но могли попасть в backups/logs.

Работы:

1. Перевыпустить Bot API token через BotFather.
2. Проверить backup-хранилища и историю shell/log aggregation.
3. Решить, нужно ли менять Telegram API hash; как минимум ограничить доступ к
   машине и резервным копиям.
4. Не переносить реальные значения в issues, commits или CI variables без
   secret storage.
5. Добавить в `SECURITY.md` процедуру ротации.

Критерии готовности:

- старый bot token не работает;
- новый token хранится только в runtime data root с `0600`;
- tracked tree и git history не содержат token signatures.

### M0.2. Разделить source tree и runtime artifacts

Файлы: `.gitignore`, installation docs.

Работы:

1. Игнорировать `*.db`, `goroku_bin.*`, `*.backup-*` и runtime logs.
2. Удалить широкое правило `goroku/modules/*`.
3. Хранить downloaded modules только в `dataRoot/modules` или
   `.goroku_plugins/source`, а не в tracked source package.
4. Явно включить module tests в Git.
5. Описать рекомендуемый layout:

```text
/opt/goroku/bin/goroku
/etc/goroku/config.json
/var/lib/goroku/sessions/
/var/lib/goroku/modules/
/var/lib/goroku/database/
/var/log/goroku/
```

Критерии готовности:

- штатный запуск не меняет source checkout;
- `git status` после запуска остаётся clean;
- clean clone и локальная машина компилируют одинаковый набор package files;
- module tests видимы через `git ls-files`.

## 7. Milestone M1: release blockers

Оценка: 4-7 дней. Приоритет: P0. Зависимости: M0.2 для корректного CI baseline.

### M1.1. Исправить backup/restore recursion

Файл: `goroku/modules/goroku_backup.go:92-99`.

Проблема:

```go
func scheduleBackupRestart() {
    scheduleBackupRestart()
}

func (m *GorokuBackup) loadedModulesMap() map[string]string {
    return m.loadedModulesMap()
}
```

Использование находится в `.backupmods` и нескольких restore paths.

Работы:

1. Определить правильный restart dependency вместо глобальной рекурсивной
   заглушки.
2. Получать loaded modules из DB/loader через узкий интерфейс.
3. Разделить `prepare restore`, `apply restore` и `request restart`.
4. Не менять рабочую DB до полной валидации архива.
5. Добавить staging directory и atomic apply.
6. Гарантировать ровно один restart request.

Тесты:

- `backupdb` round trip;
- `backupmods` формирует ожидаемый manifest;
- `backupall` включает разрешённые данные;
- `restoredb`, `restoremods`, `restoreall` не паникуют;
- corrupted archive не меняет state;
- restart callback вызван один раз;
- тест выполняется с временным data root.

Критерии готовности:

- рекурсивные заглушки удалены;
- все backup tests tracked и проходят в CI;
- ручной smoke restore завершает процесс контролируемо.

### M1.2. Ограничить archive restore

Файл: `goroku/modules/goroku_backup.go:504-650`.

Работы:

1. Ограничить размер Telegram media до чтения в память.
2. Ограничить compressed bytes, decompressed bytes, количество entries и
   глубину nested archives.
3. Отклонять symlink, device и нестандартные file modes.
4. Проверять normalized destination path.
5. Валидировать JSON schemas и module manifest до apply.
6. Ввести `RestoreLimits` с безопасными defaults.

Критерии готовности:

- zip bomb завершается controlled error без заметного роста памяти;
- path traversal не создаёт файл вне staging;
- invalid DB не заменяет рабочую DB.

### M1.3. Починить глобальный AssetChannel cache

Файл: `goroku/utils/entity.go:53-59,239-285`.

Работы:

1. Минимальный fix: защитить map `sync.RWMutex`.
2. Предпочтительный следующий шаг: сделать cache экземпляром сервиса, а не
   package global.
3. Добавить явный cleanup expired entries.
4. Не возвращать fake channel при incompatible client; вернуть typed error.
5. Добавить reset helper только для tests либо создавать isolated cache.

Тесты:

- 100 concurrent lookup/create calls под `-race`;
- cache hit не создаёт повторный канал;
- expired entry обновляется;
- shuffled repeated tests независимы.

Критерии готовности:

- `go test -race -shuffle=on -count=10 ./goroku/utils` стабилен;
- production map не доступна без lock.

### M1.4. Сделать web runtime registry типизированным и race-safe

Файлы: `goroku/web/core.go`, `goroku/web/root.go`, `goroku/webiface`.

Проблема:

- `WebCore` и embedded `Web` дублируют `clientData`;
- одна map записывается под `wc.mu`, но читается под `w.mu`;
- часть чтений выполняется без lock;
- значения представлены `[]any` и fallback fake TGID.

Целевой тип:

```go
type ClientRuntime struct {
    ID      int64
    Client  TelegramClient
    Modules ModuleRegistry
    DB      Database
}
```

Работы:

1. Оставить один registry и один mutex.
2. Заменить `AddLoader(any, any, any)` на `RegisterClient(ClientRuntime) error`.
3. Удалить `DefaultFallbackTGID`.
4. Добавить `UnregisterClient` для shutdown/reconnect.
5. Возвращать snapshots, не отдавать map вызывающему коду.

Тесты:

- concurrent register/list/auth under `-race`;
- duplicate ID policy;
- unregister во время handler request;
- zero ID отклоняется.

Критерии готовности:

- в web registry нет `map[int64][]any`;
- все map accesses используют один synchronization policy;
- race tests стабильны.

### M1.5. Сделать command registry owner-aware

Файлы: `goroku/loader.go`, `goroku/dispatcher.go`, `goroku/security.go`.

Проблема:

- `commands` хранит только handler;
- dispatcher повторно обходит unordered module map для metadata и owner;
- collision молча заменяет handler;
- unload может удалить команду другого модуля;
- aliases не удаляются при unload.

Целевой контракт:

```go
type CommandRegistration struct {
    Name       string
    ModuleName string
    Handler    CommandHandler
    Meta       CommandMeta
    Permission int
    RateLimit  bool
}
```

Работы:

1. Подготовить все registrations до commit.
2. Запрещать command/alias collisions с понятной ошибкой.
3. Если replacement нужен продукту, сделать его explicit и transactional.
4. Dispatcher получает handler, owner, metadata и permission одной lookup
   операцией.
5. Unload удаляет только owned registrations.
6. Удалять aliases, watchers, loops и module-owned extensions.

Тесты:

- две команды с одинаковым именем;
- alias конфликтует с command;
- failed registration не оставляет side effects;
- unload A не удаляет command B;
- security mask всегда принадлежит handler owner;
- map iteration order не влияет на результат.

Критерии готовности:

- dispatcher не сканирует `GetModules()` для поиска command owner;
- коллизия никогда не изменяет security policy молча;
- registry имеет deterministic tests.

## 8. Milestone M2: persistence correctness

Оценка: 5-8 дней. Приоритет: P0/P1. Зависимости: M1 regression baseline.

### M2.1. Определить persistence model

Перед изменением кода зафиксировать решение:

- локальный JSON является durable source of truth, Redis является mirror/cache;
  либо
- Redis является source of truth, локальный файл является journal/fallback.

Рекомендуемый минимальный вариант: локальная atomic copy всегда обновляется при
успешном logical commit, Redis получает versioned mirror асинхронно.

### M2.2. Исправить Redis generation protocol

Файл: `goroku/database.go:58-106,149-208`.

Работы:

1. Добавить monotonic `generation`.
2. Snapshot для flush содержит generation и immutable bytes.
3. После Redis I/O dirty flag очищается только если текущая generation равна
   записанной.
4. Не держать DB mutex во время network I/O.
5. Возвращать ошибку invalid `REDIS_URL`, а не молча отключать Redis.
6. Добавить stop channel/context, WaitGroup и `Close(ctx)`.
7. Закрывать `redis.Client`.

Тесты:

- mutation во время delayed flush не теряется;
- out-of-order responses не откатывают state;
- Redis unavailable не блокирует local commit;
- restart без Redis читает последнюю local generation;
- `Close` дожидается последнего flush.

### M2.3. Сделать JSON writes атомарными

Файлы: `goroku/database.go`, config writer в `goroku/bootstrap.go` или
`goroku/configurator.go`.

Алгоритм:

1. Serialize в память до изменения текущего файла.
2. Создать temp file в том же directory.
3. Выставить `0600` до записи секретов.
4. Write all, `fsync` file, close.
5. Atomic rename.
6. `fsync` directory на поддерживаемых OS.
7. Сохранять последнюю валидную backup copy.

Критерии готовности:

- simulated write/rename failure оставляет читаемую предыдущую DB;
- parse error не приводит автоматически к тихой пустой DB;
- recovery явно логирует выбранную backup generation.

### M2.4. Завершить Database error API

Целевые сигнатуры:

```go
Get(owner, key string, defaultValue any) (any, error)
Set(owner, key string, value any) error
Delete(owner, key string) error
Update(items map[string]map[string]any) error
Reset(data map[string]map[string]any) error
Save(context.Context) error
Close(context.Context) error
```

Работы:

1. Заменить `bool` на `error` по одному vertical slice.
2. Не игнорировать persistence failures.
3. Удалить redundant `Save()` после `Set()`.
4. Добавить batch API для logical transaction.
5. Определить typed not-found и validation errors.
6. Синхронизировать `inlineiface` и `webiface`.

### M2.5. Запретить mutable aliases через Database API

Проблема: `Set`, `Get` и `Reset` передают maps/slices по ссылке, поэтому внешний
код может менять DB в обход mutex.

Работы:

1. Копировать mutable input на write boundary.
2. Возвращать копию mutable output либо typed immutable decode.
3. Предпочитать generic typed helpers:

```go
func GetAs[T any](db Reader, owner, key string, fallback T) (T, error)
```

4. Добавить concurrent mutation tests.

Критерии готовности M2:

- crash-safe local DB;
- Redis outage не откатывает данные;
- нет network I/O под global DB lock;
- все persistence failures наблюдаемы;
- `go test -race` покрывает concurrent Set/Save/Close.

## 9. Milestone M3: web security baseline

Оценка: 4-6 дней. Приоритет: P1. Зависимости: M1.4.

### M3.1. Исправить IP trust и HTML injection

Файл: `goroku/web/root.go:609-660,720-754`.

Работы:

1. Использовать единый `clientIP(r)`.
2. По умолчанию игнорировать forwarding headers.
3. Доверять им только при explicit trusted proxy CIDRs.
4. Нормализовать IP до rate-limit key.
5. Escape IP перед Telegram HTML message.
6. Ограничить длину диагностического значения.

Тесты:

- spoofed XFF не меняет key без trusted proxy;
- trusted proxy использует ожидаемый client IP;
- `<code>` injection экранируется;
- IPv4/IPv6 с port нормализуются.

### M3.2. Ограничить pending auth

Работы:

1. Проверять наличие ready client до создания pending entry.
2. Всегда удалять entry через `defer`.
3. Уважать `r.Context().Done()`.
4. Ограничить общий concurrent pending count и count per IP.
5. Заменить polling ticker на event-driven confirmation, где возможно.
6. Периодически очищать expired entries.

### M3.3. Безопасные HTTP defaults

Файл: `goroku/web/core.go:106-153`.

Работы:

1. Default bind: `127.0.0.1`.
2. Public bind требует explicit `--web-bind` и warning.
3. Настроить `ReadTimeout`, `WriteTimeout`, `IdleTimeout`,
   `ReadHeaderTimeout`, `MaxHeaderBytes`.
4. SSH tunnel выключить по умолчанию.
5. Останавливать tunnel вместе с server.
6. Добавить `/healthz` и `/readyz` без секретов.

### M3.4. Усилить session/setup/CSRF contract

Работы:

1. `randomToken` возвращает error при отказе CSPRNG.
2. Setup token single-use после успешной initial setup.
3. Не передавать token в URL после onboarding; использовать one-time exchange.
4. Для state-changing routes требовать method и CSRF token.
5. Отсутствующие Origin/Referer не считать достаточной проверкой для browser
   session.
6. Добавить session expiry и rotation.

Критерии готовности M3:

- web не доступен с внешнего интерфейса по умолчанию;
- spoofed headers не обходят rate limit;
- slow-client tests не удерживают ресурсы бесконечно;
- setup token нельзя использовать повторно;
- disconnect клиента не оставляет pending requests.

## 10. Milestone M4: controlled execution и plugins

Оценка: 7-12 дней. Приоритет: P1. Зависимости: owner-aware security registry.

### M4.1. Ввести единый ProcessExecutor

Применение: Python/C/C++/Node/PHP/Ruby/Rust eval и terminal.

Контракт должен включать:

- context deadline;
- bounded stdout/stderr;
- process-group kill;
- concurrency semaphore;
- working directory policy;
- environment allowlist;
- optional CPU/memory/process/file/network limits;
- structured audit result.

Тесты:

- infinite process прекращён;
- child process также прекращён;
- output flood ограничен;
- timeout, cancellation и non-zero exit различимы;
- semaphore ограничивает параллелизм.

### M4.2. Убрать ложный timeout Yaegi

Файл: `goroku/modules/eval.go`, `yaegi_worker.go`.

**Сделано:** Go eval в отдельном worker process (`--yaegi-worker` re-exec того же
бинарника / test binary). Parent шлёт JSON (code + snapshots) через stdin;
`ProcessExecutor` убивает process group по timeout/cancel. Owner-only сохранён.
Лимиты: нет shared memory с ботом; `Loader` недоступен; `msg`/`client`/`db` —
JSON snapshots.

Критерий готовности: бесконечный Go eval не оставляет CPU-consuming goroutine в
основном процессе после deadline — worker process killed.

### M4.3. Зафиксировать dangerous capability invariant

Работы:

1. Eval, terminal и native plugin install всегда требуют owner identity.
2. Обычная настройка security mask не может случайно выдать dangerous
   capability группе.
3. Первая активация требует явного подтверждения.
4. Audit log содержит actor, chat, capability, language/command digest, duration,
   exit status и truncation marker.
5. Не логировать полный secret-bearing input/output.

### M4.4. Ввести plugin trust policy

Файлы: `goroku/modules/loader.go`, `hot_plugin_linux.go`, plugin security module.

Работы:

1. HTTPS-only по умолчанию.
2. Запрет private, loopback, link-local и metadata IP targets без unsafe flag.
3. Ограничить redirects и повторно проверять destination IP.
4. Manifest: name, source URL, commit, SHA-256, signer, minimum Goroku API.
5. Trust привязывать к digest/signer, не к MD5 имени.
6. Pipeline: download -> verify -> compile -> instantiate -> validate -> atomic
   registry commit.
7. Старый module не выгружать до успешной подготовки нового.
8. Документировать, что native Go plugin нельзя реально выгрузить из process.

Критерии готовности M4:

- unsigned install требует explicit unsafe confirmation;
- SSRF targets отклоняются;
- failed update сохраняет старый module;
- output flood не вызывает OOM;
- dangerous capabilities покрыты security tests.

## 11. Milestone M5: lifecycle и bounded concurrency

Оценка: 5-8 дней. Приоритет: P1. Зависимости: `Database.Close`, typed registry.

### M5.1. Ввести `App.Run(ctx) error`

Убрать final `select {}` и `os.Exit` из library lifecycle.

Рекомендуемый shutdown order:

1. Остановить приём новых web/auth requests.
2. Остановить dispatcher intake.
3. Дождаться bounded command workers.
4. Вызвать module `OnUnload`, остановить loops.
5. Остановить inline polling и handlers.
6. Остановить security reload workers.
7. Disconnect Telegram clients.
8. Flush/close databases и Redis.
9. Остановить tunnels и web server.
10. Sync logger и вернуть error вызывающему `main`.

Тесты:

- cancellation завершает app до timeout;
- hooks вызываются один раз в правильном порядке;
- повторный Stop безопасен;
- после shutdown нет известных goroutine leaks;
- final DB mutation durable.

### M5.2. Исправить inline lifecycle

Файл: `goroku/inline/core.go`.

Локальный результат: implementation-complete. Lifecycle принадлежит явной generation с
`context.CancelFunc`, intake gate, `sync.Once` и `WaitGroup`; `Close(ctx)` разделяет одну
completion между concurrent callers, а timeout ограничивает только ожидание caller-а.
Polling, TTL cleanup, registration/bootstrap, update/callback/input/slideshow/unload workers
учтены; restart ждёт drain предыдущей generation. Application shutdown ожидает `Close(ctx)`.
Fake-based tests не используют Telegram network и проходят под race/shuffle.

Работы:

1. `sync.Once` для stop.
2. WaitGroup для polling, TTL cleaner и update handlers.
3. Не заменять active stop channel.
4. Сбрасывать lifecycle state только контролируемым restart path.
5. Передавать context в long operations.

### M5.3. Заменить goroutine-per-command rate limiter

Файл: `goroku/dispatcher.go:655-732`.

Работы:

1. Выбрать token bucket/sliding window с timestamps.
2. Использовать один cleanup ticker или bounded expiry queue.
3. Удалять пустые user/chat entries.
4. Ограничить map cardinality.
5. Использовать injectable clock в tests.

### M5.4. Ввести bounded command executor

Работы:

1. Настраиваемый worker/semaphore limit per client.
2. Отдельный limit для watchers.
3. Context cancellation на shutdown.
4. Panic recovery и metrics остаются на boundary.
5. Определить overflow policy: reject, queue с limit или coalesce.

Критерии готовности M5:

- shutdown не вызывает `os.Exit` из package `goroku`;
- background workers имеют owner и stop contract;
- burst traffic не создаёт неограниченные goroutines/timers;
- repeated lifecycle tests проходят под race detector.

## 12. Milestone M6: module lifecycle и архитектурное разделение

Оценка: 2-4 недели. Приоритет: P2. Зависимости: M1.5, M2, M5.

### M6.1. Транзакционная регистрация module

Целевой lifecycle:

```text
Factory
  -> Init dependencies
  -> Decode/validate config
  -> Prepare commands/aliases/watchers/loops
  -> Atomic registry commit
  -> ClientReady
```

При любой ошибке выполняется cleanup, а module не виден dispatcher-у.

Работы:

1. Заменить reflection clone на explicit module factories.
2. Уточнить порядок `Init` и `ConfigReady`.
3. Ошибка `ConfigReady` должна отменять registration, а не только логироваться.
4. Registry должен владеть всеми registrations модуля.
5. `ClientReady` не запускать в неконтролируемой goroutine.
6. Удалить пустой `registerBuiltInModules` либо сделать его единственным
   composition point.

### M6.2. Разделить Database и Telegram asset storage

Работы:

1. Выделить `DocumentStore`.
2. Выделить `AssetRepository`.
3. Не выполнять Telegram RPC под DB lock.
4. Убрать обратную ссылку Database -> full client.

### M6.3. Разделить Dispatcher на policy pipeline

Компоненты:

```text
Parser -> RegistryLookup -> ChatPolicy -> SecurityPolicy
       -> MetadataFilter -> RateLimiter -> Executor
```

Работы:

1. Объединить дублирующиеся command/watcher predicates.
2. Компилировать regex при регистрации, не на каждом message.
3. Возвращать reason codes для debug/audit.
4. Удалить или реализовать пустые MTProto inline handlers.

### M6.4. Разделить Web

Выделить:

- server/listener;
- session and CSRF service;
- Telegram login coordinator;
- runtime registry;
- tunnel manager;
- static UI delivery.

Удалить global `web.Instance`.

### M6.5. Разделить CustomTelegramClient

Выделять постепенно, без big-bang rewrite:

- transport/session lifecycle;
- peer resolver;
- entity cache;
- message service;
- forum service;
- API limiter;
- account runtime composition.

Критерии готовности M6:

- core services тестируются через узкие interfaces;
- нет circular ownership client <-> DB <-> modules;
- module registration atomic;
- package globals не используются как dependency injection.

## 13. Milestone M7: завершение типизации

Оценка: 1-2 недели. Приоритет: P2. Зависимости: M6 contracts.

### M7.1. Cache API

- убрать `any` из permission/full user/full channel production API;
- унифицировать `TTL=0` для всех cache types;
- cache владеет своими maps и locks;
- typed `EntityRef`, `UserRef`, `ChannelRef` вместо runtime reflection;
- central alias insertion вместо дублирования.

### M7.2. Web/inline ports

- убрать `qrLogin any`, `Connection any`, `Proxy any`;
- интерфейсы размещать у consumer-а;
- `inlineiface` не должен зависеть от concrete inline implementation;
- удалить неиспользуемые interface methods;
- добавить compile-time implementation assertions.

### M7.3. Typed module config

- schema содержит type, default, validator и secret marker;
- persisted JSON normalization централизована;
- invalid value возвращает error с module/key context;
- secret fields redacted в logs/backups;
- config migration versioned.

Критерии готовности M7:

- hot paths не используют reflection для Telegram entities;
- production cache/web registries не принимают `any`;
- config errors диагностируются до module commit.

## 14. Milestone M8: продуктовый контракт и UX

Оценка: 2-3 недели. Приоритет: P2/P3. Зависимости: security baseline.

### M8.1. Привести CLI в соответствие с поведением

Сейчас `--qr-login`, `--no-auth`, `--proxy-host`, `--proxy-port`,
`--proxy-secret`, `--proxy-pass` сохраняются в структуру, но не управляют
runtime. README ошибочно называет `--proxy-pass` переключателем SSH tunnel.

Работы:

1. Удалить мёртвые flags либо реализовать их end-to-end.
2. Ввести отдельный `--ssh-tunnel`.
3. Валидировать полную MTProto proxy configuration.
4. Не перечитывать `os.Args` вручную для sandbox.
5. Добавить `goroku doctor` и `goroku config validate`.
6. Генерировать help/documentation из одного flag source.

### M8.2. Честно определить compatibility

Нужно выбрать один вариант:

1. Реалистичный: удалить обещание запуска Python Hikka/FTG/GeekTG modules и
   описать только migration/semantic compatibility.
2. Дорогой: определить compatibility API, sandboxed Python runtime и CI fixtures
   реальных modules.

До реализации второго варианта README не должен обещать полную обратную
совместимость.

### M8.3. Довести web onboarding

Работы:

1. Отделить installer от authenticated dashboard.
2. Убрать внешние CDN либо иметь offline fallback.
3. Исправить broken/закомментированный Lottie dependency.
4. RU/EN localization.
5. Semantic buttons, keyboard navigation, focus states, ARIA labels.
6. E2E paths: API credentials, phone code, 2FA, QR, bot setup, repeated login,
   expired session, server restart.

### M8.4. Минимальный operations dashboard

После security baseline можно добавить:

- account health;
- current version/commit;
- module list and trust status;
- last backup and restore verification;
- sanitized logs;
- restart/shutdown status;
- Redis connectivity;
- update availability без автоматического destructive git reset.

## 15. Milestone M9: тестовая стратегия и CI

Оценка: 1-2 недели параллельно другим milestones. Приоритет: P1-P3.

### M9.1. Исправить reproducibility CI

Работы:

1. Checkout до `setup-go`, чтобы cache видел `go.sum`.
2. Pin Go patch version.
3. Pin golangci-lint version, обновить config под используемый major.
4. Pin GitHub Actions по commit SHA для release/security pipeline.
5. Добавить `go mod tidy` diff check.
6. Добавить clean build главного binary.
7. Проверять, что ignored source files не меняют package build.

### M9.2. Security/dependency checks

- `govulncheck` с совместимой pinned версией;
- secret scanning;
- dependency review для PR;
- SBOM generation;
- license policy;
- отдельные небольшие dependency upgrade PR.

Особенно внимательно обновлять:

- `gotd/td` с `0.120.0` до современных версий;
- `x/crypto`, `x/net`, `x/sys`;
- `klauspost/compress` из-за archive handling.

### M9.3. Coverage policy

Не ставить общий 40% gate немедленно: это мотивирует писать дешёвые тесты.
Сначала установить gates для критических компонентов:

- persistence state machine: 80%+ branches целевых файлов;
- command registry/security routing: 90% основных веток;
- backup validation/apply: 80%+;
- web auth/session: 80%+;
- lifecycle/shutdown: scenario coverage обязательно;
- общий project floor сначала 20%, затем 30%, затем 40%.

### M9.4. Обязательные test suites

- database unit + Redis integration/miniredis;
- registry collision/property tests;
- backup/restore integration;
- web `httptest` + browser E2E;
- shutdown/goroutine leak tests;
- executor process cleanup tests;
- fuzz archive paths, URL parsing, HTML entities, config decoding;
- race tests с реальной конкурентной нагрузкой;
- `-shuffle -count` stability job.

Критерии готовности M9:

- clean checkout повторяет локальный build;
- CI не зависит от `latest` tools;
- module tests tracked;
- critical regression tests являются required checks.

## 16. Milestone M10: документация и доставка

Оценка: 1-2 недели. Приоритет: P3. Зависимости: стабильные contracts.

### Документы

Добавить:

- `ARCHITECTURE.md`;
- `SECURITY.md` и threat model;
- `QUICKSTART.md`;
- `CONFIGURATION.md`;
- `COMMANDS.md`;
- `MODULE_DEVELOPMENT.md`;
- `BACKUP_RESTORE.md`;
- `OPERATIONS.md`;
- `UPGRADE.md`;
- compatibility matrix;
- data retention и secret rotation guide.

README RU/EN должен содержать только проверяемые обещания. Требование Go нужно
синхронизировать с `go.mod`.

### Release pipeline

Работы:

1. SemVer tags и версия из tag/commit через `-ldflags`.
2. Содержательный CHANGELOG.
3. GoReleaser или эквивалент для Linux amd64/arm64; Android/Termux отдельно.
4. SHA-256 checksums, signatures и SBOM.
5. Docker image с non-root user и persistent data volume.
6. Hardened systemd unit.
7. Health/readiness checks.
8. Upgrade, canary и rollback runbook.
9. Не выполнять destructive git operations из production process.

Критерии готовности:

- установка не требует Go toolchain;
- binary version соответствует release tag;
- artifact проверяется checksum/signature;
- upgrade с предыдущей версии и rollback протестированы;
- backup restore проверен перед stable release.

## 17. Рекомендуемый порядок PR

Первые PR должны быть небольшими и проверяемыми:

1. `repo: stop ignoring module tests and runtime artifacts`.
2. `backup: remove recursive helpers and add round-trip tests`.
3. `utils: synchronize AssetChannel cache`.
4. `web: replace clientData []any registry`.
5. `modules: make command registrations owner-aware`.
6. `database: add generation-safe Redis flush and Close`.
7. `database: atomic local persistence`.
8. `database: migrate bool writes to errors`.
9. `web: trusted client IP and bounded pending auth`.
10. `web: safe listener defaults and full timeouts`.
11. `executor: bounded process output and cancellation`.
12. `eval: move Go evaluation out of process`.
13. `app: coordinated context-based shutdown`.
14. `dispatcher: bounded workers and rate limiter`.
15. `modules: transactional lifecycle and factories`.
16. `docs: correct compatibility, CLI and Go version`.
17. `ci: pin tools, add security and critical coverage gates`.
18. `release: versioned signed artifacts`.

## 18. Параллельные потоки работы

После закрытия M1 задачи можно распределить так:

```text
Stream A: Database
  M2.1 -> M2.2 -> M2.3 -> M2.4 -> M2.5

Stream B: Web
  M1.4 -> M3.1 -> M3.2 -> M3.3 -> M3.4

Stream C: Execution security
  M1.5 -> M4.1 -> M4.2 -> M4.3 -> M4.4

Stream D: Lifecycle
  M2.2 + M3.3 -> M5.1 -> M5.2 -> M5.3 -> M5.4

Stream E: CI/docs
  M0.2 -> M9.1 -> M9.2 -> documentation corrections
```

Нельзя параллельно менять registry ownership и глубоко переписывать dispatcher
без согласованного целевого типа `CommandRegistration`.

## 19. Оценка объёма

| Milestone | Результат | Оценка |
|---|---|---:|
| M0 | Секреты и clean source/runtime layout | 0.5-1 день |
| M1 | Устранены release blockers | 4-7 дней |
| M2 | Crash-safe persistence | 5-8 дней |
| M3 | Безопасный web baseline | 4-6 дней |
| M4 | Controlled execution/plugins | 7-12 дней |
| M5 | Graceful lifecycle и bounded concurrency | 5-8 дней |
| M6 | Разделение god objects | 10-20 дней |
| M7 | Завершение типизации | 5-10 дней |
| M8 | Честный продуктовый контракт и UX | 10-15 дней |
| M9 | Тесты и CI | 5-10 дней параллельно |
| M10 | Документация и release delivery | 5-10 дней |

Минимум до безопасного beta: примерно 4-6 недель одного разработчика.
До уверенного stable с delivery и operations: примерно 8-12 недель.

## 20. Release gates

### Gate A: internal alpha

- backup recursion исправлена;
- command collision deterministic;
- web/cache races покрыты tests;
- JSON writes atomic;
- Redis flush не теряет generation;
- secrets rotated;
- `go test`, `go vet`, race проходят на clean checkout.

### Gate B: private beta

- web безопасно bind-ится на localhost;
- pending auth bounded;
- eval/terminal output и process lifetime bounded;
- graceful shutdown завершает все известные workers;
- backup/restore round trip проверен;
- README не содержит ложных compatibility/CLI claims.

### Gate C: release candidate

- plugin trust policy включена;
- critical suites имеют установленные coverage gates;
- browser E2E покрывает onboarding;
- health/readiness и operations docs готовы;
- upgrade/rollback проверены;
- version и changelog формируются из release process.

### Gate D: stable

- signed binaries/checksums/SBOM опубликованы;
- нет known P0/P1 defects;
- dependency/security scans clean либо risks документированы;
- restore из последнего stable backup проверен;
- clean install работает без Go toolchain;
- support matrix и incident/rotation procedure опубликованы.

## 21. Метрики результата

| Метрика | Сейчас | Alpha | Stable |
|---|---:|---:|---:|
| Clean `go test ./...` | fail локально | pass | pass |
| `go test -race ./...` | pass при низком покрытии | targeted races | required |
| Общее coverage | ~16% | >= 25% | >= 40% |
| Critical-flow coverage | не измеряется | >= 70% | >= 80% |
| Unbounded process output | есть | нет | нет |
| Unbounded event goroutines | есть | сокращены | нет |
| Atomic DB write | нет | да | да |
| Graceful full shutdown | нет | да | да |
| Typed command ownership | нет | да | да |
| Reproducible release | нет | build artifact | signed release |

## 22. Что не делать сейчас

- Не начинать новый dashboard до web security baseline.
- Не добавлять новые eval languages до общего executor-а.
- Не обновлять одновременно `gotd/td` и persistence/dispatcher internals.
- Не делать массовую замену всех `any` без стабилизации interfaces.
- Не писать общий framework ради будущих use cases.
- Не считать текущий race pass доказательством отсутствия races.
- Не выпускать public stable с нативными unsigned plugins по умолчанию.
- Не обещать Python module compatibility без executable compatibility suite.

## 23. Самая первая задача

**Сделано formal:** `a07c96a` + M9.1/M0.2/M1.3–M5. **M6.1** implemented in
worktree (agent verify PASS); needs user commit for formal **Завершено**.

**Сейчас:** commit M6.1 on ask → then **M6.2** DocumentStore/AssetRepository.

Handoff:

```text
Orchestrator. Read docs/plans/goroku-roadmap-2026.md. M6.1 code done uncommitted;
on "коммить" stage product+tests only and commit; then M6.2 agent.
Do not redo M2–M5/M9.1; no user_modules into package path; push only on ask.
```
