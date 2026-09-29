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
for binary in gateway gateway-launcher gateway-update check-storage; do
    test "$(stat -c %a "$root/usr/lib/stitkovac-gateway/$binary")" = 755
done
test ! -e "$root/usr/lib/stitkovac-gateway/gateway-release"
