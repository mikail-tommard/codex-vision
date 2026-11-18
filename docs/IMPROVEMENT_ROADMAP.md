# Codex Vision: аудит и план улучшений

Актуальный короткий backlog с приоритетами и командами продолжения:
[NEXT_STEPS.md](NEXT_STEPS.md).

## Обновление 0.14.4

Добавлен experimental read-only ScreenCast/PipeWire через отдельный opt-in и
установленный GStreamer. Native path создаёт одноразовую portal session, выбирает
один source с системным согласием, получает isolated FD и использует стабильный
serial. Duration/frame budgets обязательны; latest-only кадры запрашиваются
явно, дубли подавляются. Revocation/stop очищают кадры, failed cleanup запрещает
новый native source. Desktop mapping и input не добавлены.

Парный MCP mock A,A,A,B,B: 5→2 images, 7770→3108 raw image bytes,
пример result JSON 12585→6380 bytes, 5 pulls в обоих вариантах. Native timing и
Codex usage не измерены. Linux portal/GStreamer и реальный compositor здесь
не запускались; воспроизводимый opt-in test и ограничения описаны в
[wayland-screencast.md](wayland-screencast.md). Native API требует portal v6+.

## Обновление 0.14.3

Реализованы opt-in discovery cache и подавление повторного дерева по ревизии;
targeting/readiness/verification кэш не используют. `ui_controls` доступен в
compact без изображения. Добавлены verified workflow recipes только в RAM
(8/15min/32KiB), повторный запуск с fresh targets и wait steps без ввода.
Парные synthetic measurements: result JSON 34320→7908 bytes/tree reads 5→1;
повторный workflow request 260→159 bytes. Каталог/transport/реальные Codex tokens
в эти числа не входят. API и ограничения: [economy-0143.md](economy-0143.md).

Wayland начат отдельным opt-in Screenshot portal backend (D-Bus): системный
интерактивный запрос, отмена, timeout, stop, bounded local file decode. Desktop
mapping, window discovery, streaming/PipeWire и input не реализованы; compact
предлагает только capture/status/stop. Real compositor не проверялся.
Исторический аудит и предыдущие итерации ниже не описывают текущую полноту функций.

## Область аудита

Аудит локального исходного дерева версии `0.13.6`, 2026-11-17. Изучены README,
документация наблюдения, agent loop, конфигурации, платформ, оценки и безопасности;
Go-модуль, CI, MCP handlers, очереди операций, AX references, observation manager,
policy, input validation, image encoding и платформенные backend/test contracts.
Локальное дерево содержит неотслеживаемые Git исходники: это исходная точка работы,
а не изменения, сделанные в этой итерации. История удалённого репозитория не проверялась.

## Текущее состояние и повторное использование

| Компонент | Реализовано | Ограничение |
| --- | --- | --- |
| `internal/mcpserver` | Compact/full каталоги (12/29 инструментов), `desktop_observe`/`desktop_act`, одноразовые view, пакет до 12 шагов, AX ожидания, explicit expectations, continuation и диагностика | Нет движка длительных сценариев с checkpoint/resume; результаты ещё не унифицированы в пять запрошенных статусов |
| `internal/platform` | Интерфейс Desktop и необязательные UIReader, ControlReader, CursorReporter, ProcessMatcher; native macOS/Windows, Linux X11, mock | AX controls/fields только macOS; Wayland явно отвергается; нет общего API событий UI |
| `internal/observation` | Локальный sampler, adaptive interval, focus regions, changed-region delivery, fingerprints, temporal persistence, дедупликация, preview, отменяемое ожидание | Сэмплированные пиксели не отражают смысл; маленькие изменения могут пропускаться. Выдача delta-региона не означает захват только этого региона ОС |
| `internal/safety` | Observe default, allowlist действий/executable, локальный review, foreground checks, budgets, stop-file и latch | Не устраняет внешние гонки UI; нулевые локальные бюджеты разрешены конфигурацией. Нет отдельного бюджета длительного workflow |
| `internal/input`, `internal/vision` | Проверка текста/клавиш/границ, bounded pointer motion, PNG/JPEG, pixel budget, масштабирование | Масштабирование nearest-neighbor; нет измеренного выбора качества по читаемости текста |
| Метрики/оценка | Счётчики MCP bytes/images/calls, последние 12 обезличенных диагностик, timings этапов, парные synthetic workflows | Это не task success rate реальных приложений и не токены провайдера; нет единого измерения capture/target lookup/verification |
| Память | Временные process-local view, AX snapshots и frames | Нет локальной долговременной UI-памяти, TTL storage, очистки и изоляции сессий на диске |

