#!/usr/bin/env python3
"""Apply a prepared lab-helper release without changing the gateway OTA selection."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess

ROOT = Path(__file__).resolve().parents[2]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('version', help='Prepared stable release version, for example 0.1.7')
    parser.add_argument('--verify-only', action='store_true', help='Verify local package files without connecting to the VM')
    args = parser.parse_args()
    if not re.fullmatch(r'(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})', args.version):
        parser.error('Use a stable MAJOR.MINOR.PATCH version')
    release = ROOT / 'dist/releases' / args.version / 'lab'
    metadata = json.loads((release / 'release.json').read_text())
    artifact = release / 'gateway-lab'
    with artifact.open('rb') as stream:
        digest = hashlib.file_digest(stream, 'sha256').hexdigest()
    if (metadata['version'] != args.version or metadata['platform'] != 'linux-arm64'
            or artifact.stat().st_size != metadata['size'] or digest != metadata['sha256']):
        parser.error('The lab artifact does not match its release metadata')
    print(f'Lab {args.version}: local size and SHA-256 verified.', flush=True)
    if args.verify_only:
        return
    ansible = shutil.which('ansible-playbook')
    if not ansible:
        parser.error('Install Ansible and ensure ansible-playbook is on PATH')
    variables = {'gateway_lab_version': args.version, 'gateway_lab_binary': str(artifact), 'gateway_lab_sha256': digest}
    env = os.environ.copy()
    env.setdefault('ANSIBLE_LOCAL_TEMP', '/tmp/stitkovac-ansible')
    env.setdefault('ANSIBLE_PIPELINING', 'True')
    subprocess.run([ansible, '-i', str(ROOT / 'deploy/virtualbox/inventory.yml'),
                    str(ROOT / 'deploy/virtualbox/lab.yml'), '-e', json.dumps(variables)],
                   cwd=ROOT, env=env, check=True)


if __name__ == '__main__':
    main()
