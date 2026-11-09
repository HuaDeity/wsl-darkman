import argparse
import importlib.machinery
import importlib.util
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import threading
import time
import unittest
from unittest.mock import patch

SOURCE = Path(__file__).resolve().parents[1] / "wsl-darkman"
loader = importlib.machinery.SourceFileLoader("sync", str(SOURCE))
spec = importlib.util.spec_from_loader(loader.name, loader)
sync = importlib.util.module_from_spec(spec)
loader.exec_module(sync)


def options(**kwargs):
    return argparse.Namespace(source="apps", powershell=None, no_gsettings=True,
                              hook_timeout=0.2, backend="standalone", **kwargs)


class SyncTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.env = patch.dict(os.environ, {
            "XDG_DATA_HOME": str(self.root / "user"),
            "XDG_DATA_DIRS": str(self.root / "system"),
            "XDG_CACHE_HOME": str(self.root / "cache"),
            "XDG_RUNTIME_DIR": str(self.root / "runtime"),
        })
        self.env.start()
        self.addCleanup(self.env.stop)

    def hook(self, name, body, mode="dark", root="user", executable=True):
        path = self.root / root / (mode + "-mode.d") / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text("#!/bin/sh\n" + body + "\n")
        path.chmod(0o755 if executable else 0o644)
        return path

    def test_modes_and_reject_unknown_values(self):
        self.assertEqual(sync.parse_mode(b"dark\r\n"), "dark")
        self.assertEqual(sync.parse_mode(b"light\n"), "light")
        for invalid in (b"", b"0", b"dark\nlight\n", b"broken"):
            with self.assertRaises(ValueError):
                sync.parse_mode(invalid)

    def test_hook_order_precedence_and_mode_environment(self):
        target = self.root / "calls"
        self.hook("20-last", 'printf "last:%s\\n" "$WSL_DARKMAN_MODE" >> "$CALLS"')
        self.hook("10-first", 'echo user >> "$CALLS"')
        self.hook("10-first", 'echo system >> "$CALLS"', root="system")
        self.hook("15-disabled", 'echo disabled >> "$CALLS"', executable=False)
        self.hook("15-disabled", 'echo should-not-run >> "$CALLS"', root="system")
        self.hook("other-mode", 'echo light >> "$CALLS"', mode="light")
        with patch.dict(os.environ, {"CALLS": str(target)}):
            self.assertTrue(sync.apply_mode("dark", options()))
        self.assertEqual(target.read_text(), "user\nlast:dark\n")

    def test_failure_and_timeout_do_not_block_later_hooks(self):
        target = self.root / "calls"
        self.hook("10-fail", "exit 7")
        self.hook("20-timeout", "sleep 3")
        self.hook("30-success", 'echo done >> "$CALLS"')
        start = time.monotonic()
        with patch.dict(os.environ, {"CALLS": str(target)}):
            self.assertFalse(sync.apply_mode("dark", options()))
        self.assertLess(time.monotonic() - start, 2)
        self.assertEqual(target.read_text(), "done\n")

    def test_gsettings_preferences(self):
        args = options()
        args.no_gsettings = False
        with patch.object(sync.shutil, "which", return_value="/usr/bin/gsettings"), \
             patch.object(sync, "run_action", return_value=True) as action:
            for mode, preference in (("dark", "prefer-dark"), ("light", "prefer-light")):
                self.assertTrue(sync.apply_mode(mode, args))
                self.assertEqual(action.call_args.args[0][-1], preference)

    def test_unified_hook_receives_mode_and_overrides_legacy(self):
        target = self.root / "calls"
        self.hook("theme", 'echo legacy >> "$CALLS"')
        modern = self.root / "system" / "darkman" / "theme"
        modern.parent.mkdir(parents=True)
        modern.write_text('#!/bin/sh\nprintf "%s:%s:%s\\n" "$#" "$1" "$WSL_DARKMAN_MODE" >> "$CALLS"\n')
        modern.chmod(0o755)
        with patch.dict(os.environ, {"CALLS": str(target)}):
            self.assertTrue(sync.apply_mode("dark", options()))
            self.assertTrue(sync.apply_mode("light", options()))
        self.assertEqual(target.read_text(), "1:dark:dark\n1:light:light\n")

    def test_unified_user_mask_disables_system_and_legacy(self):
        self.hook("theme", "exit 1")
        system = self.root / "system" / "darkman" / "theme"
        system.parent.mkdir(parents=True)
        system.write_text("#!/bin/sh\nexit 1\n")
        system.chmod(0o755)
        user = self.root / "user" / "darkman" / "theme"
        user.parent.mkdir(parents=True)
        user.write_text("disabled\n")
        user.chmod(0o644)
        self.assertEqual(sync.hooks_for("dark"), [])

    def test_legacy_hook_receives_no_arguments(self):
        target = self.root / "calls"
        self.hook("theme", 'echo "$#" >> "$CALLS"')
        with patch.dict(os.environ, {"CALLS": str(target)}):
            self.assertTrue(sync.apply_mode("dark", options()))
        self.assertEqual(target.read_text(), "0\n")

    def test_system_directory_order_matches_darkman(self):
        self.hook("theme", "exit 0", root="first")
        last = self.hook("theme", "exit 0", root="last")
        with patch.dict(os.environ, {"XDG_DATA_DIRS": str(self.root / "first") + ":" + str(self.root / "last")}):
            self.assertEqual(sync.hooks_for("dark"), [[str(last)]])

    def test_darkman_backend_delegates_without_local_actions(self):
        args = options()
        args.backend = "darkman"
        args.no_gsettings = False
        with patch.object(sync, "run_action", return_value=True) as action, \
             patch.object(sync, "hooks_for") as hooks:
            self.assertTrue(sync.apply_mode("dark", args))
        action.assert_called_once_with(["darkman", "set", "dark"], args.hook_timeout)
        hooks.assert_not_called()

    def test_darkman_backend_failure_triggers_recovery(self):
        args = options()
        args.backend = "darkman"
        with patch.object(sync, "windows_command", return_value=[sys.executable, "-c", "import sys; print('dark', flush=True); sys.stdin.read()"]), \
             patch.object(sync, "apply_mode", return_value=False):
            with self.assertRaisesRegex(RuntimeError, "Could not send mode to Darkman"):
                sync.watch_session(args, threading.Event())

    def test_darkman_backend_executes_cli_and_propagates_failure(self):
        args = options()
        args.backend = "darkman"
        executable = self.root / "bin" / "darkman"
        executable.parent.mkdir()
        output = self.root / "cli-arguments"
        executable.write_text('#!/bin/sh\nprintf "%s\\n" "$@" > "$CLI_ARGUMENTS"\n')
        executable.chmod(0o755)
        env = {"PATH": str(executable.parent) + ":" + os.environ.get("PATH", ""),
               "CLI_ARGUMENTS": str(output)}
        with patch.dict(os.environ, env):
            self.assertTrue(sync.apply_mode("light", args))
            self.assertEqual(output.read_text(), "set\nlight\n")
            executable.write_text("#!/bin/sh\nexit 7\n")
            self.assertFalse(sync.apply_mode("dark", args))

    def test_encoded_windows_command(self):
        import base64
        args = options()
        args.powershell = "/custom/powershell.exe"
        args.source = "system"
        command = sync.windows_command(args, once=True)
        self.assertEqual(command[0], args.powershell)
        script = base64.b64decode(command[-1]).decode("utf-16-le")
        self.assertIn("::Run('SystemUsesLightTheme', $true)", script)
        self.assertNotIn("__VALUE__", script)

    def test_read_mode_and_host_failure(self):
        with patch.object(sync, "windows_command", return_value=[sys.executable, "-c", "print('dark')"]):
            self.assertEqual(sync.read_mode(options()), "dark")
        with patch.object(sync, "windows_command", return_value=[sys.executable, "-c", "raise SystemExit(3)"]):
            with self.assertRaises(subprocess.CalledProcessError):
                sync.read_mode(options())

    def test_real_watcher_stream_deduplicates_and_closes_stdin(self):
        marker = self.root / "watcher-exited"
        program = ("import sys, pathlib; "
                   "sys.stdout.write('dark\\r\\ndark\\r\\nlight\\r\\nlight\\r\\n'); "
                   "sys.stdout.flush(); sys.stdin.read(); "
                   "pathlib.Path(sys.argv[1]).write_text('closed')")
        stop = threading.Event()
        seen = []

        def applied(mode, _):
            seen.append(mode)
            if mode == "light":
                stop.set()

        with patch.object(sync, "windows_command", return_value=[sys.executable, "-c", program, str(marker)]), \
             patch.object(sync, "apply_mode", side_effect=applied):
            sync.watch_session(options(), stop)
        self.assertEqual(seen, ["dark", "light"])
        self.assertEqual(marker.read_text(), "closed")

    def test_watcher_eof_reconnects_and_resynchronizes(self):
        stop = threading.Event()
        seen = []
        commands = [
            [sys.executable, "-c", "print('dark')"],
            [sys.executable, "-c", "import sys; print('light', flush=True); sys.stdin.read()"],
        ]

        def applied(mode, _):
            seen.append(mode)
            if mode == "light":
                stop.set()

        with patch.object(sync, "windows_command", side_effect=commands), \
             patch.object(sync, "apply_mode", side_effect=applied):
            sync.run_daemon(options(), stop)
        self.assertEqual(seen, ["dark", "light"])

    def test_startup_can_be_interrupted_before_first_mode(self):
        stop = threading.Event()
        timer = threading.Timer(0.1, stop.set)
        timer.start()
        self.addCleanup(timer.join)
        start = time.monotonic()
        with patch.object(sync, "windows_command", return_value=[sys.executable, "-c", "import sys; sys.stdin.read()"]):
            sync.watch_session(options(), stop)
        self.assertLess(time.monotonic() - start, 2)

    def test_singleton_rejects_another_process(self):
        lock_dir = self.root / "runtime" / "wsl-darkman"
        lock_dir.mkdir(parents=True)
        with (lock_dir / "daemon.lock").open("a") as lock:
            sync.fcntl.flock(lock, sync.fcntl.LOCK_EX)
            result = subprocess.run([sys.executable, str(SOURCE), "run"],
                                    stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=3)
        self.assertEqual(result.returncode, 1)
        self.assertIn(b"already running", result.stderr)

    def test_cli_sigterm_stops_daemon_and_host(self):
        ready = self.root / "ready"
        stopped = self.root / "stopped"
        host = self.root / "powershell.exe"
        host.write_text("#!/usr/bin/env python3\n"
                        "import sys, pathlib\n"
                        "print('dark', flush=True)\n"
                        "sys.stdin.read()\n"
                        "pathlib.Path(%r).write_text('stopped')\n" % str(stopped))
        host.chmod(0o755)
        self.hook("ready", 'touch "$READY"')
        env = dict(os.environ, READY=str(ready))
        with subprocess.Popen([sys.executable, str(SOURCE), "run", "--no-gsettings",
                               "--powershell", str(host)], env=env,
                              stdout=subprocess.PIPE, stderr=subprocess.PIPE) as daemon:
            try:
                deadline = time.monotonic() + 5
                while not ready.exists() and daemon.poll() is None and time.monotonic() < deadline:
                    time.sleep(0.02)
                self.assertTrue(ready.exists(), "Daemon never applied the initial host mode")
                daemon.send_signal(signal.SIGTERM)
                daemon.communicate(timeout=5)
                self.assertEqual(daemon.returncode, 0)
                self.assertTrue(stopped.exists(), "Host did not receive stdin EOF on shutdown")
            finally:
                if daemon.poll() is None:
                    daemon.kill()
                    daemon.communicate()

    def test_invalid_timeouts(self):
        for value in ("0", "-1", "nan", "inf"):
            with self.assertRaises(argparse.ArgumentTypeError):
                sync.positive_seconds(value)


if __name__ == "__main__":
    unittest.main()
