#!/usr/bin/env python3
"""Boot a disposable copy under QEMU Pi 3B; never personalize the deliverable."""
import argparse
import base64
import gzip
import json
from pathlib import Path
import shutil
import subprocess
import re
import time

import build


def run_emulator(command, log_path, timeout):
    with log_path.open('w') as log:
        process = subprocess.Popen(command, stdout=log, stderr=subprocess.STDOUT)
        deadline = time.monotonic() + timeout
        try:
            while process.poll() is None:
                text = log_path.read_text(errors='replace')
                text = re.sub(r'\x1b\[[0-?]*[ -/]*[@-~]', '', text)
                if any('Failed to start' in line and 'stitkovac-firstboo' in line for line in text.splitlines()):
                    raise RuntimeError('First boot failed; inspect the disposable disk and console log')
                # Disabling the emulated watchdog also disables the kernel's reset mechanism.
                # The persistent assertions below decide success, not this shutdown message.
                if any(marker in text for marker in ('Reboot failed -- System halted',
                       'reboot: Power down', 'reboot: Power off not available: System halted instead')):
                    return
                if time.monotonic() >= deadline:
                    raise TimeoutError('Emulated boot timed out')
                time.sleep(2)
            if process.returncode:
                raise RuntimeError('Emulator failed; inspect the console log')
        finally:
            if process.poll() is None:
                process.terminate()
                process.wait(timeout=10)


def mounted(image, function):
    loop = build.output('losetup', '--find', '--show', '--partscan', str(image))
    mounts = []
    try:
        for n in (1, 2, 3):
            build.partition_device(loop, n)
        with build.image_mounts(mounts) as root:
            build.run('mount', loop + 'p2', str(root)); mounts.append(root)
            build.run('mount', loop + 'p1', str(root / 'boot/firmware')); mounts.append(root / 'boot/firmware')
            build.run('mount', loop + 'p3', str(root / 'data')); mounts.append(root / 'data')
            function(root)
    finally:
        build.run('losetup', '-d', loop)


def inspect_first_boot(root, work, expected_version):
    assert (root / 'data/gateway-image-ready').read_text().strip() == '1', 'First boot did not complete'
    assert (root / 'data/gateway/gateway.db').is_file()
    assert (root / 'etc/hostname').read_text().strip() == 'stitkovac-gw-9a-e3'
    assert json.loads((root / 'data/network/ap-credentials.json').read_text())['ssid'] == 'stitkovac-gw-9a-e3'
    assert json.loads((root / 'etc/stitkovac-gateway/network.json').read_text())['apSSID'] == 'stitkovac-gw-9a-e3'
    assert not (root / 'data/gateway-bootstrap.json').exists()
    assert not (root / 'boot/firmware/gateway-provision.json').exists()
    selection = json.loads((root / 'data/gateway-releases/selection.json').read_text())
    assert selection['active'] == expected_version
    for line in (root / 'etc/fstab').read_text().splitlines():
        fields = line.split()
        if len(fields) >= 4 and fields[1] in ('/', '/boot/firmware'):
            assert 'ro' in fields[3].split(',')
    (work / 'result.json').write_text(json.dumps({'firstBoot': 'passed', 'otaVersion': selection['active'],
        'target': 'QEMU raspi3b, 1 GB RAM', 'hardwareNetworkingVerified': False}, indent=2) + '\n')
    script = root / 'usr/lib/stitkovac-image/check-boot-smoke.sh'
    script.write_text('#!/bin/sh\n'
                      'if /usr/lib/stitkovac-gateway/check-storage > /data/boot-smoke.log 2>&1; then\n'
                      '  echo passed > /data/boot-smoke-result\nelse\n'
                      '  echo failed > /data/boot-smoke-result\nfi\n'
                      'sync\nsystemctl --no-block poweroff\n')
    script.chmod(0o755)
    unit = root / 'etc/systemd/system/stitkovac-boot-smoke.service'
    unit.write_text('[Unit]\nDescription=Disposable boot smoke test\nAfter=local-fs.target\n'
                    'RequiresMountsFor=/data /boot/firmware\n'
                    '[Service]\nType=oneshot\nExecStart=/usr/lib/stitkovac-image/check-boot-smoke.sh\n'
                    '[Install]\nWantedBy=multi-user.target\n')
    (root / 'etc/systemd/system/multi-user.target.wants/stitkovac-boot-smoke.service').symlink_to('../stitkovac-boot-smoke.service')