Существующие safety gate, lock lanes, mock workflow fixtures, expectations и
observation manager следует расширять, а не заменять. Внешняя ML-модель для
первых этапов не нужна. UI-текст остаётся недоверенными данными, а не разрешением.

## Обнаруженные проблемы

1. **P0: view переживает отдельный MCP-ввод.** После `desktop_observe` вызов
   `ui_fill_batch`, primitive input, `window_focus` или `app_launch` не сбрасывает
   `views.current`. Старые координаты/контролы могут относиться к другому состоянию
   того же окна; одинаковые process/geometry не доказывают актуальность.
2. **P0: наблюдение пересекается с вводом сервера.** `desktop_observe` занимает
   UI-очередь, но не input lane; primitive input может менять UI во время подготовки
   view. Одних foreground checks до/после недостаточно при неизменной геометрии.
3. **P1: свежесть ограничена TTL и структурой.** View допускает минуту жизни;
   значимая смена страницы внутри окна не обязательно меняет его identity.
   Потребуется явная стратегия проверки visual sequence/возраста без ложных отказов
   из-за курсора/анимации. Нельзя выдавать pixel equality за semantic equality.
4. **P1: неоднородная верификация.** `Expect` доказывает только заданный native
   predicate. Визуальный change/readiness не доказывает сохранение/отправку.
   MCP timeout может быть нормальным результатом, поэтому клиент обязан читать
   interaction/verification, а не только `IsError`.
5. **P1: нет семантического selector API для controls.** Есть точные field labels
   и short-lived target_id. Нужен bounded поиск по label/role с отказом при
   неоднозначности и повторной проверкой непосредственно перед вводом.
6. **P2: AX caching и OS events отсутствуют.** Сейчас безопасность требует свежих
   bounded native reads. Кэш нельзя использовать для авторизации записи; его
   инвалидация зависит от input/focus/UI events и backend capabilities.
7. **P2: нет долговременной UI memory и workflow runner.** Их добавление без
   privacy design и явных budgets расширит поверхность риска.
8. **Платформенная проверка неполна.** CI builds/unit tests и synthetic fixtures
   не доказывают работу интерактивных Windows/macOS/X11. Wayland требует отдельного
   portal/PipeWire backend и системного согласия.

## Этапы реализации и критерии готовности

