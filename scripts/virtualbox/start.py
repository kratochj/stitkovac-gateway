#!/usr/bin/env python3
"""Start the existing lab and expose its HTTPS listeners through loopback SSH."""
import json
from pathlib import Path
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parents[2]
OUT = ROOT / 'dist/virtualbox'
CONFIG = json.loads((OUT / 'lab.json').read_text())
SSH = ['ssh', '-i', str(OUT / 'id_ed25519'), '-p', str(CONFIG['ssh_port']),
       '-o', 'IdentitiesOnly=yes', '-o', 'StrictHostKeyChecking=accept-new',
       '-o', 'UserKnownHostsFile=' + str(OUT / 'known_hosts'),
       '-o', 'ConnectTimeout=3', '-o', 'BatchMode=yes']
SOCKET = str(OUT / 'tunnel.sock')
HOST = 'technik@127.0.0.1'

def main():
    info = subprocess.check_output(['VBoxManage', 'showvminfo', CONFIG['name'], '--machinereadable'], text=True)
    if 'VMState="running"' not in info:
        subprocess.run(['VBoxManage', 'startvm', CONFIG['name'], '--type', 'headless'], check=True)
    deadline = time.monotonic() + 120
    while subprocess.run(SSH + [HOST, 'true'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode:
        if time.monotonic() > deadline:
            raise SystemExit('VM did not become reachable by SSH within 120 seconds')
        time.sleep(1)
    active = subprocess.run(SSH + ['-S', SOCKET, '-O', 'check', HOST], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0
    if not active:
        subprocess.run(SSH + ['-M', '-S', SOCKET, '-fNT', '-o', 'ExitOnForwardFailure=yes',
                              '-o', 'ServerAliveInterval=15', '-o', 'ServerAliveCountMax=3',
                              '-L', '127.0.0.1:8443:127.0.0.1:8443',
                              '-L', '127.0.0.1:9443:127.0.0.1:9443', HOST], check=True)
    for name, remote in [('gateway-cert.pem', '/data/gateway/tls.crt'), ('lab-cert.pem', '/data/lab/tls.crt')]:
        certificate = subprocess.check_output(SSH + [HOST, 'sudo cat ' + remote])
        (OUT / name).write_bytes(certificate)
    print('Laboratoř: https://127.0.0.1:9443/\nAdministrace brány: https://127.0.0.1:8443/')
    print('Přihlašovací údaje: ' + str(OUT / 'PRISTUPY.txt'))
    if '--open' in sys.argv and sys.platform == 'darwin':
        subprocess.run(['open', 'https://127.0.0.1:9443/'], check=True)

if __name__ == '__main__':
    main()
