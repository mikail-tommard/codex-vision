# Accessibility: диагностика конкретного MCP-процесса

`keyboard requires unlocked console and Accessibility permission` была общей ошибкой двух разных проверок. Она не доказывала, что экран заблокирован. Вызов `AXIsProcessTrusted()` был нативным и корректным: Boolean возвращался Apple API внутри Objective-C, а не вызывался по неверной Go/FFI-сигнатуре. Проблема реализации — объединенная причина отказа и недостаток сведений о том, **какой процесс** проверял доступ.

Теперь cgo получает нормализованный `int` 0/1 из нативных API. Диагностика и ввод используют актуальные ответы ОС; они не сохраняются в singleton, `sync.Once` или конфигурации. OS-внутреннее поведение TCC не переопределяется: если после выдачи доступа API продолжает отвечать false, перезапустите именно сервер и его приложение-хост и проверьте повторно.

## Правильная последовательность

1. Перезапустите Codex/MCP после замены бинарника. Проверьте версию через `session_status.server` и наличие `macos_permission_diagnostics`. Уже запущенный процесс продолжает исполнять старый образ даже при замене файла на диске.
2. Вызовите инструмент **через тот же MCP-клиент**, где не работает ввод:

   ```json
   {}
   ```

   Проверьте `pid`, `executable_path`, `parent_pid`, `process_chain`, `accessibility_permission`, `post_event_permission`, `console_unlocked`, `input_available`, `blocking_reasons`.
3. Запросите Accessibility из **этого же MCP-процесса**:

   ```json
   {"request_accessibility": true}
   ```

   Используется `AXIsProcessTrustedWithOptions` с `kAXTrustedCheckOptionPrompt` = настоящий CFBoolean true. Запрос асинхронный: `accessibility_prompt_requested:true` означает вызов API, а не показ окна и не выдачу доступа. Вызов не снимает защиту ОС и не меняет TCC-базу.
4. В macOS откройте **System Settings → Privacy & Security → Accessibility / Универсальный доступ** и разрешите доступ **приложению, которое macOS указала в запросе**. При необходимости добавьте его кнопкой `+`. Если macOS показывает сам CLI-сервер, его точный путь:

   ```text
   /absolute/path/codex-vision/bin/codex-vision-darwin-arm64
   ```

   Через Cmd+Shift+G в диалоге выбора можно перейти к этому пути. Если macOS указывает Codex/IDE/другое приложение-хост, разрешайте его, а не автоматически Terminal. Для Apple Silicon путь к arm64-бинарнику может отличаться: используйте значение из диагностики, а не копируйте путь вслепую.
5. Повторите `{}` через MCP. Ожидаемые OS-предусловия клавиатуры: `accessibility_permission:true`, `post_event_permission:true`, `console_unlocked:true`, `input_available:true`. Это еще **не доказательство доставки клавиши**: `input_delivery_verified` остается false. Локальный режим, stop, allowlist, координаты и бюджеты дополнительно могут запретить действие.
6. Для реальной проверки откройте **новый пустой документ TextEdit**, вручную установите в нем фокус и поручите MCP ввести только `Codex Vision test`. Убедитесь, что текст появился в нужном документе. Затем отдельно проверьте безопасную клавишу/сочетание. Не тестируйте Enter/горячие клавиши в терминале, форме отправки сообщения или документе с несохраненной работой. Здесь такой реальный тест не выполнен: текущему процессу недоступна Quartz GUI-сессия.

Разрешение Terminal относится к его контексту запуска. `--doctor`, запущенный из Terminal, не заменяет диагностику сервера, который запускает Codex/IDE/daemon. В проверенном контексте разработки цепочка выглядела как `codex-vision → codex app-server-daemon → codex app-server-daemon → launchd`; Terminal в ней отсутствовал. Это описание наблюдаемого запуска, а не доказанная TCC-attribution на вашем рабочем столе.

## Значения и причины

