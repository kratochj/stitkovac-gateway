#!/usr/bin/env python3
"""Cleanly shut down the owned test VM and its SSH tunnel."""
import subprocess
from start import CONFIG, SSH, HOST, SOCKET

result = subprocess.run(SSH + [HOST, 'sudo systemctl poweroff'])
subprocess.run(SSH + ['-S', SOCKET, '-O', 'exit', HOST], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
if result.returncode not in (0, 255):
    raise SystemExit(result.returncode)
print('VM dostala pokyn k vypnutí: ' + CONFIG['name'])
