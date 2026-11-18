# Что осталось завершить после 0.14.4

Это рабочий backlog. Полный журнал реализованного и границы проверок:
[IMPROVEMENT_ROADMAP.md](IMPROVEMENT_ROADMAP.md). Форматы API и opt-in flags:
[autonomy.md](autonomy.md).

## P0 — подтверждение на реальных платформах

- [ ] Windows: выполнить native filesystem ACL tests. Сейчас они только
  cross-compiled; portable ACL policy tests прошли на macOS.
- [ ] macOS: выполнить controlled AppKit GUI test в unlocked Quartz session с
  Accessibility/post-event permissions для фактического launch identity.
  Здесь native read/input tests пропущены из-за отсутствия сессии/разрешений.
- [ ] Windows/Linux X11: реальные capture/input/focus/stop, несколько мониторов,
  DPI/отрицательные координаты и блокировка/разблокировка desktop.

Windows без GUI-действий:

```powershell
go test ./internal/uimemory ./internal/mcpserver -run 'TestWindows|TestPrivateACL|TestLocalReceipts|TestRetention|TestMemoryLearns' -count=1 -v
go test ./...
go vet ./...
```

macOS, только собственный controlled fixture, без пользовательских документов:

```sh
CODEX_VISION_NATIVE_INPUT_INTEGRATION=1 go test ./internal/platform/macos -run '^TestNativeControlledUIIntegration$' -count=1 -v
```

Готовность: реальные результаты и ограничения записаны отдельно от mock/build;
skip не объявляется успешным GUI-прогоном. Разрешения/TCC не обходятся.

## P1 — native semantic readers и события

- [ ] Windows UIA: bounded discovery, native identity, window/context binding,
  enabled/secure states, явная ошибка при недоступных данных.
- [ ] Linux AT-SPI: тот же контракт без shell-based угадывания UI.
- [ ] AX/UIA/AT-SPI events для targeted invalidation/waiting, с bounded polling
  только как явно обозначенным fallback.
- [ ] Проверить remaining races: одинаковый title/URL у разных логических
  документов/вкладок, переиспользование provider identity, overlays и custom UI.

Готовность: mock contracts + platform tests; stale/ambiguous/unknown не разрешают
write. Native IDs и document URLs не увеличивают агентские MCP-ответы.

## P1 — производительность и расход контекста

- [x] Ограниченный discovery cache с инвалидацией при input/focus/context/stop
  и revisions без повторной выдачи controls; fresh write authorization сохранён.
- [ ] Native UI events для более ранней инвалидации cache; пока TTL 0..2000ms
  включается явно, внешние изменения внутри того же context могут ждать TTL.
- [ ] Сокращение full-screen native capture там, где достаточно выбранного region;
  измерить качество текста при adaptive resolution/compression.
- [ ] Парные реальные сценарии: latency, captures, MCP calls, images и serialized
  response bytes. Отдельно наблюдать usage клиента Codex, если оно доступно.
- [ ] Regression budgets для catalogue size, повторных metadata/journal и изображений.

Уже доступно: compact profile, wait_ms, after_event, detail=summary, discovery
revisions/cache и RAM recipe_id. Измерения/границы: [economy-0143.md](economy-0143.md). Не менять
безопасность ради меньшего payload. Байты/MCP-вызовы не выдавать за точные токены,
проценты лимита или стоимость Codex.

## P2 — полноценная UI memory

- [x] Session-only reusable semantic workflow templates в RAM: до 8 verified
  plans, TTL 15min, clear/stop, без typed text/keyboard, fresh targets при запуске.
- [ ] Reusable UI structures/locator strategies и безопасные disk templates;
  сейчас disk хранит только hashed verified-locator receipts, recipes — только RAM.
- [ ] Измерить реальное ускорение повторных операций; сейчас оно не заявляется.
- [ ] Продумать физическое удаление expired data без обращения к cache; сейчас
  TTL pruning выполняется при open/access.
- [ ] При поддержке совместного namespace добавить межпроцессную координацию;
  сейчас один namespace нельзя одновременно использовать нескольким серверам.

Готовность: opt-in, session isolation, TTL/purge, отсутствие typed values,
паролей/токенов/screenshots и слепого восстановления старых координат.

## P2 — расширение workflow

- [x] Wait steps для native postconditions без ввода и screenshots.
- [ ] App launch, несколько окон, scroll/drag с отдельными проверками целей.
- [ ] Безопасные переменные/шаблоны и восстановление checkpoint после restart.
- [ ] Расширить observable postconditions без произвольного кода и чтения secure
  values; business success не подменять event posting/сменой title.

Готовность: конечные deadline/action/recovery budgets, stop/cancel, закрытые
нечувствительные журналы. Не повторять неопределённые или необратимые actions.

## P3 — Wayland

- [x] Opt-in single-shot Screenshot portal с interactive consent, bounded URI/image,
  cancel/timeout/stop, отдельным compact-каталогом и отсутствием desktop mapping.
- [ ] Реально проверить consent/cancel/stop на Wayland/compositor; пока mock tests
  и cross-build, а native Linux-specific tests требуют Linux runtime.
- [x] Experimental ScreenCast/PipeWire path через отдельный opt-in, portal v6,
  stable serial и установленный GStreamer. Один source, duration/frame limits,
  явные pulls/dedup, revocation/stop/failed-cleanup handling. Без input/mapping.
- [ ] Выполнить `TestNativeScreenCastIntegration` на Linux с реальным portal;
  проверить Cancel, revocation, node loss/hotplug и отсутствие child после stop.
  Здесь прошли mocks/framing; native protocol только cross-compiled/vetted.
- [ ] RemoteDesktop portal/input с отдельным согласием и доказуемым mapping;
  capture-согласие не считать разрешением на ввод.
- [ ] Проверить практическую доступность portal v6/plugins и измерить CPU/RSS,
  latency/читаемость текста; не ослаблять serial binding ради старого node ID.
- [ ] Capability/error/session/cancellation tests; XWayland не считать полной
  поддержкой Wayland. Для ещё не реализованных функций возвращать unsupported.

## С чего продолжить разработку

1. Выполнить доступные P0 проверки, не заявляя отсутствующее окружение проверенным.
2. Реализовать Windows UIA discovery отдельным небольшим изменением.
3. Выполнить tests/race/vet и relevant builds, обновить этот checklist и roadmap.
4. Для приоритетов economy/memory/Wayland: реальные парные сценарии, UI events,
   privacy-safe locator strategies, live validation ScreenCast/PipeWire, затем
   RemoteDesktop. Порядок/команды: [wayland-screencast.md](wayland-screencast.md).
5. Держать full compatibility; новые тяжёлые/системные возможности включать явно.

Локальные конфиги, их backup, audit/state, preview URLs, screenshots и binaries
не коммитить. Последняя реализация: 0.14.4; открытые пункты выше не выполнены,
наличие roadmap не означает их выполнение.
