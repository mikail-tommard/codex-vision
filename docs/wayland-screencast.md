# Wayland ScreenCast: experimental read-only stream, 0.14.4

Реализован отдельный opt-in путь **ScreenCast portal → PipeWire FD → локальный
GStreamer → bounded PNG reader → MCP JPEG**. Это не RemoteDesktop input и не
координатный backend. Реальная Wayland-сессия здесь ещё не проверена.

## Включение

Нужны локальная Wayland-сессия, session D-Bus, ScreenCast portal **v6+** и
установленный `gst-launch-1.0` с `pipewiresrc`, `videorate`, `videoscale`,
`videoconvert`, `pngenc`, `queue` и `fdsink`. У pipewiresrc должны быть свойства
`fd`, `target-object`, `dont-reconnect`, `max-buffers`.
Сервер ничего не устанавливает и не запускает bus/compositor автоматически.

```sh
CODEX_VISION_WAYLAND_PORTAL=1 CODEX_VISION_WAYLAND_STREAM=1 \
  ./bin/codex-vision-linux-amd64 --config /absolute/path/config.wayland.local.json
```

Это переменные окружения **процесса сервера**, не MCP-параметры. Локальный config
должен разрешать `images:true`; для человеческого ответа порталу разумен
`timeout_ms:30000`. Observe mode достаточен, input permissions не нужны.
Без STREAM остаётся прежний single-shot Screenshot portal. Без PORTAL Wayland
по-прежнему отвергается. X11/macOS/Windows этим флагом не изменяются.

Compact-каталог этого backend содержит четыре инструмента: `screen_capture`,
`screencast_control`, `session_status`, `session_stop`. Capability
`portal_screencast:true` означает реализованный интерфейс, не подтверждённую
доступность портала/plugins/consent. `continuous_observation:false` относится к
прежнему desktop observation API, который по-прежнему unsupported.

## MCP

Один запуск с явными пределами, включая время согласия:

```json
{"action":"start","duration_sec":60,"max_frames":120,"wait_ms":25000}
```

Портал предлагает пользователю **один** монитор или окно. Персистентность
выключена (`persist_mode:0`), restore tokens не используются. На весь setup —
до 30 секунд; общий deadline 5..300 секунд начинается при start. Max_frames
2..600 считает все локальные кадры, включая дубли. В каждый момент возможен
один поток; повторного согласия, выбора источника и reconnect сервер не делает.

`start` возвращает `stream.stream_id` и состояние `starting`/`running` либо
терминальный результат. При `wait_ms` он может дождаться первого изображения.
Далее передавайте **последний полученный frame.sequence**, а не предполагаемый
номер следующего кадра:

```json
{"action":"next","stream_id":"returned-id","after_sequence":1,"wait_ms":25000}
```

Данные передаются только явным вызовом. Локальный поток не вызывает модель.
Прежние пиксели дают `changed:false`, без изображения. Новый кадр содержит
`frame.sequence`, `captured_at`, `width`, `height`, `mime`. `captured_at` — время
получения локального кадра сервером, не timestamp compositor. Zero cursor явно
повторно запрашивает текущий контекст; future/чужой cursor отвергается.
Повторные запросы на кодирование сериализованы и учитываются в read budgets.

```json
{"action":"status","stream_id":"returned-id"}
```

Status без ID находит последнюю сессию. Отчёт содержит deadline, sequence,
sampled_frames, duplicate_frames, images_returned, max_frames и стабильный code.
UI-текст, native session/node IDs, serial, FD и диагностический stderr не
возвращаются. Отчёт доступен также в `session_status.screencast` после stop.

Остановить поток по завершении задачи:

```json
{"action":"stop","stream_id":"returned-id"}
```

Stop доступен при исчерпанном read budget и pending consent/long poll. Он
отменяет захват; при ещё не завершившейся очистке новый start отвергается.
Отмена MCP start до завершения ответа также останавливает начатый поток;
отмена next прекращает только этот pull. `setup_timeout` обозначает timeout
portal setup, `deadline` — исчерпание общего срока выполнения.
`session_stop` дополнительно latch-останавливает весь серверный доступ до
локального restart. Stop-file проверяется и во время системного запроса.

## Границы реализации

- `starting`, `running`, `stopped`, `timeout`, `limit_reached`, `cancelled`,
  `failed` описывают **захват**, не успех действий приложения.
- Session.Closed, потеря owner портала и завершение consumer закрывают source;
  отозванные/терминальные сессии не отдают сохранённые пиксели. Передача уже
  отправленных клиенту данных не может быть отменена задним числом.
