import importlib.machinery
import importlib.util
import pathlib
import unittest
from unittest.mock import patch


loader = importlib.machinery.SourceFileLoader(
    "storage", str(pathlib.Path(__file__).with_name("check-storage"))
)
spec = importlib.util.spec_from_loader(loader.name, loader)
storage = importlib.util.module_from_spec(spec)
loader.exec_module(storage)


class StorageGuardTest(unittest.TestCase):
    def check_layout(self, root_flags):
        mounts = {
            "/data": {"target": "/data", "fstype": "ext4", "fs-options": "rw"},
            "/": {"target": "/", "fstype": "ext4", "fs-options": root_flags},
            "/boot/firmware": {"target": "/boot/firmware", "fstype": "vfat", "fs-options": "ro"},
        }
        with patch.object(storage, "mount", side_effect=mounts.__getitem__), \
                patch.object(storage.os.path, "isfile", return_value=True), \
                patch.object(storage.os.path, "isdir", return_value=True):
            storage.check()

    def test_read_only_superblock_is_accepted(self):
        self.check_layout("ro,relatime")

    def test_writable_superblock_is_rejected(self):
        with self.assertRaisesRegex(AssertionError, "root filesystem is writable"):
            self.check_layout("rw,relatime")

    def test_findmnt_uses_superblock_not_sandbox_flags(self):
        with patch.object(storage.subprocess, "run") as run:
            run.return_value.stdout = '{"filesystems": [{"fs-options": "rw"}]}'
            self.assertEqual(storage.mount("/")["fs-options"], "rw")
            self.assertIn("TARGET,FSTYPE,FS-OPTIONS", run.call_args.args[0])
