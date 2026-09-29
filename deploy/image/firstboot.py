#!/usr/bin/python3
"""Per-device initialization. Generic images contain no identities or credentials."""
import json
import os
from pathlib import Path
import shutil
import subprocess


def run(*args):
    subprocess.run(args, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def write(path, value, mode=0o600):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(path.name + '.new')
    with temporary.open('w') as stream:
        os.chmod(temporary, mode)
        stream.write(value)
        stream.flush()
        os.fsync(stream.fileno())
    temporary.replace(path)
    fd = os.open(path.parent, os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def validate(seed):
    if set(seed) != {'admin_password', 'ssh_public_key', 'country'}:
        raise ValueError('Unexpected provisioning fields')
    if not isinstance(seed['admin_password'], str) or not 16 <= len(seed['admin_password'].encode()) <= 1024:
        raise ValueError('Invalid provisioning password')
    key = seed['ssh_public_key']
    import re
    if not isinstance(key, str) or not re.fullmatch(r'ssh-ed25519 [A-Za-z0-9+/]+={0,2}(?: [A-Za-z0-9_.@ -]+)?', key):
        raise ValueError('Expected one Ed25519 SSH public key')
    if not isinstance(seed['country'], str) or not re.fullmatch('[A-Z]{2}', seed['country']):
        raise ValueError('Invalid regulatory country')
    return seed


def main():
    if Path('/data/gateway-image-ready').exists():
        if Path('/boot/firmware/gateway-provision.json').exists():
            run('mount', '-o', 'remount,rw', '/boot/firmware')
            Path('/boot/firmware/gateway-provision.json').unlink()
            os.sync()
            run('mount', '-o', 'remount,ro', '/boot/firmware')
        Path('/data/gateway-bootstrap.json').unlink(missing_ok=True)
        return
    run('mountpoint', '-q', '/data')
    run('mount', '-o', 'remount,rw', '/')
    run('mount', '-o', 'remount,rw', '/boot/firmware')
    seed_path = Path('/data/gateway-bootstrap.json')
    if not seed_path.exists():
        seed = validate(json.loads(Path('/boot/firmware/gateway-provision.json').read_text()))
        write(seed_path, json.dumps(seed))
    seed = validate(json.loads(seed_path.read_text()))
    # The secret is never passed in argv or left in a log.
    variables = {'gateway_admin_password': seed['admin_password'], 'gateway_country': seed['country'],
                 'gateway_network_provision': True, 'gateway_install_network_packages': False}
    write('/run/gateway-bootstrap-vars.json', json.dumps(variables))
    try:
        run('ansible-playbook', '-i', '/usr/lib/stitkovac-image/inventory.yml',
            '/usr/lib/stitkovac-image/deploy/ansible/bootstrap.yml', '-e', '@/run/gateway-bootstrap-vars.json')
        run('ansible-playbook', '-i', '/usr/lib/stitkovac-image/inventory.yml',
            '/usr/lib/stitkovac-image/deploy/ansible/network.yml', '-e', '@/run/gateway-bootstrap-vars.json')
    finally:
        Path('/run/gateway-bootstrap-vars.json').unlink(missing_ok=True)
    Path('/data/access').mkdir(exist_ok=True, mode=0o755)
    os.chmod('/data/access', 0o755)
    write('/data/access/authorized_keys', seed['ssh_public_key'] + '\n', 0o644)
    if subprocess.run(['id', 'technik'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode:
        run('useradd', '--create-home', '--shell', '/bin/bash', 'technik')
    write('/etc/sudoers.d/gateway-technik', 'technik ALL=(ALL) NOPASSWD: ALL\n', 0o440)
    run('visudo', '-cf', '/etc/sudoers.d/gateway-technik')
    Path('/data/ssh').mkdir(exist_ok=True, mode=0o700)
    host = Path('/data/ssh/ssh_host_ed25519_key')
    if not host.exists():
        run('ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-f', str(host))
    write('/etc/ssh/sshd_config.d/00-gateway.conf', 'PasswordAuthentication no\nKbdInteractiveAuthentication no\nPermitRootLogin no\nAllowUsers technik\nAuthorizedKeysFile /data/access/authorized_keys\nHostKey /data/ssh/ssh_host_ed25519_key\n', 0o644)
    # A failed/retried first boot never replaces persistent OS identities.
    if not Path('/data/machine-id').exists():
        write('/data/machine-id', Path('/etc/machine-id').read_text(), 0o444)
    if not Path('/data/systemd').exists():
        shutil.copytree('/var/lib/systemd', '/data/systemd')
    entries = Path('/etc/fstab').read_text().splitlines()
    final = []
    for line in entries:
        fields = line.split()
        if fields and not line.startswith('#') and len(fields) >= 4:
            if fields[1] in ('/', '/boot/firmware'):
                fields[3] = ','.join(x for x in fields[3].split(',') if x not in ('rw', 'ro')) + ',ro'
                line = '\t'.join(fields)
        final.append(line)
    for line in ['tmpfs /tmp tmpfs defaults,nosuid,nodev,mode=1777,size=128M 0 0',
                 'tmpfs /var/tmp tmpfs defaults,nosuid,nodev,mode=1777,size=64M 0 0',
                 'tmpfs /var/log tmpfs defaults,nosuid,nodev,mode=0755,size=32M 0 0',
                 '/data/machine-id /etc/machine-id none bind 0 0',
                 '/data/systemd /var/lib/systemd none bind 0 0']:
        if line not in final:
            final.append(line)
    write('/etc/fstab', '\n'.join(final) + '\n', 0o644)
    write('/etc/systemd/journald.conf.d/gateway.conf', '[Journal]\nStorage=volatile\nRuntimeMaxUse=32M\n', 0o644)
    write('/etc/systemd/system/stitkovac-gateway.service.d/image.conf', '[Unit]\nConditionPathExists=/data/gateway-image-ready\n', 0o644)
    run('systemctl', 'enable', 'stitkovac-gateway.service', 'ssh.service')
    os.sync()
    write('/data/gateway-image-ready', '1\n')
    Path('/boot/firmware/gateway-provision.json').unlink(missing_ok=True)
    seed_path.unlink()
    os.sync()
    run('systemctl', '--no-block', 'reboot')


if __name__ == '__main__':
    try:
        main()
    except Exception:
        # Do not expose Ansible output or provisioning secrets to the journal.
        raise SystemExit('Gateway initialization failed; inspect provisioning input and storage from the local console.')