- Привязка использует `pipewire-serial`/`target-object`; fallback к переиспользуемому
  node ID и reconnect запрещены. Поэтому portal <6 явно unsupported.
- Потребитель получает только FD из OpenPipeWireRemote, не обычное системное
  подключение PipeWire. Ни shell, ни произвольный pipeline от клиента не нужны.
- Локальный pipeline ограничен 2 fps, очередью в один кадр, до четырёх native
  buffers и шириной 1280 перед PNG. Decoder проверяет IHDR до allocation:
  максимум 32M pixels, 64MiB PNG и 4096 chunks. Это не лимит RSS всего GStreamer.
- В Go остаётся последний кадр. Сравнение exact pixel hash подавляет дубли,
  но часы/анимации могут давать изменения, а промежуточные состояния coalesce
  могут пропасть. Совпадение пикселей не доказывает semantic readiness.
- MCP по умолчанию JPEG quality 75, максимум 768px. Для деталей используйте
  отдельный `screen_capture` с PNG/max_width; читаемость текста в реальных
  приложениях пока не измерена. Настройки stream encoding пока фиксированы.
- Ни desktop mapping, ни monitor geometry, ни window identity, ни input не
  добавлены. Эти кадры нельзя передавать как координатную основу `desktop_act`.
- Кадры не сохраняются на диск, в UI memory или audit. Ошибки consumer дают
  обезличенный code. OS paging, GPU buffers и crash dumps вне этой гарантии.
- Cleanup отменяет consumer, закрывает FD/session и ожидает child exit с
  конечным пределом. При `cleanup_failed` новый start запрещён до локального
  разбора и restart; ошибка не выдаётся за успешную очистку.

## Воспроизводимые проверки

На macOS выполняются mocks и PNG framing, без portal capture:

```sh
go test -race ./internal/screencast ./internal/mcpserver -run 'TestStream|TestScreenCast' -count=1 -v
go test ./internal/platform/wayland -run '^TestNativePNG' -count=1 -v
```

Парный MCP mock-прогон использует одинаковые пять кадров `A,A,A,B,B`, compact
JSON, 5 pulls в обоих вариантах. Пример результата:

| Метрика | 5 single-shot calls | Один stream + 4 next |
| --- | ---: | ---: |
| MCP images | 5 | 2 |
| Raw JPEG bytes | 7770 | 3108 |
| CallToolResult JSON bytes | 12585 | 6380 |
| Single-shot requests / stream sessions | 5 / 0 | 0 / 1 |

Stop, catalogue, requests и transport overhead не включены в result bytes;
metadata timestamps могут слегка менять длину. Это **не** реальные Codex tokens,
квота, GPU/CPU latency, работа consent UI или сравнение скоростей compositor.
Количество MCP pulls не сократилось. GStreamer и настоящий портал в этом
измерении не запускались.

В Linux сначала обычные unit tests; native GUI opt-in проверяет живой путь,
первый кадр и закрытие, без GUI input и без сохранения пикселей:

```sh
go test ./internal/platform/wayland -count=1 -v
# In an unlocked Wayland session: choose only a disposable test window.
CODEX_VISION_WAYLAND_STREAM_INTEGRATION=1 go test ./internal/platform/wayland \
  -run '^TestNativeScreenCastIntegration$' -count=1 -v -timeout 45s
```

Нативный opt-in здесь не запускался. Дополнительно вручную проверить Cancel
во время Start, stop-file, Session.Closed через системный sharing UI, закрытие
выбранного окна, смену разрешения, исчерпание budget и отсутствие consumer после
stop. Отдельно записать compositor, portal/plugin versions и фактический результат.

Фактически на macOS amd64 прошли `go test ./...`, `go test -race ./...`,
`go vet ./...` и дополнительные race-прогоны актуальных stream/MCP tests,
включая отзыв/cancel во время кодирования. Native Linux source/tests собираются
без CGO и проходят Linux vet; cross-compilation не считается запуском portal.

Контракты: [ScreenCast](https://flatpak.github.io/xdg-desktop-portal/docs/doc-org.freedesktop.portal.ScreenCast.html),
[Session](https://flatpak.github.io/xdg-desktop-portal/docs/doc-org.freedesktop.portal.Session.html),
[pipewiresrc source](https://github.com/PipeWire/pipewire/blob/master/src/gst/gstpipewiresrc.c),
[videorate](https://gstreamer.freedesktop.org/documentation/videorate/index.html).
