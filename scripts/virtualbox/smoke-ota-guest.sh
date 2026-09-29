#!/bin/sh
# Run as root in the lab with a signed wrong-version manifest as the second argument.
# The active agent, cloud settings and persistent release selection are never changed.
set -eu
version=${1:?Supply the installed version}
bad_manifest=${2:?Supply a signed manifest with a deliberately wrong version}
systemctl is-active --quiet gateway-lab-network
/usr/lib/stitkovac-gateway/check-storage
# RAM mounts are deliberately noexec on this appliance.
fixture=$(mktemp -d /data/gateway-ota-smoke.XXXXXX)
trap 'rm -rf "$fixture"' EXIT HUP INT TERM
chmod 0700 "$fixture"
cp "/data/gateway-releases/$version/gateway" "$fixture/gateway"
cp "/data/gateway-releases/$version/manifest.json" "$fixture/good.json"
cp "$bad_manifest" "$fixture/bad.json"
cp /etc/stitkovac-gateway/release-keys.json "$fixture/keys.json"
chown -R stitkovac-gateway:stitkovac-gateway "$fixture"
export fixture version
# An isolated network namespace prevents a test listener from colliding with the real agent.
unshare --net sh -ec '
  ip link set lo up
  exec runuser -u stitkovac-gateway -- sh -s
' <<'GUEST'
set -eu
umask 077
cd "$fixture"
mkdir releases
update() { /usr/lib/stitkovac-gateway/gateway-update "$@" --releases "$fixture/releases" --keys "$fixture/keys.json"; }
update stage --manifest good.json --artifact gateway > /dev/null
update stage --manifest bad.json --artifact gateway > /dev/null
bad_version=$(python3 -c 'import base64,json; print(json.loads(base64.b64decode(json.load(open("bad.json"))["payload"]))["version"])')
test "$bad_version" != "$version"
update initialize --version "$version"
update request --version "$bad_version"
printf '%s' isolated-smoke-password > password
./gateway init --data-dir "$fixture/state" --password-file "$fixture/password" > /dev/null
/usr/lib/stitkovac-gateway/gateway-launcher --releases "$fixture/releases" --keys "$fixture/keys.json" \
  -- --data-dir "$fixture/state" --network-socket "$fixture/no-helper.sock" > launcher.log 2>&1 &
pid=$!
trap 'kill -TERM "$pid" 2>/dev/null || true; wait "$pid" || true' EXIT HUP INT TERM
attempt=0
until update status | python3 -c 'import json,sys; s=json.load(sys.stdin); sys.exit(not (s.get("failed")==sys.argv[1] and s["active"]==sys.argv[2] and not s["trial"]))' "$bad_version" "$version" && grep -q 'Gateway services started' launcher.log; do
  kill -0 "$pid" || { cat launcher.log; exit 1; }
  attempt=$((attempt + 1))
  test "$attempt" -lt 45 || { cat launcher.log; exit 1; }
  sleep 1
done
update status
cat launcher.log
kill -TERM "$pid"
wait "$pid"
trap - EXIT HUP INT TERM
test -s state/gateway.db
echo 'Isolated VM signed startup and automatic rollback passed.'
GUEST
systemctl is-active --quiet stitkovac-gateway
/usr/lib/stitkovac-gateway/check-storage