| Приоритет / этап | Изменение | Критерий готовности | Риски и зависимости |
| --- | --- | --- | --- |
| P0 / итерация 1 | Инвалидация view при другом MCP-вводе; сериализация observe/input | Regression через MCP: старый view отвергнут без дополнительного ввода; отдельный read сохраняет view; ожидание lane отменяется; stop не блокируется; исходные tests/race/vet проходят | Консервативное сбрасывание view даже при отказе политики; дополнительное ожидание observe. Внутренний preflight focus `desktop_act` должен сохраниться |
| P1 / итерация 2 | Стратегия свежести view, semantic selector label/role поверх наблюдённых controls | Неоднозначные/disabled/неполные/сменившиеся цели не получают ввод; успешная уникальная цель проходит тот же safety gate; mock и реальные opt-in fixtures | AX identity не всегда стабилен; bounds не доказывают отсутствие overlay; безопасный отказ вместо догадки |
| P1 / итерация 3 | Единый action outcome: success/failed/timeout/ambiguous/needs_confirmation, явная strength проверки | Добавочные совместимые поля; success только по заявленным наблюдаемым условиям; cancellation, partial input и timeout тестируются отдельно; никаких автоматических повторов write | Совместимость компактного каталога и существующего IsError; точное различение OS event posting и эффекта |
| P2 / итерация 4 | Измерения capture/lookup/verification, response wire bytes, AX read cache для discovery, события где доступны | Воспроизводимый before/after на одинаковых fixtures; cache invalidation при input/focus/stop; stale cache никогда не разрешает write; bounded fallback polling | Реальные OS events различаются; benchmark не заменяет GUI latency; fingerprint sampling теряет мелкие детали |
| P2 / итерация 5 | Opt-in локальная UI memory: только стратегии/структура, session namespace, TTL, purge | Disabled по умолчанию; explicit clear; retention и session isolation tests; нет значений, секретов, screenshots и старых actionable coordinates | Даже labels/titles могут содержать PII; сначала ограниченная схема и threat review |
| P2 / итерация 6 | Bounded workflow runner поверх существующих primitives | Явные deadline/action budget/stop/permissions/conditions; checkpoint содержит состояние, а не разрешение; resume только после fresh observe; журнал без содержимого; необратимый шаг не повторяется | Нельзя надёжно классифицировать последствия GUI по тексту кнопки; нужен явный контракт повторяемости |
| P3 / итерация 7 | Платформенные capabilities, UIA/AT-SPI discovery и Wayland design spike | Unsupported явно отражён; native opt-in checklist для каждой ОС; Wayland не объявлен готовым без portal consent/capture/input/cancel tests | Новые native APIs, упаковка, системные разрешения; не вводить обязательную тяжёлую зависимость |

## Безопасность и производительность

Все новые writes проходят `Session.Execute`; tool arguments/UI text не меняют
локальную политику. Automatic recovery допускается для bounded reads/preflight,
но не replay кликов/клавиш после возможной доставки. Stop не должен ждать UI/input
lanes. Старые snapshots не служат разрешением после смены состояния.

Оптимизации принимаются только с измерениями на одинаковых fixtures до/после.
Для итерации 1 ожидается улучшение корректности, а не скорости: метрики — число
ошибочно допущенных действий, отсутствие новых capture/model calls для отказа,
регрессии времени suite и гонок. Проценты ускорения заранее не заявляются.

## Итерация 1: завершена

- Исходная проверка `go test ./...` прошла с отдельными writable Go caches.
  Системные caches вне workspace недоступны на запись; зависимости не изменялись.
- Выбраны проблемы P0 (актуальность view и пересечение observe/input).
- Реализовано: общий классификатор input tools используется для input lane;
  `desktop_observe` занимает UI и input lanes в том же порядке, что `desktop_act`.
  MCP-обвязка сбрасывает старый view перед отдельным input handler. Внутренний
  preflight focus и validation-only ошибки `desktop_act` сохраняют прежний контракт.
- Новые regression tests воспроизвели обе проблемы **до исправления**: старый view
  был принят после клавиши; observe/input пересекались в обоих порядках захвата.
  После исправления восемь вариантов смешанного ввода дают `stale_reference`,
  **0 дополнительных действий и 0 изображений** при отказе. Проверены full/compact,
  отказ политики, native failure, сохранение view при read, успешный fresh observe,
  отмена ожидания lane, отсутствие утечки lane и stop при занятых lanes.
- Synthetic form workflow обновлён: после `ui_fill_batch` получает свежий view.
  Evaluator явно допускает один дополнительный кадр только для этого обязательного
  refresh: его доставка зависит от момента работы sampler. Счётчики не скрываются.
