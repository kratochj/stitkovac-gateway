#!/bin/sh
# Build a new lab; never overwrite an existing VM or its identity.
set -eu
cd "$(dirname "$0")/../.."
umask 077
if [ -e dist/virtualbox/lab.json ]; then
    echo 'Lab already exists; start it with scripts/virtualbox/start.py.' >&2
    exit 1
fi
for tool in python3 VBoxManage qemu-img ansible-playbook go curl; do
    command -v "$tool" >/dev/null
done
mkdir -p .cache/virtualbox dist/virtualbox
image=debian-13-generic-arm64-20260914-2601.qcow2
if [ ! -f ".cache/virtualbox/$image" ]; then
    curl --fail --location --retry 3 \
        "https://cloud.debian.org/images/cloud/trixie/20260914-2601/$image" \
        --output ".cache/virtualbox/$image.partial"
    mv ".cache/virtualbox/$image.partial" ".cache/virtualbox/$image"
fi
make VERSION=0.1.1 lab-arm64
python3 scripts/virtualbox/create.py
python3 - <<'PY'
import json, secrets, subprocess, time
from pathlib import Path
deadline = time.monotonic() + 300
while subprocess.run(['sh', 'scripts/virtualbox/ssh.sh', 'true'],
                     stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode:
    if time.monotonic() > deadline:
        raise SystemExit('SSH did not become available within 300 seconds')
    time.sleep(2)
Path('dist/virtualbox/credentials.json').write_text(json.dumps({
    'gateway_admin_password': secrets.token_urlsafe(20),
    'gateway_lab_token': 'lab_' + secrets.token_urlsafe(32),
}) + '\n')
PY
ANSIBLE_PIPELINING=True ansible-playbook -i deploy/virtualbox/inventory.yml \
    deploy/virtualbox/provision.yml -e @dist/virtualbox/credentials.json \
    -e gateway_printer_interface=vgw0
python3 scripts/virtualbox/prepare-access.py
python3 scripts/virtualbox/stop.py
python3 scripts/virtualbox/export.py --no-export
python3 scripts/virtualbox/start.py
python3 scripts/virtualbox/smoke.py
python3 scripts/virtualbox/stop.py
python3 scripts/virtualbox/export.py
