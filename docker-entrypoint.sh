#!/bin/sh
set -eu

fail() {
  printf '%s\n' "$1" >&2
  exit 1
}

validate_id() {
  case "$2" in
    ''|*[!0-9]*) fail "$1 must be a nonzero decimal UID/GID" ;;
  esac
  # Bound IDs without shell arithmetic overflow; reject leading zeroes.
  case "$2" in
    0*) fail "$1 must be a nonzero decimal UID/GID without leading zeroes" ;;
  esac
  [ "${#2}" -le 10 ] && [ "$2" -lt 4294967295 ] || fail "$1 is outside the UID/GID range"
}

umask 077
uid="$(id -u)"
gid="$(id -g)"
if [ "$uid" = 0 ]; then
  PUID="${PUID-1000}"
  PGID="${PGID-1000}"
  validate_id PUID "$PUID"
  validate_id PGID "$PGID"
  [ ! -L /var/lib/file-station ] || fail "state directory must not be a symbolic link"
  mkdir -p /var/lib/file-station
  # Never recurse into existing state or change ownership of shared mounts.
  chown "$PUID:$PGID" /var/lib/file-station
  exec su-exec "$PUID:$PGID" /usr/local/bin/file-station "$@"
fi

if [ "${PUID+x}" = x ]; then
  validate_id PUID "$PUID"
  [ "$PUID" = "$uid" ] || fail "PUID must match --user when starting without root"
fi
if [ "${PGID+x}" = x ]; then
  validate_id PGID "$PGID"
  [ "$PGID" = "$gid" ] || fail "PGID must match --user when starting without root"
fi
exec /usr/local/bin/file-station "$@"