- Проверки: `go test ./...`, `go vet ./...` и `go test -race ./...` прошли.
  Native build текущей macOS и cross-build Windows amd64 / Linux amd64 прошли.
  Это сборки и synthetic/native-contract unit tests, **не интерактивное GUI-тестирование**.
- `python3 scripts/evaluate.py` прошёл (8/8). Последний unified-прогон:

  | Synthetic scenario | MCP calls | Images | Result content bytes | Local elapsed ms |
  | --- | ---: | ---: | ---: | ---: |
  | browser_like | 2 | 2 | 15439 | 572 |
  | form_like | 4 | 1 | 11285 | 562 |
  | terminal_like | 2 | 1 | 9338 | 557 |
  | cad_like | 2 | 2 | 15442 | 570 |

  Это отдельные измерения текущей версии, не benchmark ускорения до/после.
  Для формы обязательный refresh добавил один MCP-вызов к прежней
  последовательности из трёх вызовов; прежняя последовательность теперь отвергается
  как небезопасная. Ускорение и экономия токенов не заявляются. `result_content_bytes`
  — существующая метрика content, а не полный serialized MCP wire size.
- Граница исправления: сериализуются серверные MCP observe/input calls.
  Background sampler и внешние изменения приложения не становятся атомарными;
  другие read APIs не получили общего обещания atomic snapshot. P1 остаётся открытым.

## Следующие итерации: реализация 0.14.0

### Итерация 2 — semantic targeting и свежесть

Реализованы `DesktopStep.target {label,role}` и полный точный поиск по наблюдённому
дереву. Target должен быть показан в view, дерево должно быть complete, disabled
и unknown-enabled не допускаются. Повторная проверка перед каждым шагом; bounds
не обновляются вслепую. Дубликаты за пределами 64 показанных контролов также
учитываются. Смена title инвалидирует view; координатный ввод дополнительно
проверяет published significant visual revision. Поле Window в следующем view
обновляется после подтверждённого изменения title.

Safety gate повторно проверяет контрол после local review: label/role/bounds,
enabled, уникальность, foreground и title. Это закрывает дополнительный промежуток
между MCP preflight и возвращением из approval dialog. Нативная доставка событий
по-прежнему не атомарна с последней проверкой.

Тесты: unique/role disambiguation, duplicates, incomplete/disabled/unknown,
late duplicate после первого шага, неверные selectors до refocus, title/visual
staleness, семь изменений цели после approval и нулевая доставка при отказе.

### Итерация 3 — результаты действий

Добавлено обратносовместимое `outcome`: success/failed/timeout/ambiguous/
needs_confirmation, evidence, verified, input_may_have_posted и неизменно false
retry_action. Legacy IsError/interaction сохраняются. Event posting и visual
change не становятся success. Scope native predicate/readback/foreground указан
явно; бизнес-результат приложения не доказывается.

Тесты проверяют все статусы, cancellation/deadline, postcondition и compact/full
MCP encoding. Счётчики outcome не выдаются за частоту успеха реальных задач.

### Итерация 4 — измерения, частично

Реализованы count/total_ns/max_ns этапов capture, controls_read, target_lookup,
action, verification, workflow. `measure_wire_bytes` opt-in измеряет сериализованный
CallToolResult с base64 и выбранным profile encoding; без transport envelope.
Есть unit-проверка точного размера JSON обоих профилей. Дополнительная сериализация
и финальная AX-проверка имеют стоимость; ускорение не заявляется.

AX discovery cache, OS UI-event subscriptions и измерение реального task success
rate остаются открытыми. Свежая авторизация write из cache не допускается.

### Итерация 5 — фундамент памяти, частично

