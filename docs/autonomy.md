# Семантические цели, workflow и локальная память

## Семантический выбор

Запросите `desktop_observe {"include_controls":true}`, затем используйте реальный
`view_id` и точную подпись из ответа:

```json
{
  "view_id": "VIEW_ID_FROM_RESPONSE",
  "steps": [{"action":"click","target":{"label":"Save","role":"AXButton"}}],
  "expect": {"title_contains":"Saved"}
}
```

`target` и `target_id` взаимоисключающие; координаты одновременно с ними не
принимаются. Поиск точный, без fuzzy matching и внешней ML-модели. Подпись/роль
должны однозначно определять доступный элемент полного дерева, доставленный в
этом view. Дубликаты учитываются даже за пределами 64 показанных элементов.
Перед каждым шагом читается свежее native дерево. Изменение bounds, неизвестное
enabled-состояние и неполное дерево останавливают ввод. На backend без controls
семантические цели недоступны; fallback-клик не выполняется.

Дополнительная проверка выполняется внутри safety gate **после** локального
подтверждения: долгий диалог не позволяет кликнуть изменившийся/отключённый или
ставший неоднозначным контрол. Проверяются также foreground, геометрия и заголовок.
Этот дополнительный native read входит во время action и может увеличить latency.

Изменение заголовка окна инвалидирует view. Для координатных кликов/перемещений
проверяется также revision значимых визуальных изменений. Курсор и шум,
отфильтрованный sampler, сами по себе revision не меняют. Семантические цели могут
пережить визуальную анимацию, только если свежий native match прошёл проверки.
Это не атомарный снимок: изменения между двумя выборками и смена документа с тем
же заголовком могут оставаться незаметными. Pixel equality не доказывает смысл UI.

На macOS доступна дополнительная native-привязка: opaque ID AX-объекта и контекст
AX window/document. `view.context_scope` сообщает `native_window_document`, если
приложение предоставляет AXDocument, либо `native_window`. Смена контекста,
замена объекта и потеря ранее доступной identity запрещают старый ввод, даже при
совпадающих label/role/bounds/title. Это распространяется также на field snapshots
и клавиатуру из context-bound view. Без контекста сохраняется структурный режим
backend; наличие интерфейса не означает, что приложение предоставляет AXDocument.

Native IDs — внутренние process-local ссылки (до 60 секунд, до 2000 объектов),
не сохраняются как переиспользуемые координаты и не передаются агенту. Document URL
не возвращается. После pointer approach native-targeted click дополнительно
проверяет AX hit/цепочку родителей до исходного объекта. Неизвестный hit или
перекрытие даёт отказ; запасной координатный клик не выполняется. Это уменьшает
гонку, но не делает ОС/приложение транзакционными и не даёт глобальную гарантию
отсутствия перекрытий для произвольного координатного ввода.

## Результаты действий

Поле `outcome` добавляется к ответам существующих инструментов ввода, сохраняя
`IsError`, `interaction`, `verification` и `continuation`:

| status | Основание |
| --- | --- |
| success | Наблюдаемое native условие, AX readback поля или подтверждение foreground; точный scope указан в evidence |
| failed | Ошибка/отмена; проверяйте completed steps и input_may_have_posted |
| timeout | Условие не подтверждено до deadline; ввод не повторяется |
| ambiguous | Неоднозначная цель либо доступны только факты отправки событий/визуального изменения |
| needs_confirmation | Локальная политика требует approval; MCP-аргумент не может его предоставить |

`retry_action` всегда false. Даже `success` для предиката заголовка не доказывает
сохранение файла на диске, проведение платежа или бизнес-результат приложения.

## Включаемые возможности

Дополнительные инструменты выключены по умолчанию. Добавьте нужные параметры в
свой локальный конфиг, сохраняя обычные policy/allowlist настройки:

```json
{
  "workflows": true,
  "ui_memory_directory": "/absolute/private/codex-vision-memory",
  "ui_memory_namespace": "my-task-session",
  "ui_memory_ttl_hours": 24,
  "measure_wire_bytes": true
}
```

Это фрагмент, не готовый assist-профиль. Существующие конфиги и права сервер не
переписывает. После изменения конфигурации пересоберите сервер и перезапустите
MCP-подключение. Без flags прежние каталоги сохраняют 12/29 инструментов.

## Workflow

При `workflows:true` появляется `workflow_control`. Пример плана для тестового
приложения с заранее известными native labels и заголовками:

```json
{
  "action":"start",
  "window_id":"OBSERVED_WINDOW_ID",
  "timeout_ms":60000,
  "max_actions":2,
  "steps":[
    {"step":{"action":"click","target":{"label":"Open test document","role":"AXButton"}},"expect":{"title_contains":"Test document"}},
    {"step":{"action":"click","target":{"label":"Save","role":"AXButton"}},"expect":{"title_contains":"Saved"}}
  ]
}
```

Start без `wait_ms` возвращает ID сразу. Проверка: `{"action":"status","workflow_id":"ID"}`;
отмена: `{"action":"cancel","workflow_id":"ID"}`. Workflow живёт в текущем
процессе, отменяется при `session_stop`/закрытии сервера и не восстанавливается
после перезапуска. Одна активная задача на процесс; следующий start заменяет
историю завершённой задачи.

