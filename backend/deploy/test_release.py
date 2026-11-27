"""Exercise deployment ordering/failure handling without Docker, SSH or a real VPS."""
import json
import os
from pathlib import Path
import shlex
import subprocess
import tempfile
import unittest


class ReleaseTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="banya-release-test-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.old = "a" * 40
        self.new = "b" * 40
        self.source = Path(__file__).resolve().parent
        (self.root / "shared").mkdir()
        (self.root / "backups").mkdir()
        (self.root / "shared/.env").write_text(
            "DOMAIN=test.example.ru\nAPP_ENV=production\nCOOKIE_SECURE=true\n"
            "PUBLIC_URL=https://test.example.ru\nALLOWED_ORIGIN=https://test.example.ru\n"
        )
        for sha in [self.old, self.new]:
            deploy = self.root / "releases" / sha / "backend/deploy"
            deploy.mkdir(parents=True)
            (deploy / "release.sh").write_text(
                (self.source / "release.sh").read_text().replace(
                    "root=/opt/banya", "root=" + shlex.quote(str(self.root))
                )
            )
            (deploy / "smoke.sh").write_text((self.source / "smoke.sh").read_text())
        mock_bin = self.root / "bin"
        mock_bin.mkdir()
        docker = mock_bin / "docker"
        docker.write_text("""#!/usr/bin/env python3
import json, os, sys
args = sys.argv[1:]
with open(os.environ['DEPLOY_TEST_LOG'], 'a') as log:
    log.write(json.dumps(args) + '\\n')
failure = os.environ.get('DEPLOY_TEST_FAIL')
if failure == 'pull' and 'pull' in args:
    sys.exit(1)
if failure == 'checkpoint' and 'backup' in args and 'once' in args:
    sys.exit(1)
if failure == 'migration' and 'run' in args and 'migrate-booking' in args:
    sys.exit(1)
if 'exec' in args and any('/api/v1/baths' in arg for arg in args):
    print('{"baths": [{"id": "test"}]}')
""")
        docker.chmod(0o700)
        curl = mock_bin / "curl"
        curl.write_text("#!/bin/sh\nexit 0\n")
        curl.chmod(0o700)
        self.log = self.root / "calls.jsonl"
        self.env = {**os.environ, "PATH": str(mock_bin) + os.pathsep + os.environ["PATH"],
                    "DEPLOY_TEST_LOG": str(self.log)}

    def previous(self):
        old = self.root / "releases" / self.old
        (old / "release.env").write_text("IMAGE_TAG=" + self.old + "\n")
        (self.root / "current").symlink_to(old)

    def run_release(self, failure=""):
        self.env["DEPLOY_TEST_FAIL"] = failure
        result = subprocess.run([
            "bash", str(self.root / "releases" / self.new / "backend/deploy/release.sh"),
            self.new, "ghcr.io/example/banya",
        ], env=self.env, text=True, capture_output=True)
        calls = [json.loads(line) for line in self.log.read_text().splitlines()]
        return result, calls

    def test_first_release_has_no_previous_link(self):
        result, calls = self.run_release()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.root / "current").resolve().name, self.new)
        self.assertFalse((self.root / "previous").is_symlink())
        self.assertFalse(any("backup" in call for call in calls))
        self.assertEqual(sum(any(arg.startswith("migrate-") for arg in call) and "run" in call for call in calls), 4)

    def test_update_checkpoints_before_migrations(self):
        self.previous()
        result, calls = self.run_release()
        self.assertEqual(result.returncode, 0, result.stderr)
        checkpoint = next(i for i, call in enumerate(calls) if "backup" in call)
        migration = next(i for i, call in enumerate(calls) if "migrate-catalog" in call)
        self.assertLess(checkpoint, migration)
        self.assertEqual((self.root / "previous").resolve().name, self.old)
        self.assertEqual((self.root / "current").resolve().name, self.new)

    def test_pull_failure_does_not_stop_running_services(self):
        self.previous()
        result, calls = self.run_release("pull")
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(any("stop" in call for call in calls))
        self.assertEqual((self.root / "current").resolve().name, self.old)

    def test_checkpoint_failure_restarts_old_services(self):
        self.previous()
        result, calls = self.run_release("checkpoint")
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue(any("up" in call and "--no-deps" in call for call in calls))
        self.assertFalse(any("migrate-catalog" in call for call in calls))
        self.assertEqual((self.root / "current").resolve().name, self.old)

    def test_migration_failure_stops_apps_without_schema_rollback(self):
        self.previous()
        result, calls = self.run_release("migration")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("stop", calls[-1])
        self.assertFalse(any("start" in call or "down" in call for call in calls))
        self.assertEqual((self.root / "current").resolve().name, self.old)


if __name__ == "__main__":
    unittest.main()
