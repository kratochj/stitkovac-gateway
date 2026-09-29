#!/usr/bin/env python3
"""Wait for clean shutdown and export the lab without its installation seed."""
import hashlib
import json
from pathlib import Path
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parents[2]
OUT = ROOT / 'dist/virtualbox'
CONFIG = json.loads((OUT / 'lab.json').read_text())
deadline = time.monotonic() + 120
while True:
    # VBox can briefly reject the info query while its shutdown session unlocks.
    result = subprocess.run(['VBoxManage', 'showvminfo', CONFIG['name'], '--machinereadable'],
                            text=True, capture_output=True)
    if result.returncode == 0 and 'VMState="poweroff"' in result.stdout:
        break
    if time.monotonic() > deadline:
        raise SystemExit('VM is still running; shut it down before exporting')
    time.sleep(2)
if '--no-export' in sys.argv:
    raise SystemExit(0)
archive = OUT / 'stitkovac-gateway-lab-arm64.ova'
if archive.exists():
    raise SystemExit('Export already exists; move it aside explicitly before exporting again')
subprocess.run(['VBoxManage', 'storageattach', CONFIG['name'], '--storagectl', 'VirtioSCSI',
                '--port', '2', '--device', '0', '--type', 'dvddrive', '--medium', 'none'], check=True)
subprocess.run(['VBoxManage', 'export', CONFIG['name'], '--output', str(archive),
                '--ovf20', '--manifest', '--vsys', '0', '--product', 'Stitkovac Gateway Lab',
                '--version', '0.1.0', '--description', 'ARM64 Debian lab with isolated WSS server and DHCP/TCP PDF printer simulator.'], check=True)
with archive.open('rb') as handle:
    digest = hashlib.file_digest(handle, 'sha256').hexdigest()
archive.with_suffix('.ova.sha256').write_text(digest + '  ' + archive.name + '\n')
print(archive)
