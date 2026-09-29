#!/bin/sh
# Exercise the real signed ARM64 agent and rollback without networking or hardware.
# Requires make VERSION=0.1.0 build arm64, OpenSSL and Docker with ARM64 support.
set -eu
cd "$(dirname "$0")/.."
workspace=$(pwd)
fixture=$(mktemp -d /tmp/stitkovac-ota-test.XXXXXX)
trap 'rm -rf "$fixture"' EXIT HUP INT TERM
umask 077
openssl genpkey -algorithm Ed25519 -out "$fixture/signing.pem"
bin/gateway-release --artifact bin/gateway-linux-arm64 --version 0.1.0 \
  --key-file "$fixture/signing.pem" --key-id test \
  --manifest-out "$fixture/0.1.0.json" > "$fixture/keys.json"
# A deliberately wrong build version must fail readiness and restore 0.1.0.
bin/gateway-release --artifact bin/gateway-linux-arm64 --version 0.1.1 \
  --key-file "$fixture/signing.pem" --key-id test \
  --manifest-out "$fixture/0.1.1.json" > /dev/null
rm "$fixture/signing.pem"
docker run --rm --network none --platform linux/arm64 --read-only \
  --tmpfs /tmp:rw,exec,size=256m \
  --mount "type=bind,src=$workspace/bin,dst=/tools,readonly" \
  --mount "type=bind,src=$fixture,dst=/fixture,readonly" \
  debian:bookworm-slim sh -ec '
    umask 077
    mkdir /tmp/releases
    update() { /tools/gateway-update-linux-arm64 "$@" --releases /tmp/releases --keys /fixture/keys.json; }
    update stage --manifest /fixture/0.1.0.json --artifact /tools/gateway-linux-arm64
    update stage --manifest /fixture/0.1.1.json --artifact /tools/gateway-linux-arm64
    update initialize --version 0.1.0
    update request --version 0.1.1
    printf "%s" temporary-test-password > /tmp/password
    /tools/gateway-linux-arm64 init --data-dir /tmp/state --password-file /tmp/password
    /tools/gateway-launcher-linux-arm64 --releases /tmp/releases --keys /fixture/keys.json \
      -- --data-dir /tmp/state > /tmp/launcher.log 2>&1 &
    pid=$!
    trap "kill -TERM $pid 2>/dev/null || true" EXIT
    attempts=0
    until update status | grep -q '\''"failed":"0.1.1"'\'' && grep -q "Gateway services started" /tmp/launcher.log; do
      kill -0 "$pid" || { cat /tmp/launcher.log; exit 1; }
      attempts=$((attempts + 1))
      test "$attempts" -lt 15 || { cat /tmp/launcher.log; exit 1; }
      sleep 1
    done
    update status
    cat /tmp/launcher.log
    kill -TERM "$pid"
    wait "$pid"
    trap - EXIT
    test -s /tmp/state/gateway.db
    printf "%s\n" "Signed ARM64 startup and rollback passed"
  '
