#!/usr/bin/env python3
"""Prepare per-device credentials on an already flashed, mounted boot partition."""
import argparse
import json
import os
from pathlib import Path
import secrets
import subprocess


def prepare(boot, access):
    if not boot.is_dir() or not (boot / 'gateway-image.json').is_file():
        raise ValueError('Select the mounted boot partition of a Stitkovac gateway image')
    marker = json.loads((boot / 'gateway-image.json').read_text())
    if marker.get('product') != 'stitkovac-gateway' or marker.get('personalized') is not False:
        raise ValueError('Unsupported gateway image marker')
    seed = boot / 'gateway-provision.json'
    if seed.exists() or seed.is_symlink():
        raise ValueError('Provisioning already exists; refusing to replace device credentials')
    access.mkdir(mode=0o700, parents=False, exist_ok=False)
    password = secrets.token_urlsafe(24)
    key = access / 'technik_ed25519'
    subprocess.run(['ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-C', 'gateway-technician', '-f', str(key)], check=True)
    public = key.with_suffix('.pub').read_text().strip()
    # Keep the only copy of the private key on the technician's computer.
    credentials = access / 'PRISTUPY.txt'
    with credentials.open('x') as stream:
        os.chmod(credentials, 0o600)
        stream.write('Administrace: https://192.168.77.1:8443/\nHeslo: ' + password + '\n'
                     'SSH: technik@192.168.77.1\nPrivátní klíč: ' + str(key.resolve()) + '\n')
        stream.flush()
        os.fsync(stream.fileno())
    fd = os.open(seed, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as stream:
        json.dump({'admin_password': password, 'ssh_public_key': public, 'country': 'CZ'}, stream)
        stream.write('\n')
        stream.flush()
        os.fsync(stream.fileno())
    return credentials


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('boot', type=Path, help='Mounted boot partition, e.g. /Volumes/bootfs')
    parser.add_argument('--access-dir', type=Path, required=True, help='New private directory on the technician computer')
    args = parser.parse_args()
    boot, access = args.boot.resolve(), args.access_dir.resolve()
    if access == boot or boot in access.parents:
        raise ValueError('Private access files must not be stored on the SD card')
    print('Přístupy jsou uložené v: ' + str(prepare(boot, access)))
    print('Bezpečně vysuň kartu. První inicializace skončí automatickým restartem.')


if __name__ == '__main__':
    main()