Для экономии MCP/модельных обращений добавьте к start `"wait_ms":3000`: сервер
локально ждёт завершения и может вернуть итог в том же вызове. Ожидание ограничено
оставшимся таймаутом RPC; если выполнение продолжается, возвращается running с
тем же ID. Это не таймаут/повтор самого workflow. Для следующей проверки:

```json
{"action":"status","workflow_id":"ID","wait_ms":3000,"after_event":2}
```

`after_event` берётся из предыдущего `event_cursor`, не угадывается. В ответе —
только новые journal entries; State/checkpoint/deadline сохраняются. Без cursor
остаётся прежний полный журнал. Status без ID находит последний workflow, если
ответ на start был потерян. Ожидание использует канал завершения, без busy polling,
скриншотов или модельных вызовов; cancel/session_stop остаются доступны. Отмена
ожидания RPC не равна отмене разрешённого asynchronous workflow — для этого есть
cancel. Invalid wait/cursor отвергаются до запуска или возобновления сценария.

Для обычной проверки завершения используйте `"detail":"summary"` вместе с
`wait_ms`: успешные строки журнала не повторяются. Ответ сохраняет State,
checkpoint, event_cursor, budgets, resumable и состояние неподтверждённого ввода;
при сбое остаётся последняя запись с кодом ошибки. `journal_mode:"summary"`
обозначает проекцию, а не удаление журнала. Полная история доступна обычным status.
Summary нельзя совмещать с after_event; без detail формат full остаётся прежним.

```json
{"action":"status","workflow_id":"ID","wait_ms":3000,"detail":"summary"}
```

`input_may_have_posted` описывает неопределённость текущего шага: он true с начала
попытки action до подтверждения checkpoint, включая ожидание postcondition. False
при success не означает, что ранее подтверждённые шаги ничего не сделали. Ни один
вариант доставки не разрешает повтор неподтверждённого действия.

Границы: 1–50 шагов, явный action budget, общий срок 1–300 секунд; каждый шаг
дополнительно ограничен `timeout_ms` локального конфига. Поддерживаются
click/move по свежим label/role, press/hotkey/type. Координаты, старые target_id,
fixed delays, launch, drag и scroll в этом runner пока не поддерживаются.
Каждый шаг требует явный native postcondition. Нет автоматического переключения
приложений, rollback, повторов write и visual fallback. Наблюдение/проверка
происходят через native UI, без скриншотов. Все writes проходят обычный safety gate.

Checkpoint двигается только после подтверждения условия. `resume` разрешён лишь
для `resumable:true`: последний сбой произошёл до попытки ввода, исходный общий
deadline ещё действует и не исчерпан бюджет попыток (число шагов + 3). Любая
попытка action, в том числе отказ native backend, консервативно запрещает повтор
неподтверждённого шага. Завершённые шаги не повторяются. Для ручного нового плана
сначала исследуйте фактическое состояние.

Status/cancel не занимают input lane. Журнал ограничен планом и recovery budget;
содержит только индексы, состояния, время и закрытые коды ошибок. Текст ввода,
подписи UI, заголовки и сам план в отчёт/журнал/диск не попадают. План с typed
text существует в памяти только пока выполнение/безопасное resume ещё возможно.

## Локальная память

При заданном directory появляется `ui_memory_control` с `action:"status"` и
`action:"clear"`. Это первая ограниченная версия памяти: записи об успешно
проверенных семантических локаторах, а не библиотека воспроизводимых планов.
Они сохраняются только после matching explicit native expectation.

На диске — keyed digests структуры/селектора, время и счётчик успешных проверок.
Нет текста UI, process paths, field values, паролей, typed text, изображений и
координат. До 128 записей на namespace; TTL 1–720 часов. Просроченные записи
игнорируются и удаляются при открытии/обращении к cache. Когда процесс не работает,
физическое удаление происходит при следующем открытии, а не отдельным сервисом.
Clear очищает только текущий namespace.

На POSIX directory создаётся с mode 0700, файлы — 0600; проверяются тип, права и
тождественность реально открытого файла. Небезопасный каталог, symlink-файл или
повреждённая схема дают явную ошибку.

Windows использует native SecurityAttributes при создании: owner — process-user
SID, protected DACL, Full Control только этому пользователю и LocalSystem.
Проверяются security descriptor реально открытого handle, owner, allow ACEs,
inheritance flags и тип объекта; NULL/отсутствующий/broad DACL отвергается.
Существующие ACL автоматически не исправляются. Temp-файл приватен до первой
записи; замена — MoveFileEx в том же каталоге без delete-then-rename и copy fallback,
с повторной проверкой ACL результата.

Windows cache требует локального writable volume с persistent ACLs (например,
NTFS/ReFS), обычного drive path и доверенного operator-controlled parent.
UNC/network/device paths, ADS, reparse points и hardlinked data не поддерживаются.
POSIX mode bits не заменяют ACL. Администратор/LocalSystem и программы того же
пользователя не являются отдельной security boundary. Рекомендуемое место — новый
закрытый leaf directory внутри `%LOCALAPPDATA%`, вне junction/OneDrive paths.

