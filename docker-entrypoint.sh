#!/bin/sh
set -eu

DATA_DIR="${DATA_DIR:-/paste-data}"
APP_USER="${APP_USER:-pastebox}"
APP_GROUP="${APP_GROUP:-pastebox}"
UID="${UID:-}"
GID="${GID:-}"
TZ="${TZ:-Asia/Seoul}"

if [ -n "${MIRROR_URL:-}" ]; then
  printf '%s\n' \
    "${MIRROR_URL%/}/v3.23/main" \
    "${MIRROR_URL%/}/v3.23/community" \
    > /etc/apk/repositories
fi

if [ -f "/usr/share/zoneinfo/$TZ" ]; then
  cp "/usr/share/zoneinfo/$TZ" /etc/localtime
  echo "$TZ" > /etc/timezone
else
  echo "warning: timezone '$TZ' not found, keeping existing timezone settings" >&2
fi

mkdir -p "$DATA_DIR"

# When UID/GID are set, use the host user's numeric IDs so bind-mounted
# data stays writable from the host without root-only ownership drift.
RUN_AS_USER="$APP_USER"
RUN_AS_GROUP="$APP_GROUP"
if [ -n "$UID" ] && [ -n "$GID" ]; then
  RUN_AS_USER="$UID"
  RUN_AS_GROUP="$GID"
fi

# Limit ownership fixes to the data root so existing paste files are not made
# world-writable on every startup.
chown "$RUN_AS_USER:$RUN_AS_GROUP" "$DATA_DIR" 2>/dev/null || true
chmod 0775 "$DATA_DIR" 2>/dev/null || true

exec su-exec "$RUN_AS_USER:$RUN_AS_GROUP" "$@"
