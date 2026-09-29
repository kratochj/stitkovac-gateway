#!/usr/bin/env python3
"""Write private local access instructions and macOS launchers without logging secrets."""
import json
import os
from pathlib import Path

os.umask(0o077)
ROOT = Path(__file__).resolve().parents[2]
OUT = ROOT / 'dist/virtualbox'
password = json.loads((OUT / 'credentials.json').read_text())['gateway_admin_password']
access = OUT / 'PRISTUPY.txt'
access.write_text(
    'Štítkovač Gateway – soukromá testovací laboratoř\n\n'
    'Spuštění: dvojklik na Spustit.command (ve stejné složce).\n'
    'Vypnutí: dvojklik na Vypnout.command.\n\n'
    'Laboratoř: https://127.0.0.1:9443/\n'
    'Uživatel laboratoře: technik\n'
    'Administrace brány: https://127.0.0.1:8443/\n'
    f'Heslo pro obě webová rozhraní: {password}\n\n'
    'Certifikáty jsou vlastní; fingerprinty viz gateway-cert.pem a lab-cert.pem.\n'
    'SSH používá soukromý klíč id_ed25519; přihlášení heslem je vypnuté.\n'
    'Tato složka a export obsahují přístupy k testovací VM. Nesdílet veřejně.\n'
    'Návod: ../../docs/virtualbox-lab.md\n', encoding='utf-8')
access.chmod(0o600)
for name, script in [('Spustit', 'start.py --open'), ('Vypnout', 'stop.py')]:
    launcher = OUT / (name + '.command')
    launcher.write_text('#!/bin/sh\nset -eu\nexport PATH="/opt/homebrew/bin:/usr/local/bin:$PATH"\n'
                        'cd "$(dirname "$0")/../.."\npython3 scripts/virtualbox/' + script + '\n')
    launcher.chmod(0o700)
print('Private access instructions and launchers prepared.')
