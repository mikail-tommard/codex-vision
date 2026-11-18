# Экономия контекста, UI memory и workflow в 0.14.3

## Discovery без изображений

`ui_controls` теперь доступен и в compact-профиле на backend с ControlReader.
Без поддержки native controls он не увеличивает compact-каталог:

```json
{"window_id":"observed-id"}
```

Первый ответ содержит `controls`, `revision`, `changed:true`. Для последующих
чтений передайте полученный `since_revision`: если данные прежние, ответ содержит
`changed:false` и **не содержит** повторного `controls`. Это не пустое дерево.
Неизвестная ревизия возвращает полный список. Смена max_nodes/compact требует
нового списка; ревизия не является разрешением на ввод.

По умолчанию **каждый запрос читает дерево заново**. Для некритичного discovery
можно явно добавить `max_age_ms:250` (0..2000): `cached:true` и `age_ms` сообщают
возраст. Кэш один, только в RAM; foreground/process/bounds/title проверяются
при каждом запросе, native UI context — если backend его поддерживает.
Input attempts, ошибки фокуса/permissions, смена context и stop инвалидируют кэш.
Без нативных UI events внешнее изменение внутри того же документа может быть
незаметно до TTL: **для актуального чтения используйте max_age_ms:0**.

Target resolution, uniqueness, readiness и verification **не читают этот кэш**.
Данные discovery нельзя использовать как подтверждение актуального результата.
Идентификаторы нативных объектов и document context не уходят агенту.

## Проверенные сценарии по короткому ID

При локально включённых `workflows:true` и UI memory можно явно запомнить план:

```json
{
  "action":"start", "window_id":"observed-id",
  "max_actions":1, "timeout_ms":5000, "wait_ms":4000, "detail":"summary",
  "remember":true,
  "steps":[{
    "step":{"action":"click","target":{"label":"Open","role":"AXButton"}},
    "expect":{"control_label":"Document","control_role":"AXGroup"}
  }]
}
```

Только успешное завершение всех postconditions возвращает `recipe_id`.
Для повторного запуска передайте `action:"start",recipe_id,window_id,max_actions,
timeout_ms`; `steps` и `remember` при этом запрещены. Права, fresh targeting,
postconditions, deadline и ограниченные попытки остаются прежними. Новая сессия,
другое приложение, expiry или clear отвергают ID. Координаты не сохраняются.
Повторный запуск всегда явный: это не разрешение автоматически повторять действия.

Шаблоны содержат только semantic click/move и wait, до 50 шагов/32 KiB.
Typed text, keyboard keys/hotkeys и посторонние текстовые параметры запрещены.
До 8 шаблонов хранятся **только в RAM текущего сервера** 15 минут; expiry timer,
clear и stop освобождают их. UI-текст может быть чувствительным, поэтому планы
не попадают на диск, в журнал или отчёт. Disk memory по-прежнему хранит только
hashed receipts. Clear также отзывает обучение уже выполняющегося workflow.

## Шаг ожидания

```json
{
  "step":{"action":"wait"},
  "expect":{"control_label":"Ready","enabled":true}
}
```

Ожидание проверяет native predicate локально, без скриншотов и input event.
Уже выполненное условие допустимо; `require_transition` для wait не применяется.
Wait занимает один шаг бюджета. Timeout до ввода допускает ограниченный resume
в пределах исходного deadline. Stop/cancel остаются доступны. Polling bounded,
нативных event subscriptions пока нет.

## Измерения и повторение

Парные synthetic tests на macOS amd64, Go 1.26.5, compact wire:

| Сценарий | До | После |
| --- | ---: | ---: |
| 5 discovery calls, 100 controls: суммарные CallToolResult JSON bytes | 34320 | 7908 |
| Чтения дерева в том же сценарии | 5 | 1 |
| JSON arguments повторного однокнопочного workflow | 260 | 159 |

Discovery сравнивает fresh/full с revision + явно включённым кэшем. Обе стратегии
делают 5 MCP-вызовов, возвращают 0 изображений; чтения native context не включены
в счётчик чтений дерева. Размеры включают фиксированные тестовые labels; реальные
данные отличаются. Recipe сравнивает повторную передачу плана с `recipe_id`.
Первичное обучение, каталог, transport overhead и latency не включены. Это
**не измерение токенов, подписочного лимита Codex или ускорения реального GUI**.

```sh
go test ./internal/mcpserver -run 'TestDiscoveryRevisionAndCachePairedBytes|TestVerifiedRecipeReusesPlanWithFreshTargetsAndSmallerRequest' -count=1 -v
go test ./internal/mcpserver -run '^TestCompactPayloadBudget$' -count=1 -v
```

По-прежнему используйте `wait_ms`, `detail:"summary"`, journal cursors и native
expectations. Изображение запрашивайте, когда новое визуальное состояние нужно
для следующего решения. Wayland single-shot имеет отдельные ограничения:
[platforms.md](platforms.md#wayland).

Проверки реализации на macOS amd64: `go test ./...`, `go test -race ./...`,
`go vet ./...`; целевые cache/recipe/portal tests также прошли под race detector.
Сборки CLI: macOS amd64, Linux amd64, Windows amd64/arm64. Linux-specific
portal/CLI tests cross-compiled, Linux vet прошёл; эти binaries не запускались
на macOS. Работа настоящего Wayland portal/compositor, consent UI и Windows GUI
этими проверками не подтверждается.
