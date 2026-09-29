#!/bin/sh
set -eu
umask 022

version=${1:-0.1.0-dev}
case "$version" in ''|*[!0-9A-Za-z.+~-]*) echo 'Invalid package version' >&2; exit 1 ;; esac
command -v dpkg-deb >/dev/null
test -f bin/gateway-linux-arm64
test -f bin/gateway-launcher-linux-arm64
test -f bin/gateway-update-linux-arm64
test -f bin/gateway-network-linux-arm64
stage=$(mktemp -d)
chmod 755 "$stage"
trap 'rm -rf "$stage"' EXIT HUP INT TERM
mkdir -p "$stage/DEBIAN" "$stage/usr/lib/stitkovac-gateway" "$stage/lib/systemd/system" dist
install -m 755 bin/gateway-linux-arm64 "$stage/usr/lib/stitkovac-gateway/gateway"
install -m 755 bin/gateway-launcher-linux-arm64 "$stage/usr/lib/stitkovac-gateway/gateway-launcher"
install -m 755 bin/gateway-network-linux-arm64 "$stage/usr/lib/stitkovac-gateway/gateway-network"
install -m 644 deploy/systemd/stitkovac-gateway-network.service "$stage/lib/systemd/system/stitkovac-gateway-network.service"
install -m 755 bin/gateway-update-linux-arm64 "$stage/usr/lib/stitkovac-gateway/gateway-update"
install -m 755 deploy/systemd/check-storage "$stage/usr/lib/stitkovac-gateway/check-storage"
install -m 644 deploy/systemd/stitkovac-gateway.service "$stage/lib/systemd/system/stitkovac-gateway.service"
cat > "$stage/DEBIAN/control" <<EOF
Package: stitkovac-gateway
Version: $version
Architecture: arm64
Maintainer: kratochj
Depends: python3, util-linux
Description: Stitkovac local print gateway
 Local print transport and administration for a prepared Raspberry Pi OS image.
EOF
dpkg-deb --root-owner-group --build "$stage" "dist/stitkovac-gateway_${version}_arm64.deb"
