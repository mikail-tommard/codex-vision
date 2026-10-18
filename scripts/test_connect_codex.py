"""Regression tests: never write to the operator's real Codex configuration."""
import importlib.util
from pathlib import Path
import tempfile
import tomllib
import unittest

spec = importlib.util.spec_from_file_location("connect_codex", Path(__file__).with_name("connect-codex.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class ConnectCodexTests(unittest.TestCase):
    def test_registration_preserves_other_settings_and_is_idempotent(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "config.toml"
            original = 'model = "example"\n[mcp_servers.other]\ncommand = "other"\n'
            path.write_text(original)
            block = '[mcp_servers.codex_vision]\ncommand = "server"\n'
            module.add_server(path, block, dry_run=True)
            self.assertEqual(path.read_text(), original)
            module.add_server(path, block)
            self.assertEqual(tomllib.loads(path.read_text())["mcp_servers"]["other"]["command"], "other")
            self.assertEqual(len(list(path.parent.glob("*.before-codex-vision-*"))), 1)
            before = path.read_bytes()
            module.add_server(path, block)
            self.assertEqual(path.read_bytes(), before)

    def test_conflicting_entry_is_not_replaced(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "config.toml"
            original = '[mcp_servers.codex_vision]\ncommand = "existing"\n'
            path.write_text(original)
            with self.assertRaises(RuntimeError):
                module.add_server(path, '[mcp_servers.codex_vision]\ncommand = "different"\n')
            self.assertEqual(path.read_text(), original)

    def test_paths_are_absolute_and_quoted(self):
        with tempfile.TemporaryDirectory(prefix="codex vision ") as directory:
            root = Path(directory)
            binary = root / 'server "quoted"'
            binary.write_text("test")
            binary.chmod(0o700)
            config = root / "config.json"
            config.write_text('{}')
            block = tomllib.loads(module.server_block(root, binary, config))["mcp_servers"]["codex_vision"]
            self.assertEqual(block["command"], str(binary.resolve()))
            self.assertEqual(block["cwd"], str(root.resolve()))
            self.assertEqual(block["args"], ["--config", str(config.resolve())])

    def test_missing_files_fail_before_registration(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            with self.assertRaises(RuntimeError):
                module.server_block(root, root / "missing", root / "missing.json")
            binary = root / "server"
            binary.write_text("test")
            binary.chmod(0o700)
            with self.assertRaises(RuntimeError):
                module.server_block(root, binary, root / "missing.json")


if __name__ == "__main__":
    unittest.main()