Добавлен `internal/uimemory`: opt-in private local store, keyed structural digests,
128 receipts на namespace, TTL 1–720h, explicit clear, atomic replacement и
строгая bounded JSON-схема. Текст UI/values/typed text/process paths/coordinates/
screenshots/plans не сохраняются. Запись только после проверенного explicit native
postcondition. Новый namespace на запуск по умолчанию; локальная настройка
namespace позволяет намеренно переиспользовать тот же cache.

Тесты: повторное открытие, изоляция namespace, TTL pruning на диске, capacity,
clear, повреждённый файл, отсутствие raw UI-content и невозможность обойти
проверку сместившейся цели. Windows storage явно unsupported до native ACL
adapter; POSIX mode bits не считаются правами Windows. Не реализованы библиотека
повторяемых сценариев и ускорение поиска на основе memory. Физический TTL pruning
работает при open/access, не отдельным offline-сервисом.

### Итерация 6 — bounded native workflow

Opt-in `workflow_control`: asynchronous start/status/cancel/resume, одна задача,
явный action budget до 50 и общий deadline до 300s. На каждом шаге — свежий native
read, обычная локальная policy и обязательный observable postcondition.
Checkpoint только после проверки. Resume только после pre-input failure,
не продлевает deadline и ограничен количеством шагов + 3 попытки. Любая попытка
write с неподтверждённым результатом запрещает replay. Stop/close отменяют worker.
Отчёт/журнал не содержат plan, UI text, typed content или error strings.

Тесты: два проверенных действия с нулём captured frames, checkpoint, resume,
post-action timeout без replay, cancellation/stop при занятой input lane,
read-only/allowlist, полная проверка плана до первого шага, finite recovery budget.
Runner пока для одного наблюдённого окна, native selectors/keyboard и native
postconditions; launch/scroll/drag/visual-only plans и restart persistence отсутствуют.

### Итерация 7 — capabilities, частично

`session_status.capabilities` явно описывает implemented interfaces отдельно от
OS permissions и local policy. Native UI events=false. UIA, AT-SPI и Wayland
portals не добавлены; cross-build не выдаётся за проверку интерактивного desktop.

### Валидация и границы

Промежуточные `go test ./...` и `go vet ./...` прошли. Race-прогон выявил
wall-clock-зависимость старого теста UI wait/capture: fixture теперь становится
готовым после фактического продвижения polling и capture, а не после sleep 450ms.
После исправления полный race-прогон прошёл. После добавления post-approval safety
guard снова прошли `go test ./...`, `go test -race ./...` и `go vet ./...`.
Последний отдельный fix отзывает resumable checkpoint при session_stop даже у
уже остановившегося worker: workflow/stop tests прошли обычно и с race detector;
`go vet ./...` повторно прошёл.

Native build текущей macOS и cross-build Windows amd64/Linux amd64 версии 0.14.0
прошли. Интерактивные GUI-прогоны на этих ОС не запускались. Evaluator прошёл 8/8;
машиночитаемый отчёт: [evaluation-autonomy.json](evaluation-autonomy.json).

| Unified synthetic scenario | MCP calls | Images | Content bytes | Local elapsed ms |
| --- | ---: | ---: | ---: | ---: |
| browser_like | 2 | 2 | 15627 | 612 |
| form_like | 4 | 1 | 11631 | 600 |
| terminal_like | 2 | 1 | 9525 | 586 |
| cad_like | 2 | 2 | 15630 | 622 |

Новый native workflow fixture подтверждает два действия без captured frames.
Это данные текущих synthetic drivers, не сравнение скорости версий. Дополнительные
проверки/metadata могут увеличивать стоимость; выигрыш latency/tokens не заявляется.

Добавленные/расширенные компоненты: `internal/mcpserver/{targeting,outcome,
performance,workflow,memory}.go`, соответствующие tests, `internal/uimemory`,
`internal/safety` control proof, `internal/platform` capabilities/Action metadata,
config flags, MCP adapters и документация `docs/autonomy.md`.

## Итерация 8 — native identity/context и экономия вызовов (0.14.1)

Реализовано:

