#!/bin/sh
set -eu
cd "$(dirname "$0")/../.."
exec ssh -i dist/virtualbox/id_ed25519 -p 2222 \
  -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new \
  -o UserKnownHostsFile=dist/virtualbox/known_hosts \
  -o ConnectTimeout=5 technik@127.0.0.1 "$@"
