#!/usr/bin/env python3
"""Create an isolated ARM64 VirtualBox lab from a pinned Debian cloud image."""
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[2]
OUT = ROOT / 'dist/virtualbox'
NAME = 'Stitkovac Gateway Lab'
IMAGE = ROOT / '.cache/virtualbox/debian-13-generic-arm64-20260914-2601.qcow2'
DIGEST = 'bef60fa5c4f5511cf83fe1ade0279edd5318dccbbb44f14eb0aa7f3b467e038d7a7c0b6f837be0ad1c3b2e06c5ed8e27adfdf0fd111a201b8f4c9347cd414863'

def run(*args, **kwargs):
    return subprocess.run([str(x) for x in args], check=True, **kwargs)

def main():
    os.umask(0o077)
    OUT.mkdir(parents=True, exist_ok=True)
    if (OUT / 'lab.json').exists():
        raise SystemExit('Lab already exists; use its start/stop commands instead of replacing disks.')
    with IMAGE.open('rb') as image:
        if hashlib.file_digest(image, 'sha512').hexdigest() != DIGEST:
            raise SystemExit('Debian image SHA-512 mismatch')
    qemu = shutil.which('qemu-img')
    if not qemu:
        raise SystemExit('qemu-img is required')
    run('ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-C', 'gateway-lab-only', '-f', OUT / 'id_ed25519')
    public = (OUT / 'id_ed25519.pub').read_text().strip()
    seed = OUT / 'seed'
    seed.mkdir()
    user = {'hostname': 'stitkovac-gateway-lab', 'manage_etc_hosts': True,
            'users': [{'name': 'technik', 'groups': ['sudo'], 'shell': '/bin/bash',
                       'sudo': 'ALL=(ALL) NOPASSWD:ALL', 'lock_passwd': True,
                       'ssh_authorized_keys': [public]}],
            'ssh_pwauth': False, 'disable_root': True,
            'bootcmd': [['resolvectl', 'dns', 'enp0s8', '1.1.1.1']],
            'package_update': True,
            'packages': ['python3', 'sudo', 'busybox', 'iproute2', 'ca-certificates', 'curl'],
            'runcmd': [['touch', '/var/lib/gateway-lab-cloud-init-ready']]}
    # JSON is a YAML subset; avoid quoting secrets into shell commands.
    (seed / 'user-data').write_text('#cloud-config\n' + json.dumps(user))
    (seed / 'meta-data').write_text('instance-id: stitkovac-gateway-lab-1\nlocal-hostname: stitkovac-gateway-lab\n')
    iso = OUT / 'seed.iso'
    if sys.platform == 'darwin':
        run('hdiutil', 'makehybrid', '-o', iso, seed, '-iso', '-joliet', '-default-volume-name', 'cidata')
    else:
        run('xorriso', '-as', 'mkisofs', '-output', iso, '-volid', 'cidata', '-joliet', '-rock', seed)
    disk = OUT / 'system.vdi'
    run(qemu, 'convert', '-f', 'qcow2', '-O', 'vdi', IMAGE, disk)
    run('VBoxManage', 'modifymedium', 'disk', disk, '--resize', '8192')
    run('VBoxManage', 'createmedium', 'disk', '--filename', OUT / 'data.vdi', '--size', '2048', '--format', 'VDI')
    run('VBoxManage', 'createvm', '--name', NAME, '--platform-architecture', 'arm', '--ostype', 'Debian13_arm64', '--default', '--basefolder', OUT / 'vm', '--register')
    (OUT / 'lab.json').write_text(json.dumps({'name': NAME, 'ssh_port': 2222, 'architecture': 'arm64', 'debian_sha512': DIGEST}, indent=2)+'\n')
    run('VBoxManage', 'modifyvm', NAME, '--memory', '2048', '--cpus', '2', '--nic1', 'nat', '--nic-type1', 'virtio', '--nat-pf1', 'ssh,tcp,127.0.0.1,2222,,22', '--audio-enabled', 'off')
    run('VBoxManage', 'modifyvm', NAME, '--natdnshostresolver1', 'on')
    # ARM defaults provide a VirtIO SCSI controller; inspect and attach explicitly.
    run('VBoxManage', 'storagectl', NAME, '--name', 'VirtioSCSI', '--portcount', '3')
    for port, medium, kind in [(0, disk, 'hdd'), (1, OUT / 'data.vdi', 'hdd'), (2, iso, 'dvddrive')]:
        run('VBoxManage', 'storageattach', NAME, '--storagectl', 'VirtioSCSI', '--port', str(port), '--device', '0', '--type', kind, '--medium', medium)
    run('VBoxManage', 'startvm', NAME, '--type', 'headless')
    print('VM started. Wait for SSH on 127.0.0.1:2222, then run the lab Ansible playbook.')

if __name__ == '__main__':
    main()