- Native macOS AX identity cache: CFEqual/CFHash объектов, до 2000 ссылок, scope
  по AX root + AXDocument, expiry 60s. При отсутствии AXDocument явно weaker
  native_window context. Ранее известная identity/context не может исчезнуть в
  структурный fallback. Native tokens/Document URL не выходят в MCP.
- Привязка проходит через agent view, fields snapshot, batch/workflow и safety
  gate после local approval. Заменённый объект с теми же label/role/bounds и
  изменение document context с прежним title дают отказ, включая keyboard/fields.
- Native-targeted click после pointer approach проверяет AX hit и bounded parent
  chain до исходного объекта. Field write проверяет context до mutation и после
  readback. Это не атомарная ОС-транзакция и не глобальная occlusion-защита для
  произвольных координат; слабые/отсутствующие native evidence остаются явными.
- Workflow start/status/resume принимают bounded wait_ms: ожидание completion
  через канал, без дополнительных model polls/captures. after_event/event_cursor
  возвращают journal delta. Status без ID восстанавливает последний run ID;
  cancel/stop доступны во время wait. Отмена RPC wait не повторяет/не отменяет
  отдельно разрешённый bounded worker автоматически.
- Persistent locator fingerprints исключают transient native object IDs; receipt
  не становится разрешением для нового объекта. Default compact catalog и tool
  signatures сохранены; новые workflow args влияют только на opt-in tool.

Проверки:

- `go test ./...`, `go test -race ./...`, `go vet ./...` прошли.
- Native build macOS и cross-build Windows amd64/Linux amd64 прошли.
- Native Foundation cache test проверяет CF identity, смену root/document,
  expiry и capacity. Mock tests проверяют same-looking replacement, потерю
  identity, прежний title, keyboard/fields context, отсутствие private IDs в MCP,
  approval-time races, wait timeout/cancellation/cursor validation и zero replay.
- Создан реальный controlled AppKit fixture; compilation test прошёл. GUI input
  opt-in использует только его executable allowlist и проверяет фактический
  callback кнопки. Прогоны native read/input **пропущены** в текущем окружении:
  нет Quartz console session, Accessibility и post-event permissions. Это не
  считается успешным GUI/E2E-прогоном. TCC/системные разрешения не изменялись.

Экономия измерена парно на одинаковых двух verified mock actions, с одинаковым
enabled catalog. До — start + status, после — start с wait_ms; это сравнение
стратегий доставки, не скорости версий:

| Profile | MCP calls до → после | Serialized result bytes до → после | Images после |
| --- | ---: | ---: | ---: |
| compact | 2 → 1 | 749 → 448 | 0 |
| full | 2 → 1 | 1390 → 832 | 0 |

Около 40% меньше JSON в этом fixture и вдвое меньше MCP-вызовов. Точные токены,
лимит/стоимость Codex, model latency, catalog/request bytes и transport не
измерялись; переносить эти проценты на квоту нельзя. Отчёт с ограничениями:
[evaluation-economy-0141.json](evaluation-economy-0141.json).

Основные файлы: platform UIContext/identity contracts, macOS `control_identity.h`,
bridge/UI/input adapters, `safety/policy.go`, MCP view/field/batch/workflow bindings,
`workflow_economy_test.go`, native cache/controlled fixture tests, документация.

## Итерация 9 — Windows private storage и summary delivery (0.14.2)

Completed:

- Windows storage adapter вместо безусловного unsupported: owner process-user
  SID, protected current-user/LocalSystem DACL в SecurityAttributes при создании
  directory/temp; проверка фактически открытого handle. Существующие ACL не
  исправляются. Неизвестные/broad/inherited/NULL DACL отвергаются.
- Локальный ACL-capable writable volume обязателен; UNC/network/device/ADS,
  reparse paths и hardlinked data отвергаются. Same-directory MoveFileEx без
  delete-then-rename или copy fallback, с повторной проверкой final ACL.
