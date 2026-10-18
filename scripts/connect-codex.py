#!/usr/bin/env python3
"""Register this checkout with Codex, preserving unrelated settings (Python 3.11+)."""
import argparse
import os
import json
import platform
from pathlib import Path
import tempfile
import time
import tomllib


def add_server(config_path: Path, block: str, dry_run: bool = False) -> str:
    if config_path.is_symlink():
        raise RuntimeError("Refusing to replace a symlink; use its explicit target path.")
    original = config_path.read_bytes() if config_path.exists() else b""
    before = tomllib.loads(original.decode("utf-8"))
    wanted = tomllib.loads(block)["mcp_servers"]["codex_vision"]
    existing = before.get("mcp_servers", {}).get("codex_vision")
    if existing is not None:
        if existing == wanted:
            return "Already configured; no changes."
        raise RuntimeError("A different codex_vision entry exists; refusing to replace it.")
    updated = original + (b"\n" if original and not original.endswith(b"\n") else b"") + b"\n" + block.encode()
    after = tomllib.loads(updated.decode("utf-8"))
    actual = after["mcp_servers"].pop("codex_vision")
    if "mcp_servers" not in before and not after["mcp_servers"]:
        del after["mcp_servers"]
    if actual != wanted or after != before:
        raise RuntimeError("Prepared edit changes unrelated settings; refusing.")
    if dry_run:
        return f"Validated: add only [mcp_servers.codex_vision] to {config_path}"
    config_path.parent.mkdir(parents=True, exist_ok=True)
    # Backup and replacement stay in the private config directory, never workspace.
    if original:
        backup = config_path.with_name(config_path.name + f".before-codex-vision-{time.time_ns()}")
        fd = os.open(backup, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
        with os.fdopen(fd, "wb") as stream:
            stream.write(original)
    fd, temporary = tempfile.mkstemp(prefix=".codex-vision-config-", dir=config_path.parent)
    try:
        with os.fdopen(fd, "wb") as stream:
            stream.write(updated)
            stream.flush()
            os.fsync(stream.fileno())
        # Do not lose concurrent user edits while preparing this addition.
        current = config_path.read_bytes() if config_path.exists() else b""
        if current != original:
            raise RuntimeError("Codex config changed concurrently; retry after reviewing.")
        os.replace(temporary, config_path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)
    return f"Added codex_vision to {config_path}; other settings preserved."



def server_block(root: Path, binary: Path | None = None, server_config: Path | None = None) -> str:
    root = root.resolve()
    if binary is None:
        system = {"Darwin": "darwin", "Linux": "linux", "Windows": "windows"}.get(platform.system())
        arch = {"x86_64": "amd64", "amd64": "amd64", "arm64": "arm64", "aarch64": "arm64"}.get(platform.machine().lower())
        if system is None or arch is None:
            raise RuntimeError("Unsupported host; pass --binary explicitly.")
        suffix = ".exe" if system == "windows" else ""
        binary = root / "bin" / f"codex-vision-{system}-{arch}{suffix}"
    binary = binary.resolve()
    server_config = (server_config or root / "config.json").resolve()
    if not binary.is_file() or not os.access(binary, os.X_OK):
        raise RuntimeError(f"Build the native executable first or pass --binary: {binary}")
    if not server_config.is_file():
        raise RuntimeError(f"Create a local config from configs/config.example.json first: {server_config}")
    if not isinstance(json.loads(server_config.read_text()), dict):
        raise ValueError("Server config must be a JSON object.")
    quote = json.dumps  # JSON string escapes are compatible with TOML basic strings.
    return ("[mcp_servers.codex_vision]\n"
            f"command = {quote(str(binary))}\n"
            f"args = [\"--config\", {quote(str(server_config))}]\n"
            f"cwd = {quote(str(root))}\n"
            "startup_timeout_sec = 15\ntool_timeout_sec = 45\n")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--config", type=Path, default=Path(os.environ.get("CODEX_HOME", str(Path.home() / ".codex"))) / "config.toml")
    parser.add_argument("--dry-run", action="store_true")
    parser.add_argument("--binary", type=Path, help="Built native server executable")
    parser.add_argument("--server-config", type=Path, help="Local server JSON; defaults to checkout/config.json")
    args = parser.parse_args()
    root = Path(__file__).resolve().parent.parent
    try:
        block = server_block(root, args.binary, args.server_config)
        print(add_server(args.config, block, args.dry_run))
    except (RuntimeError, OSError, ValueError) as error:
        parser.exit(1, f"codex-vision: {error}\n")


if __name__ == "__main__":
    main()
