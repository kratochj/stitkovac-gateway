#!/usr/bin/env python3
"""Host-independent checks for clone safety and destructive-operation guards."""
import hashlib
import importlib.util
from pathlib import Path
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


build = load('image_build', ROOT / 'scripts/image/build.py')
firstboot = load('firstboot', ROOT / 'deploy/image/firstboot.py')


class ImageTests(unittest.TestCase):
    def test_builder_refuses_devices_symlinks_existing_output_and_bad_digest(self):
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp) / 'base.img'
            base.write_bytes(b'base')
            output = Path(tmp) / 'new.img'
            digest = hashlib.sha256(b'base').hexdigest()
            build.validate(base, output, digest)
            self.assertFalse(output.exists())
            with self.assertRaises(ValueError):
                build.validate(base, output, '0' * 64)
            link = Path(tmp) / 'link.img'
            link.symlink_to(base)
            with self.assertRaises(ValueError):
                build.validate(link, output, digest)
            output.write_bytes(b'keep')
            with self.assertRaises(ValueError):
                build.validate(base, output, digest)
            self.assertEqual(output.read_bytes(), b'keep')
            with self.assertRaises(ValueError):
                build.validate(Path('/dev/null'), Path(tmp) / 'other.img', digest)

    def test_seed_is_explicit_and_never_accepts_cloud_identity_or_shell_input(self):
        seed = {'admin_password': 'unique provision password', 'ssh_public_key': 'ssh-ed25519 AAAATEST fixture', 'country': 'CZ'}
        self.assertEqual(firstboot.validate(seed), seed)
        for invalid in [seed | {'token': 'secret'}, seed | {'country': 'CZ\ncommand'},
                        seed | {'ssh_public_key': 'command="something" ssh-ed25519 AAAA'},
                        seed | {'ssh_public_key': 'ssh-ed25519 AAAA\nssh-ed25519 BBBB'},
                        seed | {'admin_password': 'short'}]:
            with self.assertRaises(ValueError):
                firstboot.validate(invalid)

    def test_atomic_configuration_write_has_private_permissions(self):
        with tempfile.TemporaryDirectory() as tmp:
            target = Path(tmp) / 'nested/secret.json'
            firstboot.write(target, 'first')
            firstboot.write(target, 'second')
            self.assertEqual(target.read_text(), 'second')
            self.assertEqual(target.stat().st_mode & 0o777, 0o600)
            self.assertFalse(target.with_suffix('.json.new').exists())


if __name__ == '__main__':
    unittest.main()
