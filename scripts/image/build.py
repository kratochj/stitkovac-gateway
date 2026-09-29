#!/usr/bin/env python3
"""Build a generic appliance image from a pinned, uncompressed RPi OS Lite image.

Linux ARM64 + root required. All partition writes target a newly created regular
output file through its own loop device; physical disks are never accepted.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import tempfile

REPO = Path(__file__).resolve().parents[2]


def run(*args, **kwargs):
    return subprocess.run(args, check=True, **kwargs)


def output(*args):
    return subprocess.check_output(args, text=True).strip()


def validate(source, destination, checksum):
    if source.is_symlink() or not source.is_file():
        raise ValueError('Input must be a regular uncompressed image file')
    if destination.exists() or destination.is_symlink():
        raise ValueError('Output already exists; refusing to overwrite it')
    with source.open('rb') as stream:
        digest = hashlib.file_digest(stream, 'sha256').hexdigest()
    if len(checksum) != 64 or digest != checksum:
        raise ValueError('Base image SHA-256 mismatch')


def main():
    p = argparse.ArgumentParser()
    p.add_argument('--base', type=Path, required=True)
    p.add_argument('--sha256', required=True)
    p.add_argument('--output', type=Path, required=True)
    p.add_argument('--validate-only', action='store_true')
    args = p.parse_args()
    validate(args.base, args.output, args.sha256)
    for name in ('gateway', 'gateway-launcher', 'gateway-update', 'gateway-network'):
        if not (REPO / f'bin/{name}-linux-arm64').is_file():
            raise ValueError('Build all four ARM64 tools first')
    if args.validate_only:
        print('Base checksum, output path and binaries verified; no image written.')
        return
    if platform.system() != 'Linux' or platform.machine() != 'aarch64' or os.geteuid() != 0:
        raise ValueError('Use a dedicated Linux ARM64 image-build host as root')
    for command in ('losetup', 'sfdisk', 'mount', 'umount', 'chroot', 'mkfs.ext4', 'resize2fs', 'e2fsck'):
        if not shutil.which(command):
            raise ValueError('Missing image-build dependency: ' + command)
    # Copy into an exclusive file. Never attach an existing device or the source image.
    with args.output.open('xb') as target, args.base.open('rb') as source:
        shutil.copyfileobj(source, target)
    loop = None
    mounted = []
    try:
        table = json.loads(output('sfdisk', '--json', str(args.output)))['partitiontable']
        parts = table['partitions']
        if table['label'] != 'dos' or table.get('sectorsize', 512) != 512 or len(parts) != 2 or parts[1]['type'] != '83':
            raise ValueError('Expected a two-partition Raspberry Pi OS MBR image')
        # Grow the root by 2 GiB for offline packages, append a separate 2 GiB data partition.
        root_size = parts[1]['size'] + 2 * 1024**3 // 512
        data_start = ((parts[1]['start'] + root_size + 2047) // 2048) * 2048
        data_size = 2 * 1024**3 // 512
        with args.output.open('r+b') as image:
            image.truncate((data_start + data_size) * 512)
        boot_flag = ', bootable' if parts[0].get('bootable') else ''
        layout = f"label: dos\nlabel-id: {table['id']}\nunit: sectors\n\nstart={parts[0]['start']}, size={parts[0]['size']}, type={parts[0]['type']}{boot_flag}\nstart={parts[1]['start']}, size={root_size}, type=83\nstart={data_start}, size={data_size}, type=83\n"
        run('sfdisk', str(args.output), input=layout, text=True, stdout=subprocess.DEVNULL)
        loop = output('losetup', '--find', '--show', '--partscan', str(args.output))
        check = subprocess.run(['e2fsck', '-pf', loop + 'p2'], stdout=subprocess.DEVNULL)
        if check.returncode not in (0, 1):
            raise ValueError('Base root filesystem failed its check')
        run('resize2fs', loop + 'p2', stdout=subprocess.DEVNULL)
        run('mkfs.ext4', '-q', '-L', 'GWDATA', loop + 'p3')
        with tempfile.TemporaryDirectory(prefix='gateway-image-') as temp:
            root = Path(temp)
            def mount(device, target, *options):
                target.mkdir(parents=True, exist_ok=True)
                run('mount', *options, device, str(target))
                mounted.append(target)
            mount(loop + 'p2', root)
            mount(loop + 'p1', root / 'boot/firmware')
            for name in ('dev', 'proc', 'sys'):
                mount('/' + name, root / name, '--bind')
            if (root / 'data/gateway/gateway.db').exists() or list((root / 'etc/NetworkManager/system-connections').glob('*.nmconnection')):
                raise ValueError('Use a pristine vendor image, not an installed appliance')
            # Prevent package scripts from starting daemons inside the image.
            policy = root / 'usr/sbin/policy-rc.d'
            policy.write_text('#!/bin/sh\nexit 101\n'); policy.chmod(0o755)
            resolver = root / 'etc/resolv.conf'
            resolver.unlink(missing_ok=True)
            resolver.write_text(Path('/etc/resolv.conf').read_text())
            run('chroot', str(root), 'apt-get', 'update')
            run('chroot', str(root), 'env', 'DEBIAN_FRONTEND=noninteractive', 'apt-get', 'install', '-y', '--no-install-recommends',
                'ansible-core', 'network-manager', 'iw', 'dnsmasq-base', 'nftables', 'openssh-server', 'sudo', 'python3', 'util-linux')
            run('chroot', str(root), 'apt-get', 'clean')
            bundle = root / 'usr/lib/stitkovac-image'
            shutil.copytree(REPO / 'deploy/ansible', bundle / 'deploy/ansible')
            shutil.copytree(REPO / 'deploy/systemd', bundle / 'deploy/systemd')
            (bundle / 'bin').mkdir()
            for name in ('gateway', 'gateway-launcher', 'gateway-update', 'gateway-network'):
                shutil.copy2(REPO / f'bin/{name}-linux-arm64', bundle / f'bin/{name}-linux-arm64')
            shutil.copy2(REPO / 'deploy/image/firstboot.py', bundle / 'firstboot.py')
            (bundle / 'firstboot.py').chmod(0o755)
            (bundle / 'inventory.yml').write_text('gateways:\n  hosts:\n    localhost:\n      ansible_connection: local\n')
            shutil.copy2(REPO / 'deploy/image/firstboot.service', root / 'etc/systemd/system/stitkovac-firstboot.service')
            run('chroot', str(root), 'systemctl', 'enable', 'stitkovac-firstboot.service')
            for unit in ('ssh.service', 'ssh.socket', 'apt-daily.timer', 'apt-daily-upgrade.timer', 'dphys-swapfile.service', 'resize2fs_once.service', 'userconfig.service'):
                run('chroot', str(root), 'systemctl', 'mask', unit)
            # SSH is enabled only after unique keys and access policy exist.
            run('chroot', str(root), 'systemctl', 'unmask', 'ssh.service')
            run('chroot', str(root), 'systemctl', 'disable', 'ssh.service')
            boot = root / 'boot/firmware'
            command_line = (boot / 'cmdline.txt').read_text().split()
            (boot / 'cmdline.txt').write_text(' '.join(x for x in command_line if not x.startswith(('init=', 'systemd.run=', 'systemd.run_success_action=', 'systemd.unit='))) + '\n')
            with (root / 'etc/fstab').open('a') as f:
                f.write('\nLABEL=GWDATA /data ext4 defaults,noatime 0 2\n')
            (root / 'data').mkdir(exist_ok=True)
            # Clone-safe image: remove machine and SSH identities created by apt/base OS.
            (root / 'etc/machine-id').write_text('')
            (root / 'var/lib/dbus/machine-id').unlink(missing_ok=True)
            for key in (root / 'etc/ssh').glob('ssh_host_*'):
                key.unlink()
            for seed in ('var/lib/systemd/random-seed', 'boot/firmware/random-seed'):
                (root / seed).unlink(missing_ok=True)
            policy.unlink()
            # Unmount inside the temporary directory lifetime.
            for target in reversed(mounted):
                run('umount', str(target))
            mounted.clear()
        run('losetup', '-d', loop); loop = None
        with args.output.open('rb') as stream:
            digest = hashlib.file_digest(stream, 'sha256').hexdigest()
        args.output.with_suffix('.json').write_text(json.dumps({'baseSha256': args.sha256, 'sha256': digest, 'architecture': 'arm64', 'personalized': False}, indent=2) + '\n')
        print('Generic image built. Add per-device gateway-provision.json only to the flashed card.')
    finally:
        for target in reversed(mounted):
            subprocess.run(['umount', str(target)], check=False)
        if loop:
            subprocess.run(['losetup', '-d', loop], check=False)


if __name__ == '__main__':
    main()
