import hashlib
import importlib.machinery
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

loader = importlib.machinery.SourceFileLoader('health', str(Path(__file__).with_name('platform-health')))
spec = importlib.util.spec_from_loader(loader.name, loader)
health = importlib.util.module_from_spec(spec)
loader.exec_module(health)

ROOT = json.dumps({'filesystems': [{'fstype': 'ext4', 'fs-options': 'ro'}]})
DATA = json.dumps({'filesystems': [{'target': '/data', 'fstype': 'ext4', 'fs-options': 'rw'}]})


class RecoveryTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        (self.base / 'recovery').mkdir()
        self.expected = {}
        for name in health.NAMES:
            content = (name + '-trusted').encode()
            value = hashlib.sha256(content).hexdigest()
            self.expected[name] = value
            (self.base / name).write_bytes(content)
            (self.base / 'recovery' / value).write_bytes(content)
        (self.base / 'recovery.json').write_text(json.dumps(self.expected))
        # Ownership is tested independently; fixtures also run without root on macOS.
        self.trust = patch.object(health, 'trusted_file').start()
        self.addCleanup(patch.stopall)
        self.events = patch.object(health, 'safe_record').start()
        self.command = patch.object(health, 'run', return_value=ROOT).start()

    def corrupt(self, name='gateway-launcher'):
        (self.base / name).write_bytes(b'corrupt')

    def test_healthy_does_not_remount(self):
        with patch.object(health, 'atomic_write') as write:
            health.recover(self.base)
            write.assert_not_called()
            self.command.assert_not_called()

    def test_both_damaged_files_are_restored_and_root_relocked(self):
        for name in health.NAMES:
            self.corrupt(name)
        transitions = []
        health.recover(self.base, transitions.append)
        self.assertEqual(['rw', 'ro'], transitions)
        for name, value in self.expected.items():
            self.assertEqual(value, health.digest(self.base / name))
            self.assertEqual(0o755, (self.base / name).stat().st_mode & 0o777)

    def test_missing_executable_is_restored(self):
        (self.base / 'gateway-update').unlink()
        health.recover(self.base, lambda mode: None)
        self.assertEqual(self.expected['gateway-update'], health.digest(self.base / 'gateway-update'))

    def test_bad_second_backup_prevents_all_writes(self):
        for name in health.NAMES:
            self.corrupt(name)
        (self.base / 'recovery' / self.expected['gateway-update']).write_bytes(b'bad')
        transitions = []
        with self.assertRaisesRegex(ValueError, 'checksum mismatch'):
            health.recover(self.base, transitions.append)
        self.assertEqual([], transitions)
        self.assertEqual(b'corrupt', (self.base / 'gateway-launcher').read_bytes())

    def test_failed_write_always_relocks_root(self):
        self.corrupt()
        transitions = []
        with patch.object(health, 'atomic_write', side_effect=OSError('disk full')):
            with self.assertRaises(OSError):
                health.recover(self.base, transitions.append)
        self.assertEqual(['rw', 'ro'], transitions)

    def test_failed_remount_still_attempts_read_only(self):
        self.corrupt()
        transitions = []
        def remount(mode):
            transitions.append(mode)
            if mode == 'rw':
                raise OSError('failed')
        with self.assertRaises(OSError):
            health.recover(self.base, remount)
        self.assertEqual(['rw', 'ro'], transitions)

    def test_writable_root_is_not_modified(self):
        self.corrupt()
        self.command.return_value = ROOT.replace('ro', 'rw')
        with self.assertRaisesRegex(ValueError, 'read-only'):
            health.recover(self.base)

    def test_failed_read_only_remount_blocks_success(self):
        self.corrupt()
        def remount(mode):
            if mode == 'ro':
                raise OSError('cannot restore read-only')
        with self.assertRaises(OSError):
            health.recover(self.base, remount)
        self.assertNotIn('integrity-restored', [call.args[0] for call in self.events.call_args_list])

    def test_root_must_really_be_read_only_after_remount(self):
        self.corrupt()
        self.command.side_effect = [ROOT, ROOT.replace('ro', 'rw')]
        with self.assertRaisesRegex(ValueError, 'remains writable'):
            health.recover(self.base, lambda mode: None)

    def test_malformed_manifest_fails_closed(self):
        (self.base / 'recovery.json').write_text('{"gateway-launcher":"../../evil"}')
        with self.assertRaisesRegex(ValueError, 'manifest'):
            health.recover(self.base)
        self.command.assert_not_called()

    def test_atomic_failure_preserves_original(self):
        target = self.base / 'gateway-launcher'
        old = target.read_bytes()
        with patch.object(health.os, 'replace', side_effect=OSError('failure')):
            with self.assertRaises(OSError):
                health.atomic_write(target, b'new', 0o755)
        self.assertEqual(old, target.read_bytes())
        self.assertEqual([], list(self.base.glob('.gateway-launcher-*')))


class DiagnosticsTest(unittest.TestCase):
    def test_retention_keeps_incidents_when_snapshots_rotate(self):
        with tempfile.TemporaryDirectory() as tmp, \
                patch.object(health, 'RECORDS', Path(tmp) / 'events.json'), \
                patch.object(health, 'run', return_value=DATA):
            # Supply Linux proc metadata on macOS without mocking log reads.
            original = Path.read_text
            def read(path, *args, **kwargs):
                if str(path).startswith('/proc/'):
                    return 'test-boot' if 'boot_id' in str(path) else '123.45 0'
                return original(path, *args, **kwargs)
            with patch.object(Path, 'read_text', read):
                health.record('integrity-restored')
                for index in range(140):
                    health.record('snapshot', index=index)
            result = json.loads(health.RECORDS.read_text())
            self.assertEqual(128, len(result['snapshots']))
            self.assertEqual(12, result['snapshots'][0]['index'])
            self.assertEqual('integrity-restored', result['events'][0]['event'])
            self.assertLess(health.RECORDS.stat().st_size, health.MAX_LOG_BYTES)
            self.assertEqual(0o600, health.RECORDS.stat().st_mode & 0o777)

    def test_missing_data_mount_never_writes(self):
        with patch.object(health, 'run', return_value=DATA.replace('"/data"', '"/"')), \
                patch.object(health, 'atomic_write') as write:
            with self.assertRaises(ValueError):
                health.record('snapshot')
            write.assert_not_called()

    def test_snapshot_does_not_store_raw_kernel_messages_or_environment(self):
        outputs = ['ActiveState=failed\nExecMainStatus=11\nUnexpected=secret',
                   'mmc0: I/O error token=secret\nUnder-voltage detected!',
                   'throttled=0x50000']
        with patch.object(health, 'run', side_effect=outputs), \
                patch.object(health, 'safe_record') as event, \
                patch.dict(os.environ, {'TOKEN': 'secret', 'SERVICE_RESULT': 'signal', 'EXIT_STATUS': 'SEGV'}):
            health.snapshot()
        fields = event.call_args.kwargs
        self.assertNotIn('secret', json.dumps(fields))
        self.assertEqual('11', fields['service']['ExecMainStatus'])
        self.assertEqual('SEGV', fields['exit_status'])
        self.assertEqual(1, fields['kernel_tail_counts']['io_error'])

    def test_diagnostic_failure_is_nonfatal(self):
        with patch.object(health, 'record', side_effect=OSError('full')):
            health.safe_record('snapshot')

    def test_symlink_is_not_a_trusted_recovery_file(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'link'
            path.symlink_to('/etc/hosts')
            with self.assertRaises(ValueError):
                health.trusted_file(path)


if __name__ == '__main__':
    unittest.main()