Windows native filesystem tests подготовлены и cross-compiled; на текущем macOS
они не выполнялись. Проверка на Windows без GUI-разрешений:

```powershell
go test ./internal/uimemory ./internal/mcpserver -run 'TestWindows|TestPrivateACL|TestLocalReceipts|TestRetention|TestMemoryLearns' -count=1 -v
```

Без `ui_memory_namespace` каждый запуск получает новый namespace. Заданный
оператором namespace позволяет повторно использовать тот же cache между
перезапусками; для изолированных задач выбирайте разные значения. Не используйте
один namespace из нескольких параллельных серверов: cache имеет process-local
mutex и atomic replacement файла, а не межпроцессную транзакцию.

Memory hit учитывается как свидетельство предыдущего проверенного использования;
он **не** пропускает свежий AX read/полную проверку уникальности. Ускорение поиска
этой версией памяти не заявляется. Новое положение элемента меняет digest;
старые координаты никогда не восстанавливаются из cache.

## Метрики и платформы

`session_status.stage_metrics` содержит count/total_ns/max_ns для capture,
controls_read, target_lookup, action, verification и workflow. Этапы могут
перекрываться; это локальные измерения, не model/network latency.
`metrics.action_outcomes` считает action reports по статусам, не реальную частоту
успеха бизнес-задач.

`measure_wire_bytes:true` добавляет `serialized_result_bytes`: JSON
CallToolResult с base64 и выбранным compact/full encoding, без JSON-RPC envelope.
Дополнительная сериализация имеет стоимость, поэтому измерение включается явно.
Обычный `result_content_bytes` сохраняет прежнее значение text + raw image bytes.

`session_status.capabilities` явно сообщает реализованные native интерфейсы.
Supported не означает выданные OS permissions или локальное разрешение.
`native_ui_events:false` означает bounded polling для AX, а не имитацию подписки.
UIA/AT-SPI и Wayland portals этим изменением не добавлены. Проверки mock и
cross-build не считаются интерактивными тестами реального desktop.

## Воспроизводимые проверки

Экономный режим: оставляйте `tool_profile:"compact"`, запрашивайте controls только
для semantic targeting и используйте `result_mode:"auto"` с явным native expect.
Для известных сценариев включайте workflows и ждите результат локально вместо
повторных status/observe. Не запрашивайте recent diagnostics и wire-size profiling
постоянно: они нужны при разборе/измерениях. Изображение необходимо, когда следующее
решение действительно зависит от новой визуальной информации.

Парное измерение start→status и start+wait на одинаковых двух verified шагах:

```sh
go test ./internal/mcpserver -run '^TestWorkflowWaitReducesCallsAndSerializedBytes$' -count=1 -v
go test ./internal/mcpserver -run '^TestWorkflowSummaryReducesBytesWithoutLosingCheckpoints$' -count=1 -v
```

Это сравнение протокольных стратегий на mock, не измерение токенов, стоимости,
квоты Codex или ускорения версий. Меньше MCP-вызовов и повторных данных позволяет
избегать лишних модельных ходов; изображения здесь не требуются. Native/policy
проверки одинаковы в обоих вариантах. Каталог/запросы и transport overhead в
измерение serialized result bytes не входят.

```sh
go test ./internal/mcpserver -run 'TestSemanticTarget|TestTitleChange|TestSignificantVisual|TestActionOutcome|TestOutcomeSurvives|TestWorkflow|TestMemory|TestSerialized' -count=1
go test ./internal/uimemory -count=1
go test ./...
go test -race ./...
go vet ./...
```

macOS, отдельный input opt-in для контролируемого AppKit fixture:

```sh
# Read-only existing integration (32×32 capture, memory only):
CODEX_VISION_NATIVE_INTEGRATION=1 go test ./internal/platform/macos -run '^TestNativeMacReadIntegration$' -count=1 -v
# Opens/clicks ONLY a freshly compiled test fixture, never user documents:
CODEX_VISION_NATIVE_INPUT_INTEGRATION=1 go test ./internal/platform/macos -run '^TestNativeControlledUIIntegration$' -count=1 -v
```

Fixture проверяет замену кнопки при прежних label/bounds, AX document context с
прежним title (если AXDocument доступен) и реальный callback кнопки после native
click. Он имеет отдельный executable allowlist; завершение/таймаут убирают только
его дочерний процесс. Без unlocked GUI/AX/post-event permissions тест пропускается,
без input и попыток изменить TCC. Обычные unit-тесты лишь компилируют fixture и
проверяют Foundation cache/mocks — это не замена интерактивному прогону.

В разблокированной GUI-сессии используйте тестовую форму из `eval/fixtures`:
проверьте уникальный Save, два одноимённых контрола, перестановку цели между
observe/act, смену документа с новым заголовком, cancel во время ожидания native
условия и stop при работающем workflow. Работайте с тестовыми данными; успешный
тест требует фактической проверки результата приложения, а не только MCP status.
