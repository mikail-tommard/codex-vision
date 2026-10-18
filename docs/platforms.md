# Сборка и запуск на разных платформах

Требуется Go 1.26.5+. Скрипты собирают исходный код в `bin/`; готовые бинарники не хранятся в Git. macOS требует CGO и Apple SDK на Mac. Windows и Linux собираются без CGO.

## macOS

```sh
./scripts/build.sh darwin arm64
# Для Intel:
./scripts/build.sh darwin amd64
```

[Разрешения macOS](macos-permissions.md) выдаются отдельно. Координаты — Quartz logical points; не смешивайте их с физическими Retina-пикселями. Используйте bounds и коэффициенты масштаба из ответов инструментов.

## Windows

Из PowerShell:

```powershell
.\scripts\build.ps1
# ARM64:
.\scripts\build.ps1 -TargetArch arm64
Copy-Item configs/config.example.json config.json
.\bin\codex-vision-windows-amd64.exe --doctor
```

Для регистрации подставьте абсолютные пути:

```powershell
codex mcp add codex_vision -- "C:\path\codex-vision\bin\codex-vision-windows-amd64.exe" --config "C:\path\codex-vision\config.json"
```

Координаты — физические пиксели виртуального рабочего стола, включая отрицательные начала. Backend проверяет DPI-awareness и отказывает на недоступном/защищённом desktop. UAC и ограничения Windows на фокус не обходятся. Поля macOS Accessibility и macOS app_launch недоступны.

## Linux X11

Нужны установленные локально `xrandr`, `xprop`, `xdotool`, ImageMagick `import` и `loginctl`. Сервер сам пакеты не устанавливает.

```sh
./scripts/build.sh
cp configs/config.example.json config.json
./bin/codex-vision-linux-amd64 --doctor
```

Для ARM64 используйте имя `codex-vision-linux-arm64`. Запускайте из активной разблокированной локальной X11-сессии с корректными `DISPLAY` и `XDG_SESSION_ID`. logind должен подтвердить локальность, активность и состояние сессии. Wayland и удалённый X11 отвергаются.

Linux backend поддерживает чтение экрана/окон, наведение, прокрутку и фокус. Кликам и клавиатуре нужна дальнейшая реализация backend; переносимый MCP-инструмент не означает поддержку операции на любой ОС.

## Установка в отдельный каталог

`./scripts/install.sh [каталог]` или `scripts/install.ps1` собирает и копирует программу. Существующий executable не заменяется. Скрипты не добавляют автозапуск, не меняют PATH и не регистрируют MCP сами.

## Mock

```sh
go run ./cmd/codex-vision --mock --config configs/config.example.json
```

Процесс ожидает MCP JSON-RPC по stdin/stdout; его запуск сам по себе не открывает графическое окно. Mock не захватывает реальный экран и не отправляет ввод ОС. Вывод логов идёт в stderr, stdout зарезервирован под MCP.
