#!/bin/sh
set -eu

DATA_DIR="${DATA_DIR:-/paste-data}"
APP_USER="${APP_USER:-pastebox}"
APP_GROUP="${APP_GROUP:-pastebox}"

if [ -n "${MIRROR_URL:-}" ]; then
  printf '%s\n' \
    "${MIRROR_URL%/}/v3.23/main" \
    "${MIRROR_URL%/}/v3.23/community" \
    > /etc/apk/repositories
fi

mkdir -p "$DATA_DIR"

# Bind mounts inherit host-side ownership. Make the directory writable before
# dropping privileges. Some filesystems ignore chown, so chmod is kept as a
# fallback for local Docker Desktop / rootless Docker cases.
chown -R "$APP_USER:$APP_GROUP" "$DATA_DIR" 2>/dev/null || true
chmod -R u+rwX,g+rwX,o+rwX "$DATA_DIR" 2>/dev/null || true

exec su-exec "$APP_USER:$APP_GROUP" "$@"