- POSIX storage вынесен отдельно; проверяются тип, private mode и same-file
  identity между Lstat и открытым handle. Temp creation перепроверяет directory.
- Windows skips общих memory/MCP tests сняты; добавлены native Windows descriptor,
  replacement, broad ACL without repair, hardlink и path tests. Portable ACL rule
  проверяется на текущем host независимо от Win32 runtime.
- `detail:"summary"` для workflow: успешные journal rows остаются на сервере;
  ответ сохраняет terminal state/checkpoint/cursor/uncertainty и последнюю ошибку.
  `journal_mode` явно сообщает projection. Full без detail не изменён; summary
  с after_event отвергается до запуска. Старые API/каталог по умолчанию сохранены.
- Во время action/verification running report теперь отмечает неподтверждённый
  ввод; uncertainty очищается только при verified checkpoint. Тест с реально
  принятым mock-событием проверяет running, cancellation и отсутствие replay.

Changed Files:

- `internal/uimemory/store.go`, новые `files_posix.go`, `files_windows.go`,
  `acl_policy.go` и соответствующие tests; общие `store_test.go`.
- `internal/mcpserver/workflow.go`, `workflow_summary_test.go`, `tools.go`,
  `memory_test.go`, `server.go`; `docs/autonomy.md`, `SECURITY.md`, `CHANGELOG.md`.

Tests:

- `go test ./...`, `go test -race ./...`, `go vet ./...` прошли на текущей macOS.
- Windows amd64 filesystem tests cross-compiled (`go test -c`), target Windows
  `go vet ./internal/uimemory` прошёл. **Windows runtime tests не выполнялись**;
  сборка не считается подтверждением поведения NTFS/Win32 ACL.
- Native macOS build, Linux amd64 и Windows amd64/arm64 cross-build прошли.
- Portable ACL policy: private/current owner, protected DACL, ordinary inheritance,
  world/admin grants, missing/null ACL, deny/callback ACE, inherit-only/inherited
  ACE, insufficient owner access и bounded entry count. POSIX link/mode regressions
  и новые summary/pending-input tests прошли.

Performance:

Парно, одинаковые восемь verified mock keyboard actions, один MCP-вызов с wait_ms,
0 изображений, одинаковый enabled catalog; отличается только full/summary delivery:

| Profile | Serialized result JSON: full → summary |
| --- | ---: |
| compact | 892 → 330 bytes |
| full | 1656 → 610 bytes |

Около 63% меньше результата в этом сценарии. Это не измерение токенов/квоты
Codex, requests/catalog/transport или model latency. Safety/native checks
одинаковы. [Машиночитаемый отчёт](evaluation-summary-0142.json).

Remaining Issues / Next Iteration:

Windows native filesystem tests нужно выполнить на реальной Windows или CI.
Этот шаг не добавляет UIA/AT-SPI/Wayland и не превращает receipt memory в полную
библиотеку сценариев; Windows GUI ещё не имеет semantic reader. Далее — native
adapters и безопасный discovery cache, с отдельными измерениями и platform tests.

## Продолжение

Следующий запуск: прочитать этот roadmap и `docs/autonomy.md`, выполнить baseline.
В unlocked macOS console запустить controlled native tests по `docs/autonomy.md`
под фактической разрешённой launch identity. На Windows выполнить новые private
storage tests. Затем platform-native read/event adapters и безопасный
discovery cache и полноценная библиотека структурных сценариев. Экономию измерять
парными сценариями: MCP calls, images, serialized bytes; не выдавать их за точные
токены/квоту Codex. Не ослаблять native uniqueness/post-approval checks ради cache
hits. Для всех Go-команд в ограниченной среде:

```sh
GOCACHE=/private/tmp/codex-vision-go-cache GOMODCACHE=/private/tmp/codex-vision-go-mod go test ./...
```
