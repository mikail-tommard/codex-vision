# Изменения

Версия работающего сервера доступна в `session_status.server.version`. Эта история описывает возможности исходного кода; наличие записи не означает публикацию готовых бинарников или проверку каждого приложения вживую.

## 0.14.4

- Experimental opt-in Wayland ScreenCast/PipeWire: `screencast_control` start/next/status/stop, explicit duration/frame budgets, system source selection, JPEG delivery only on explicit pulls and exact pixel deduplication. No desktop mapping or input.
- Native portal calls pin owner/request/session, watch Session.Closed and owner loss, use non-persistent single-source consent and stable PipeWire serial targeting. Portal v6+ and installed GStreamer plugins required; no automatic install or node-ID/reconnect fallback.
- Stop/cancel/deadline/frame budget close the stream; revoked sessions discard cached pixels. Pulls are serialized; cleanup failure blocks new capture sessions instead of accumulating child processes.
- Paired MCP mock: 5→2 images, 7770→3108 raw JPEG bytes, example CallToolResult JSON 12585→6380 bytes for A,A,A,B,B. Both strategies use 5 pulls; no real Codex usage/GUI timing measured.
- Added portable manager/MCP/PNG tests and explicit Linux-only native capture integration opt-in. Real compositor, portal consent and GStreamer execution remain unverified here. See `docs/wayland-screencast.md`.

## 0.14.3

- `ui_controls` в compact: revision-based подавление повторного дерева и явный RAM discovery cache 0..2000ms. Input/focus/context/stop инвалидируют кэш; targeting/readiness/verification всегда читают свежие данные.
- UI memory: до 8 успешно проверенных semantic workflow recipes в RAM на 15 минут, повторный запуск по короткому ID с новыми budgets и fresh targets. Без typed text/keyboard; планы не сохраняются на диск. Clear/stop отзывают in-flight learning.
- Workflow wait steps проверяют native condition без ввода и изображений.
- Opt-in Wayland Screenshot portal через D-Bus, interactive consent, bounded requests, отказ/отмена/stop и проверка local image URI. Только single-shot capture; ScreenCast/PipeWire/RemoteDesktop input не реализованы, реальный compositor здесь не проверялся.
- Парные mock-измерения: discovery responses 34320→7908 bytes, tree reads 5→1; повторный workflow request 260→159 bytes. Это не Codex tokens/квота; детали в `docs/economy-0143.md`.

## 0.14.2

- UI memory Windows: native protected owner/SYSTEM DACL при создании directory/temp files, проверка actual-handle ACL/owner и private replacement через MoveFileEx. Broad/inherited/NULL ACL, reparse/hardlinks, ADS и nonlocal/non-ACL volumes отвергаются; существующие права не изменяются.
- Общие store/MCP memory tests больше не пропускаются на Windows; добавлены native descriptor/filesystem tests и portable ACL policy cases. Windows runtime здесь не проверялся, cross-compile не заменяет его.
- workflow detail=summary сокращает повторные успешные journal rows, сохраняя checkpoint, cursor, состояние и последнюю ошибку. Full остаётся прежним, summary несовместим с after_event.
- Running workflow честно отмечает input_may_have_posted во время action/verification; отмена не разрешает replay.
- Парные mock-измерения объёма full/summary включены в tests; точные Codex tokens/квота не измеряются.

## 0.14.1

- macOS AX object identity и window/document context защищают от одинаково выглядящих замен и смены документа с прежним title. Native hit-test выполняется после pointer approach; field write проверяет context и после readback.
- Приватные native IDs/document URL не добавляются в MCP-ответы или persistent locator strategies.
- workflow wait_ms позволяет получить завершение в одном вызове, after_event/event_cursor исключают повторную выдачу журнала. Cancel/stop остаются доступны во время ожидания; status без ID находит последний run.
- Парное измерение mock workflow: 2→1 MCP-вызов, примерно 40% меньше serialized JSON, 0 изображений; это не измерение токенов или квоты Codex.
- Добавлен opt-in контролируемый AppKit GUI fixture; отсутствие GUI/permissions явно означает skip. Windows/Linux backend contracts сохранены.

## 0.14.0

- Семантические target label/role из наблюдённого дерева: точное уникальное совпадение и свежая проверка перед каждым действием.
- Защита view при смене заголовка, значимом visual revision и отдельном MCP-вводе; observe/input сериализуются.
- Добавочные outcome success/failed/timeout/ambiguous/needs_confirmation с явным scope доказательств.
- Opt-in workflow_control: native postconditions, checkpoint, deadline/action budget, cancel и ограниченный pre-input resume без повторов неподтверждённых действий.
- Opt-in локальные хешированные записи о проверенных локаторах: namespace, TTL, bounded storage и clear; Windows storage явно unsupported до ACL adapter.
- Метрики этапов, opt-in serialized result bytes и явные backend capabilities. Native UIA/AT-SPI/events/Wayland не добавлены.
- Документация и ограничения: docs/autonomy.md. Default flags сохраняют прежний каталог инструментов и локальную политику.

