# Security model

Codex Vision is a local desktop bridge for an explicitly connected MCP client. It is not an isolation boundary against other programs running as the same operating-system user.

## Observation and privacy

Connecting a client authorizes it to receive requested desktop images and window metadata. The application allowlist restricts **input**, not observation. Visible private content can appear in a screenshot regardless of which application owns it.

Images are encoded in memory and returned through MCP stdio. The server does not save screenshot history, call an LLM, send telemetry or upload images to an external service. A connected MCP client may forward tool results to its own model/provider. Memory is not securely erased; OS paging and crash dumps are outside this guarantee.

The optional browser preview opens a loopback-only HTTP listener and exposes a capability URL. Anyone able to use that URL locally can view the session; do not publish it. Preview is not a privacy-redaction mechanism. Stopping/restarting a session invalidates its preview capability.

## Local authorization

The default mode is `observe`: desktop reads without input. `assist` requires a local capability allowlist and exact executable paths in `allowed_processes`. Startup JSON controls policy; MCP arguments cannot change it.

Operations requiring approval need `local_approval:true`. With `confirm_each_action:true`, the backend must provide local review UI; otherwise those operations are unavailable. macOS implements local AppKit review through an `osascript` subprocess. The complete escaped payload is supplied through stdin, not command-line arguments, and cancellation is the default.

With `confirm_each_action:false`, the local configuration grants standing permission for ordinary allowlisted input without individual dialogs. OS permission, foreground, geometry, stop and budget checks still apply. This can enable substantial GUI actions: review the allowlist and trust the client before using it. The server does not infer that a button, typed command or form submission is harmless.

Store configuration in a directory controlled by the operator. Do not share one server among mutually untrusted clients. `max_actions_per_task` is a process-lifetime input budget; read budgets are separate. Setting a local limit to zero does not remove model-provider limits.

## Target identity and platform restrictions

- **macOS:** requires an unlocked local console and relevant Screen Recording/Accessibility permissions. It does not edit TCC, reset system privacy settings or elevate privileges. Window focus matches the requested Quartz window with AX geometry and, when needed, a unique matching title; it does not authorize an arbitrary window by title alone.
- **Windows:** requires the supported input desktop and correct DPI behavior. It does not bypass secure desktop, UAC, UIPI or foreground restrictions.
- **Linux:** rejects Wayland and remote X11 and requires an active unlocked local logind X11 session. It depends on the desktop environment accurately reporting lock/session state.

Process-image checks, opaque window IDs and repeated foreground checks reduce accidental targeting. They cannot eliminate races with other same-user software. Unreadable identity or ambiguous focus fails closed. On macOS, recognized code-sign clones require native identity verification, not a matching filename alone.

The exact installed FreeCAD launcher/runtime pair is recognized only when confirming launch. Input into its GUI requires a separate runtime allowlist entry. This does not permit Python, arbitrary helpers or an entire directory.

### macOS scroll targeting

Scroll is pinned to the current opaque window ID/native Quartz ID and PID. A Quartz bounding-box ambiguity may be resolved only by a native AX hit belonging to that foreground process and focused-window geometry. Layers are not blindly ignored. Missing evidence or a foreign/different window refuses input. Cursor position and foreground geometry are rechecked before posting; same-user races cannot be eliminated. This fallback reads structural ownership/bounds, not field contents.

### Windows focus confirmation

Focus restores an iconic target asynchronously, sends one foreground request, then polls for the exact HWND within a bounded deadline. Each poll rechecks the interactive desktop and the token/PID. Cancellation or stale identity stops the attempt. No synthetic Alt/Tab, taskbar click, AttachThreadInput, foreground-lock setting or elevation fallback is used. OS denial and unresponsive targets remain possible; the error reports observed request/result metadata rather than claiming a proven Windows policy cause.

## Actions, waiting and failure

Input is serialized and cancellation-aware. Each action retains policy and foreground checks. A desktop batch prevalidates predictable steps, pins window/process/geometry and stops at the first error. Completed steps are not rolled back. The server never automatically repeats a partially executed batch, click or drag.

A stable visual change is not proof of a completed operation or an opponent's response. The temporal filter can suppress useful brief events and cannot distinguish clocks, animations or meaningful objects. AX readiness checks require observed labels and known states; ambiguous, truncated or unknown metadata does not prove readiness. Permission/focus failures do not silently become visual fallback.

A text-only readiness result confirms only its stated predicate, not page contents or business success. Unseen pixels remain available for explicit delivery. `continuation` is advisory metadata, not authorization for another action. Filling a field does not press Enter, but applications may autosave or react to edits.

## Stop and audit

`session_stop` latches an emergency stop and cancels active work. A configured stop file also latches stop; removing it does not restore the running process. Recovery requires an operator-controlled new process after resolving the stop condition. Ordinary observation stop is separate from emergency stop.

Audit records go to stderr and the configured local JSONL file. Typed text and image payloads are not intentionally logged. Protect the audit location; OS error messages and metadata can still reveal application identity. MCP stdout is reserved for protocol messages.

There is no general shell tool, hidden persistence, startup service, credential-extraction feature or privilege-escalation capability. GUI keyboard access can still operate a terminal or submit consequential actions; the client must follow the user's actual authorization.

## Reporting a vulnerability

Do not post credentials, private screenshots, audit files or working configs in public issues. Use the repository's private vulnerability reporting facility if enabled, or establish a private channel with the maintainer. For ordinary bugs, provide a minimal redacted reproduction, OS/architecture and `session_status.server.version`.
