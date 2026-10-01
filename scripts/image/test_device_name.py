"""Permanent Wi-Fi MAC selection must survive randomized interface addresses."""
import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('device_name', Path(__file__).resolve().parents[2] / 'deploy/ansible/files/device-name.py')
identity = importlib.util.module_from_spec(spec)
spec.loader.exec_module(identity)


class DeviceNameTests(unittest.TestCase):
    def test_name_uses_exact_last_two_bytes_in_lowercase(self):
        self.assertEqual(identity.device_name("B8:27:EB:E3:9A:E3"), "stitkovac-gw-9a-e3")
        self.assertEqual(identity.device_name("b8:27:eb:e3:00:01"), "stitkovac-gw-00-01")

    def test_permanent_address_wins_over_randomized_address(self):
        result = subprocess.CompletedProcess([], 0, json.dumps([
            {'ifname': 'wlan0', 'address': '02:11:22:33:44:55', 'permaddr': 'B8:27:EB:E3:9A:E3'}]))
        with patch.object(identity.subprocess, 'run', return_value=result):
            self.assertEqual(identity.permanent_mac('wlan0'), 'b8:27:eb:e3:9a:e3')

    def test_kernel_marked_permanent_address_and_randomization_rejection(self):
        result = subprocess.CompletedProcess([], 0, '[{"ifname":"wlan0"}]')
        with tempfile.TemporaryDirectory() as tmp, patch.object(identity.subprocess, 'run', return_value=result):
            root = Path(tmp)
            (root / 'wlan0').mkdir()
            (root / 'wlan0/address').write_text('b8:27:eb:e3:9a:e3\n')
            (root / 'wlan0/addr_assign_type').write_text('0\n')
            self.assertEqual(identity.permanent_mac('wlan0', root), 'b8:27:eb:e3:9a:e3')
            for kind in ('1', '2', '3'):
                (root / 'wlan0/addr_assign_type').write_text(kind)
                with self.assertRaises(ValueError):
                    identity.permanent_mac('wlan0', root)

    def test_reject_invalid_and_non_unicast_mac(self):
        for value in ('', '00:00:00:00:00:00', 'ff:ff:ff:ff:ff:ff', '01:11:22:33:44:55', 'b8:27:eb:9a:e3', '$(hostname)'):
            with self.subTest(value=value), self.assertRaises(ValueError):
                identity.validate_mac(value)

    def test_interface_validation_precedes_command(self):
        with patch.object(identity.subprocess, 'run') as run:
            with self.assertRaises(ValueError):
                identity.permanent_mac('../eth0')
            run.assert_not_called()
