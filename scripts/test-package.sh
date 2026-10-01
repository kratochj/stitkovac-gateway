#!/bin/sh
set -eu
package=${1:?Debian package path is required}
root=$(mktemp -d)
trap 'rm -rf "$root"' EXIT HUP INT TERM
# Extraction must restore directory permissions even under a private umask.
umask 077
dpkg-deb --extract "$package" "$root"
test "$(stat -c %a "$root")" = 755
test "$(stat -c %a "$root/usr/lib/stitkovac-gateway")" = 755
test "$(dpkg-deb --field "$package" Architecture)" = arm64
for binary in gateway gateway-launcher gateway-update gateway-network check-storage platform-health; do
    test "$(stat -c %a "$root/usr/lib/stitkovac-gateway/$binary")" = 755
done
test ! -e "$root/usr/lib/stitkovac-gateway/gateway-release"

python3 - "$root" <<'PYTHON'
import hashlib
import json
from pathlib import Path
import sys
root = Path(sys.argv[1])
base = root / 'usr/lib/stitkovac-gateway'
manifest = json.loads((base / 'recovery.json').read_text())
assert set(manifest) == {'gateway-launcher', 'gateway-update'}
for name, checksum in manifest.items():
    assert hashlib.sha256((base / name).read_bytes()).hexdigest() == checksum
    assert hashlib.sha256((base / 'recovery' / checksum).read_bytes()).hexdigest() == checksum
assert (root / 'lib/systemd/system/stitkovac-gateway.service.d/platform-health.conf').is_file()
assert (root / 'lib/systemd/system/stitkovac-gateway-diagnostics.timer').is_file()
PYTHON