| Поле / код | Значение |
| --- | --- |
| `accessibility_permission` | Свежий `AXIsProcessTrusted()` для PID сервера |
| `post_event_permission` | Свежий `CGPreflightPostEventAccess()`; разрешение синтеза событий проверяется отдельно |
| `console_unlocked` | GUI dictionary существует; console/login flags валидны и true; session UID совпадает с effective UID; lock indicator не сообщает блокировку |
| `input_available` | Предусловия ОС для отправки событий выполнены; фактическая доставка не проверена |
| `console_session_unavailable` | `CGSessionCopyCurrentDictionary()` вернул NULL; недоступна Quartz GUI-сессия/WindowServer. Это не утверждение, что физический Mac заблокирован |
| `console_state_unknown`, `console_not_active` | Неизвестный on-console flag или процесс не на активной консоли |
| `console_login_state_unknown`, `console_login_incomplete` | Неизвестно, завершен ли GUI-login, либо он не завершен |
| `console_user_unknown`, `console_user_mismatch` | Не удалось установить session UID либо он не совпадает с effective UID |
| `console_lock_state_unknown`, `console_locked` | Некорректный lock indicator либо ОС сообщает блокировку |
| `accessibility_permission_denied` | Accessibility для контекста этого процесса не разрешен |
| `event_post_permission_denied` | ОС не разрешает этому процессу синтезировать события |

Все найденные независимые препятствия перечисляются в `blocking_reasons`; `blocking_reason` — первое препятствие. Неизвестные session-поля возвращаются как JSON null, а не как выдуманный false. `CGSSessionScreenIsLocked` — необязательный индикатор WindowServer, обычно отсутствующий в разблокированной сессии; `lock_indicator_present` показывает его наличие. Это не публично гарантированный Apple API блокировки; остальная проверка использует опубликованные console/login/user keys, а итог не является защитой от вмешательства другого кода того же пользователя. Некорректные типы или неизвестные обязательные поля приводят к отказу.

Для фокуса через AX достаточно корректной консоли и Accessibility; отдельный PostEvent требуется для клавиатуры/мыши. Screen Recording проверяется независимо и не объявляется обязательным для клавиатуры.

## Что известно об ответственном TCC-процессе

Диагностика выводит реально наблюдаемые PID/пути и до 16 уровней родительской цепочки без argv, environment или секретов. **Родитель не обязательно является TCC-responsible identity.** У TCC нет поддерживаемого публичного API такого lookup, поэтому `tcc_attribution.verified:false` и `responsible_process:null` намеренны. Точное имя определяйте по системному запросу/Settings или TCC-логу именно этого запуска. Не выдавайте первый `.app` в списке родителей за установленный факт.

При необходимости можно вручную наблюдать локальные TCC-сообщения, отфильтрованные по PID из диагностики:

```sh
/usr/bin/log stream --info --style compact --predicate 'subsystem == "com.apple.TCC" AND eventMessage CONTAINS "pid=YOUR_MCP_PID"'
```

Подставьте число вместо `YOUR_MCP_PID` и повторите запрос Accessibility через MCP. Формат логов зависит от macOS, сведения могут быть скрыты или недоступны; пустой/закрытый лог не подтверждает attribution. Сервер сам не читает TCC.db, не вызывает `tccutil reset`, не меняет SIP/permissions и не повышает привилегии. Недоступные процессы в цепочке отмечаются пустым путем/`parent_pid_known:false`, а не подменяются догадкой.

Пересборка неподписанного/ad-hoc бинарника может изменить его code identity. Используйте стабильный путь; после обновления проверьте доступ снова. Подписанный релиз с устойчивой идентичностью — отдельная задача, а не повод редактировать TCC или выдать себе доступ.

Источники: [AXIsProcessTrustedWithOptions — prompt асинхронен](https://developer.apple.com/documentation/applicationservices/1459186-axisprocesstrustedwithoptions), [CGSessionCopyCurrentDictionary](https://developer.apple.com/documentation/coregraphics/cgsessioncopycurrentdictionary%28%29?language=objc), [Accessibility в настройках macOS](https://support.apple.com/guide/mac-help/allow-accessibility-apps-to-access-your-mac-mh43185/mac), [Apple DTS о responsible code и TCC](https://developer.apple.com/forums/thread/678819).