## 0.13.6

- Windows window_focus: после восстановления перекрытое окно один раз поднимается штатным BringWindowToTop. Если это уже активировало точный HWND, SetForegroundWindow не вызывается; иначе отправляется одна попытка активации. Desktop/PID/ID перепроверяются между этапами. Диагностика содержит результат подъёма. Ограничения Windows и recovery guard сохранены; успех API не заменяет проверку foreground.
- desktop_act/desktop_batch: delay_before_ms позволяет объединить известную пару меню (атака → подтверждение, смена → выбор) в один вызов с короткой локальной паузой. 0..1000 мс на шаг, до 2000 мс суммарно; по умолчанию 0. После паузы заново проверяются окно, процесс, геометрия и ссылки контролов. Пауза не доказывает готовность меню. Один итоговый результат, без промежуточных кадров модели.
- Неверные паузы отклоняются до refocus/потребления view. При отмене или потере фокуса после первого действия сохраняется частичное выполнение и запрет повтора.
- Синтетические тесты проверяют порядок подъёма/активации, отмену, stale ID и пару кликов с задержкой. Фактическое переключение Minecraft и экономия времени боя на Windows требуют проверки в GUI-сессии.

## 0.13.5

- Windows: захват через DDB/CreateCompatibleBitmap + GetDIBits заменён на top-down 32-bpp CreateDIBSection. BitBlt завершается через GdiFlush на закреплённом OS thread, затем BGRX копируется в собственный непрозрачный RGBA-буфер до освобождения GDI-объекта.
- Устранён путь ошибки GetDIBits incomplete с вводящим в заблуждение текстом ERROR_SUCCESS. Отказы создания/копирования/синхронизации по-прежнему не возвращают изображение. Проверки interactive desktop и границ экрана сохранены; это не исправление window_focus.
- Переносимые тесты проверяют цвета, порядок строк, нечётную ширину, независимость памяти, отказ неполного буфера и отмену. Windows ABI-проверка включает BITMAPINFO. Реальный desktop-capture на Windows требует отдельного запуска opt-in integration test.

## 0.13.4

- desktop_observe без нового изображения сохраняет image-координаты предыдущего доставленного кадра только при неизменных окне, заголовке, геометрии, области, observation ID, focus version и sequence. Просроченный/потреблённый view и новый недоставленный кадр не наследуются. В ответе указан image_sequence; дополнительный кадр не отправляется.
- Короткие инструкции различают вкладку Terminal и нативное окно; рекомендуют пакет проверенного текста с ENTER и одну проверку вывода вместо кадров после каждого фрагмента.
- В схеме шага type явно описаны ограничения 4096 Unicode code points и 16384 UTF-8 bytes на пакет. Автоматического разбиения, отправки команд или обхода отказов approval нет.
- Регрессионные тесты проверяют сохранение координат без второго изображения и отказ наследования при смене контекста. Экономия подписочного лимита и работа с реальными SSH-вкладками этими тестами не измеряются.

## 0.13.3

- macOS: активное AX-окно с одинаковыми границами нескольких Quartz-окон дополнительно сопоставляется по уникальному заголовку, с повторной проверкой стабильности фокуса. Неизвестный/повторяющийся заголовок сохраняет отказ.
- window_focus заново сопоставляет точный Quartz ID после единственной активации фонового приложения; первоначальная неоднозначность больше не запрещает эту разрешённую активацию. Read-side polling ограничен, клавиши/клики не используются.
- Повторяющиеся ссылки на один AX-элемент не считаются разными окнами. Проверяются владелец AX-окна и свежие границы перед AXRaise; итоговый native window ID по-прежнему обязателен.
- Диагностика include_recent различает неоднозначность окон и сохраняет только численные счётчики совпадений и AX error code, без заголовков.

## 0.13.2

- Протокол для выбранного приложения: window_list → одна разрешённая активация → desktop_observe, без лишнего чтения активного окна между подтверждёнными действиями.
- Для вкладок рекомендованы наблюдаемые UI-ссылки и явная проверка известного заголовка; восстановительные stop/start-циклы и повтор неподтверждённого клика запрещены инструкциями. Успех нельзя заявлять только по отправке события.
- session_status(include_recent=true): последние 12 обработанных сервером вызовов с категорией ошибки, native error code, стадией ввода и локальными временами. История только в памяти, без аргументов, текста UI и кадров; обычные ответы не увеличены историей.
- Continuation явно сообщает, что сбрасывать наблюдение и повторять ввод нельзя; тайм-аут без кадра требует только проверки состояния.

