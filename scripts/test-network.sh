#!/bin/sh
set -eu
# An isolated network namespace is required by nft --check. Never run this script
# against a host's live network namespace or mount host NetworkManager state.
repo=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
docker run --rm --platform linux/arm64 --cap-add NET_ADMIN -v "$repo:/src:ro" debian:trixie-slim sh -eu -c '
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq --no-install-recommends network-manager iproute2 util-linux iw dnsmasq-base nftables python3 python3-gi gir1.2-nm-1.0 python3-jinja2 curl systemd >/dev/null
python3 /src/scripts/test-network-linux.py
'