def inspect_second_boot(root, work):
    assert (root / 'data/boot-smoke-result').read_text().strip() == 'passed', 'Storage guard failed on second boot'
    result_file = work / 'result.json'
    result = json.loads(result_file.read_text())
    result['secondBootStorageGuard'] = 'passed'
    result_file.write_text(json.dumps(result, indent=2) + '\n')


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('image', type=Path)
    parser.add_argument('work', type=Path)
    args = parser.parse_args()
    args.work.mkdir(exist_ok=False)
    disk = args.work / 'test.img'
    build.run('cp', '--sparse=always', str(args.image), str(disk))
    with disk.open('r+b') as stream:
        stream.truncate(8 * 1024**3)
    expected_version = None

    def prepare(root):
        nonlocal expected_version
        boot = root / 'boot/firmware'
        envelope = json.loads((root / 'usr/lib/stitkovac-image/release/manifest.json').read_text())
        expected_version = json.loads(base64.b64decode(envelope['payload']))['version']
        for name in ('kernel8.img', 'bcm2710-rpi-3-b.dtb'):
            shutil.copy2(boot / name, args.work / name)
        kernel = args.work / 'kernel8.img'
        if kernel.read_bytes()[:2] == b'\x1f\x8b':
            kernel.write_bytes(gzip.decompress(kernel.read_bytes()))
        shutil.copy2(next((root / 'boot').glob('initrd.img-*-rpi-v8')), args.work / 'initrd.img')
        # QEMU resets when the current vendor initramfs disarms its emulated watchdog.
        # Only the disposable test DTB changes; real hardware retains the vendor DTB.
        build.run('fdtput', '-t', 's', str(args.work / 'bcm2710-rpi-3-b.dtb'),
                  '/soc/watchdog@7e100000', 'status', 'disabled')
        # QEMU has no radio; inject an explicit MAC only in the disposable copy.
        inventory = root / 'usr/lib/stitkovac-image/inventory.yml'
        inventory.write_text(inventory.read_text() + '      gateway_naming_test_mac: "b8:27:eb:e3:9a:e3"\n')
        # Initial provisioning still validates the real OS and signed agent.
        (boot / 'gateway-provision.json').write_text(json.dumps({
            'admin_password': 'disposable-boot-smoke-only', 'country': 'CZ',
            'ssh_public_key': 'ssh-ed25519 ' + base64.b64encode(b'\x00\x00\x00\x0bssh-ed25519\x00\x00\x00\x20' + bytes(range(32))).decode() + ' smoke',
        }))
        service = root / 'etc/systemd/system/stitkovac-firstboot.service.d'
        service.mkdir(parents=True)
        (service / 'smoke.conf').write_text('[Service]\nTimeoutStartSec=1200\n')
    mounted(disk, prepare)
    command = ['qemu-system-aarch64', '-M', 'raspi3b', '-m', '1G', '-smp', '4',
               '-kernel', str(args.work / 'kernel8.img'), '-dtb', str(args.work / 'bcm2710-rpi-3-b.dtb'),
               '-initrd', str(args.work / 'initrd.img'),
               '-drive', 'file=' + str(disk) + ',format=raw,if=sd', '-display', 'none',
               '-serial', 'stdio', '-no-reboot', '-append',
               'console=ttyAMA1,115200 root=/dev/mmcblk0p2 rootfstype=ext4 rootwait rw fsck.repair=yes']
    run_emulator(command, args.work / 'console.log', 1500)

    mounted(disk, lambda root: inspect_first_boot(root, args.work, expected_version))
    command[-1] = command[-1].replace('rootwait rw', 'rootwait ro')
    run_emulator(command, args.work / 'second-boot.log', 600)

    mounted(disk, lambda root: inspect_second_boot(root, args.work))
    print('Provisioning, signed OTA initialization and second-boot storage guards passed.')


if __name__ == '__main__':
    main()
