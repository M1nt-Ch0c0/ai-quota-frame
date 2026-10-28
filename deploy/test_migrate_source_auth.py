import importlib.util
import os
from pathlib import Path
import tempfile
import unittest


spec = importlib.util.spec_from_file_location(
    "migrate_source_auth", Path(__file__).with_name("migrate-source-auth.py")
)
migration = importlib.util.module_from_spec(spec)
spec.loader.exec_module(migration)


class SourceMigrationTests(unittest.TestCase):
    def test_preserves_device_and_display_configuration(self):
        untouched = b"# private runtime\nPHOTOFRAME_PUSH_TOKEN='unchanged-device-token'\nFRAME_THEME=mint-skin\nREFRESH_INTERVAL=15m\nHTTP_PROXY=http://127.0.0.1:7890\n"
        original = untouched + b"CLIPROXY_MANAGEMENT_KEY='old-key'\n"
        updated = migration.replace_assignments(
            original,
            {"CLIPROXY_MANAGEMENT_KEY": "new-key", "SOURCE_AUTH_MODE": "management-key"},
        )
        self.assertTrue(updated.startswith(untouched))
        self.assertNotIn(b"old-key", updated)
        self.assertIn(b"SOURCE_AUTH_MODE=management-key\n", updated)

    def test_refuses_duplicate_assignments(self):
        with self.assertRaises(ValueError):
            migration.replace_assignments(
                b"SOURCE_AUTH_MODE=management-key\nexport SOURCE_AUTH_MODE=google-id-token\n",
                {"SOURCE_AUTH_MODE": "management-key"},
            )

    def test_backup_is_private_and_never_overwrites(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "backup"
            migration.private_write(path, b"private configuration")
            self.assertEqual(os.stat(path).st_mode & 0o777, 0o600)
            with self.assertRaises(FileExistsError):
                migration.private_write(path, b"replacement")
            self.assertEqual(path.read_bytes(), b"private configuration")


if __name__ == "__main__":
    unittest.main()
