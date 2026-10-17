#!/usr/bin/env python3
"""Exercise cargo-bounded.sh with fake commands and temporary directories.

Run: python3 scripts/test_cargo_bounded.py
No Cargo compiler, real target directory, or existing cache is touched.
"""

from __future__ import annotations

import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import unittest


SCRIPT = Path(__file__).with_name("cargo-bounded.sh").resolve()
REAL_DU = shutil.which("du")
FAKE_CARGO = r'''#!/usr/bin/env python3
import json, os, pathlib, subprocess, sys, time

target = pathlib.Path(os.environ["CARGO_TARGET_DIR"])
mode = sys.argv[1]
if mode == "environment":
    keys = ["CARGO_TARGET_DIR", "CARGO_BUILD_TARGET_DIR", "CARGO_INCREMENTAL",
            "CARGO_PROFILE_DEV_INCREMENTAL", "CARGO_PROFILE_TEST_INCREMENTAL",
            "CARGO_PROFILE_DEV_DEBUG", "CARGO_PROFILE_TEST_DEBUG",
            "CARGO_PROFILE_DEV_BUILD_OVERRIDE_DEBUG", "CARGO_PROFILE_TEST_BUILD_OVERRIDE_DEBUG",
            "RUSTC_WRAPPER", "RUSTC_WORKSPACE_WRAPPER", "CARGO_BUILD_RUSTC_WRAPPER",
            "CARGO_BUILD_RUSTC_WORKSPACE_WRAPPER", "CARGO_BUILD_JOBS"]
    print(json.dumps({key: os.environ.get(key) for key in keys}))
elif mode == "exit":
    sys.exit(17)
elif mode == "hold":
    (target / "started").touch()
    time.sleep(30)
elif mode == "grow":
    started = pathlib.Path(os.environ["DESCENDANT_STARTED"])
    subprocess.Popen([sys.executable, "-c", "import pathlib,time; pathlib.Path(%r).touch(); time.sleep(3); pathlib.Path(%r).touch()" % (str(started), os.environ["DESCENDANT_MARKER"])])
    deadline = time.monotonic() + 5
    while not started.exists() and time.monotonic() < deadline:
        time.sleep(0.01)
    if not started.exists():
        raise SystemExit("descendant did not start")
    (target / "artifact").write_bytes(b"x" * (1024 * 1024))
    time.sleep(30)
elif mode == "fast":
    (target / "artifact").write_bytes(b"x" * (1024 * 1024))
elif mode == "free":
    pathlib.Path(os.environ["FREE_MARKER"]).touch()
    time.sleep(30)
elif mode == "du-failure":
    pathlib.Path(os.environ["DU_MARKER"]).touch()
    time.sleep(30)
else:
    raise SystemExit("unexpected fake Cargo invocation")
'''


class CargoBoundedTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory(prefix="cargo-bounded-test-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.target = self.root / "target"
        self.bindir = self.root / "bin"
        self.bindir.mkdir()
        cargo = self.bindir / "cargo"
        cargo.write_text(FAKE_CARGO, encoding="utf-8")
        cargo.chmod(0o755)
        self.env = os.environ.copy()
        self.env.update({
            "PATH": str(self.bindir) + os.pathsep + self.env["PATH"],
            "CARGO_BOUNDED_TARGET_DIR": str(self.target),
            "CARGO_BOUNDED_MIN_FREE_BYTES": "0",
            "CARGO_BOUNDED_INTERVAL": "1",
            "RUSTC_WRAPPER": "/existing/wrapper",
            "CARGO_PROFILE_DEV_DEBUG": "2",
            "CARGO_BUILD_JOBS": "7",
            "DESCENDANT_MARKER": str(self.root / "descendant-survived"),
            "DESCENDANT_STARTED": str(self.root / "descendant-started"),
            "FREE_MARKER": str(self.root / "free-low"),
            "DU_MARKER": str(self.root / "du-failure"),
        })

    def invoke(self, *args: str, **settings: str) -> subprocess.CompletedProcess[str]:
        env = self.env.copy()
        env.update(settings)
        return subprocess.run(
            [str(SCRIPT), *args], env=env, text=True, capture_output=True, timeout=15
        )

    def wait_for(self, path: Path) -> None:
        deadline = time.monotonic() + 10
        while not path.exists() and time.monotonic() < deadline:
            time.sleep(0.05)
        self.assertTrue(path.exists(), f"timed out waiting for {path}")

    def test_environment_and_exit_status(self) -> None:
        result = self.invoke("environment", "--", "--config")
        self.assertEqual(result.returncode, 0, result.stderr)
        actual = json.loads(result.stdout)
        self.assertEqual(actual["CARGO_TARGET_DIR"], str(self.target.resolve()))
        self.assertEqual(actual["CARGO_BUILD_TARGET_DIR"], str(self.target.resolve()))
        self.assertEqual(actual["CARGO_INCREMENTAL"], "0")
        for key in ("CARGO_PROFILE_DEV_INCREMENTAL", "CARGO_PROFILE_TEST_INCREMENTAL"):
            self.assertEqual(actual[key], "false", key)
        for key in ("CARGO_PROFILE_DEV_DEBUG", "CARGO_PROFILE_TEST_DEBUG",
                    "CARGO_PROFILE_DEV_BUILD_OVERRIDE_DEBUG", "CARGO_PROFILE_TEST_BUILD_OVERRIDE_DEBUG"):
            self.assertEqual(actual[key], "0", key)
        for key in ("RUSTC_WRAPPER", "RUSTC_WORKSPACE_WRAPPER",
                    "CARGO_BUILD_RUSTC_WRAPPER", "CARGO_BUILD_RUSTC_WORKSPACE_WRAPPER"):
            self.assertEqual(actual[key], "", key)
        self.assertEqual(actual["CARGO_BUILD_JOBS"], "7")
        self.assertEqual(self.invoke("exit").returncode, 17)

    def test_invalid_options_and_starting_limits_are_rejected(self) -> None:
        for args in (("--target-dir", "other"), ("--target-dir=other",),
                     ("--config", "build.jobs=1"), ("--config=build.jobs=1",)):
            result = self.invoke("environment", *args)
            self.assertNotEqual(result.returncode, 0, args)
            self.assertIn("not permitted", result.stderr)
        for key, value in (("CARGO_BOUNDED_INTERVAL", "0"),
                           ("CARGO_BOUNDED_MAX_BYTES", "invalid"),
                           ("CARGO_BOUNDED_MIN_FREE_BYTES", "-1")):
            self.assertNotEqual(self.invoke("environment", **{key: value}).returncode, 0)
        self.target.mkdir(exist_ok=True)
        artifact = self.target / "preexisting"
        artifact.write_bytes(b"x" * 65536)
        result = self.invoke("environment", CARGO_BOUNDED_MAX_BYTES="1")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("refusing to start", result.stderr)
        self.assertEqual(result.stdout, "")
        self.assertTrue(artifact.exists())
        result = self.invoke("environment", CARGO_BOUNDED_MIN_FREE_BYTES=str(2**63))
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("refusing to start", result.stderr)

    def test_lock_and_signal_forwarding(self) -> None:
        proc = subprocess.Popen([str(SCRIPT), "hold"], env=self.env, text=True,
                                stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        try:
            self.wait_for(self.target / "started")
            second = self.invoke("environment")
            self.assertNotEqual(second.returncode, 0)
            self.assertIn("another guarded command", second.stderr)
        finally:
            proc.send_signal(signal.SIGTERM)
            _, stderr = proc.communicate(timeout=10)
        self.assertEqual(proc.returncode, 128 + signal.SIGTERM, stderr)
        self.assertIn("stopping Cargo process group", stderr)
        self.assertEqual(self.invoke("environment").returncode, 0)

    def test_live_target_budget_stops_descendants_but_not_unrelated_processes(self) -> None:
        unrelated = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(30)"])
        try:
            result = self.invoke("grow", CARGO_BOUNDED_MAX_BYTES="65536")
            self.assertEqual(result.returncode, 125, result.stderr)
            self.assertIn("target uses", result.stderr)
            self.assertIsNone(unrelated.poll())
            self.assertTrue((self.target / "artifact").exists())
            self.assertTrue((self.root / "descendant-started").exists())
            time.sleep(3.2)
            self.assertFalse((self.root / "descendant-survived").exists())
        finally:
            unrelated.terminate()
            unrelated.wait(timeout=10)

    def test_fast_command_is_sampled_after_exit(self) -> None:
        result = self.invoke("fast", CARGO_BOUNDED_MAX_BYTES="65536")
        self.assertEqual(result.returncode, 125, result.stderr)
        self.assertIn("target uses", result.stderr)
        self.assertTrue((self.target / "artifact").exists())

    def test_live_free_space_drop_stops_command(self) -> None:
        # Keep this deterministic without actually filling the host disk. The
        # wrapper reads disk_usage through the standard Python site mechanism.
        site = self.root / "site"
        site.mkdir()
        (site / "sitecustomize.py").write_text(
            "import os, shutil\nfrom collections import namedtuple\n"
            "D = namedtuple('usage', 'total used free')\n"
            "shutil.disk_usage = lambda path: D(1000000000, 0, "
            "0 if os.path.exists(os.environ['FREE_MARKER']) else 1000000000)\n",
            encoding="utf-8",
        )
        result = self.invoke("free", PYTHONPATH=str(site), CARGO_BOUNDED_MIN_FREE_BYTES="100")
        self.assertEqual(result.returncode, 125, result.stderr)
        self.assertIn("filesystem has 0 bytes free", result.stderr)
        self.assertTrue((self.root / "free-low").exists())

    @unittest.skipUnless(REAL_DU, "du is unavailable")
    def test_measurement_failure_stops_running_command(self) -> None:
        du = self.bindir / "du"
        du.write_text(
            "#!/usr/bin/env python3\nimport os, sys\n"
            "if os.path.exists(os.environ['DU_MARKER']):\n"
            "    sys.stderr.write('fake du failure\\n'); sys.exit(1)\n"
            f"os.execv({REAL_DU!r}, [{REAL_DU!r}, *sys.argv[1:]])\n",
            encoding="utf-8",
        )
        du.chmod(0o755)
        result = self.invoke("du-failure")
        self.assertEqual(result.returncode, 125, result.stderr)
        self.assertIn("cannot measure target", result.stderr)
        self.assertIn("fake du failure", result.stderr)


if __name__ == "__main__":
    unittest.main()
