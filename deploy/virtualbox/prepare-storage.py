#!/usr/bin/python3
"""Format only the new blank 2 GiB disk in the explicitly identified lab VM."""
import json
from pathlib import Path
import subprocess


def output(*args):
    return subprocess.check_output(args, text=True).strip()


assert output('hostname') == 'stitkovac-gateway-lab', 'This is not the lab VM'
if subprocess.run(['mountpoint', '-q', '/data']).returncode == 0:
    assert output('findmnt', '-n', '-o', 'FSTYPE', '--mountpoint', '/data') == 'ext4'
    raise SystemExit(0)
disks = json.loads(output('lsblk', '--json', '--bytes', '--output', 'NAME,TYPE,SIZE,FSTYPE,MOUNTPOINTS,LABEL'))['blockdevices']
candidates = [d for d in disks if d['type'] == 'disk' and d['size'] == 2147483648 and not d.get('children') and not any(d.get('mountpoints') or [])]
assert len(candidates) == 1, 'Expected exactly one unmounted 2 GiB lab data disk'
disk = candidates[0]
assert disk['fstype'] is None or (disk['fstype'] == 'ext4' and disk['label'] == 'GWLABDATA'), 'Refusing to replace existing data'
if disk['fstype'] is None:
    subprocess.run(['mkfs.ext4', '-L', 'GWLABDATA', '/dev/' + disk['name']], check=True)
Path('/data').mkdir(exist_ok=True)
fstab = Path('/etc/fstab')
entry = 'LABEL=GWLABDATA /data ext4 defaults,noatime 0 2\n'
if entry not in fstab.read_text():
    with fstab.open('a') as f:
        f.write(entry)
subprocess.run(['mount', '/data'], check=True)