## 0.13.1

- desktop_act проверяет параметры ожидания и режимы до focus/работы с view; view потребляется только перед первой попыткой ввода после batch preflight.
- Неверные параметры до ввода больше не уничтожают действующий view. После попытки ввода, тайм-аута или частичного выполнения старый view по-прежнему нельзя повторять.
- Схемы desktop_act/desktop_batch явно задают settle_ms 100..2000 и wait_ms 100..30000; 0/отсутствие сохраняют прежние значения по умолчанию.

## 0.13.0

- Linux X11: клики, drag, клавиши, сочетания, Unicode-текст по stdin и позиция курсора; контроль окна и best-effort отпускание ввода после отмены/ошибки.
- Проверка реальной caller-сессии logind вместо доверия одному XDG_SESSION_ID. Пустой Display принимается только при подтверждённых UID, локальном активном разблокированном X11 и совпадающем ActiveSession seat. Заполненный несовпадающий Display по-прежнему запрещён; :0 и :0.0 сопоставляются.
- --doctor проверяет сессию Linux и показывает метод привязки, а не только наличие мониторов. Добавлен отдельный пример assist-конфига Linux и инструкции Ubuntu/Xorg.
- Policy привязывает клавиатуру и прокрутку к проверенному окну, как и мышь. Wayland portal-backend остаётся нереализованным.

## 0.12.0

- Compact по умолчанию: 12 инструментов вместо 29; `tool_profile:full` или `--tool-profile full` сохраняет прежний каталог и формат ответов.
- Compact передаёт метаданные только JSON-текстом, без второй копии structuredContent.
- UI-контролы в unified view читаются по запросу include_controls; выбор сохраняется после действий. Проверки окна, разрешений, target_id и явных UI-условий сохранены.
- Сокращены общие инструкции; исправлена рекомендация ожидания: change для собственного результата (включая 2048), response только для двух визуальных фаз.

## 0.11.0

- Единый desktop_observe: активное окно, scoped image, UI-контролы и опциональные поля.
- desktop_act: краткоживущие view/target ссылки, повторная проверка контрола перед шагом, image-coordinate mapping и одна разрешённая preflight focus-попытка.
- Явные native UI-postconditions для пакетов и передача следующего view в успешном результате.
- Структурированные подсказки ошибок, process-local метрики и более короткие общие инструкции.
- Парные synthetic workflow evaluations, локальная HTML-страница и отдельный план настоящих GUI-проверок.

## 0.10.3

- Windows window_focus восстанавливает свёрнутое окно через ShowWindowAsync и ожидает фактического foreground до 2 секунд.
- Успех подтверждается точным HWND, а не только BOOL SetForegroundWindow или мгновенной проверкой.
- При ожидании повторно проверяются PID/ID, interactive desktop и отмена. Запрос активации отправляется один раз; таймаут содержит диагностические признаки вместо безусловного утверждения о запрете Windows.

## 0.10.2

- Прокрутка macOS проверяет точное активное окно и использует AX hit-test при неоднозначном Quartz-перекрытии.
- Чужое окно, неподтверждённая цель, выход курсора за границы и смена фокуса/курсора по-прежнему блокируют событие.
- Ошибка вне окна сообщает координаты курсора и границы для диагностики.

## 0.10.1

- Поддержка подтверждения запуска установленного FreeCAD с отдельным GUI-runtime.
- Документация точных executable-путей для локального допуска FreeCAD.

## 0.10.0

- Двухфазное визуальное ожидание `desktop_batch.wait_for=response`.
- Текстовый результат подтверждённой AX-готовности в `result_mode=auto`.
- Подсказки `continuation` для использования уже полученного результата.
- Таймаут `mouse_action_wait` без изображения по умолчанию.

## 0.9.x

- Диагностика причин фильтрации и выдачи кадров.
- Более точное сопоставление AX/Quartz-окон и информация о версии процесса.
- Исправление JSON-сериализации нативных логических флагов.

## 0.8.0 и ранее

- AX-контролы и циклы готовности, отдельный размер изображений для модели.
- Быстрый подход курсора и измерения локальных задержек.
- Пакеты desktop-действий, локальное ожидание, области внимания.
- Фильтр устойчивых изменений, preview и режимы наблюдения.
- Native desktop backend, локальная политика доступа и stdio MCP.
