#!/usr/bin/env bash
# ez-am-downloader :: wrapper lifecycle
# Usage: login.sh [start|submit <six-digit-code>|status|interactive]
set -euo pipefail
umask 077

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
AMD_BASE="${AMD_BASE:-$HOME/amd}"
CONFIG_DIR="${EZ_AM_CONFIG_DIR:-$AMD_BASE/config}"
DATA_DIR="${EZ_AM_STATE_DIR:-$AMD_BASE/wrapper-data}"
ENV_FILE="$CONFIG_DIR/.env"
CONFIG_FILE="$CONFIG_DIR/config.yaml"
SESSION_DB="$DATA_DIR/data/com.apple.android.music/files/mpl_db/kvs.sqlitedb"
WRAPPER_IMAGE="${WRAPPER_IMAGE:-local/wrapper:latest}"
LOGIN_CONTAINER="amd-wrapper-login"
AUTH_CONTAINER="amd-wrapper-auth"
TWO_FACTOR_DIR="/app/rootfs/data/data/com.apple.android.music/files"
TWO_FACTOR_FILE="$TWO_FACTOR_DIR/2fa.txt"
AUTH_OWNED=false

cleanup_owned_auth() {
  local status=$?
  if [ "$AUTH_OWNED" = true ]; then
    AUTH_OWNED=false
    remove_container "$AUTH_CONTAINER" || true
  fi
  return "$status"
}

handle_auth_signal() {
  local signal="$1"
  trap - EXIT INT TERM HUP
  cleanup_owned_auth || true
  case "$signal" in
    HUP) exit 129 ;;
    INT) exit 130 ;;
    TERM) exit 143 ;;
  esac
}

arm_auth_cleanup() {
  AUTH_OWNED=true
  trap cleanup_owned_auth EXIT
  trap 'handle_auth_signal HUP' HUP
  trap 'handle_auth_signal INT' INT
  trap 'handle_auth_signal TERM' TERM
}

disarm_auth_cleanup() {
  AUTH_OWNED=false
  trap - EXIT INT TERM HUP
}

require_docker() {
  if ! command -v docker >/dev/null 2>&1 || ! docker info >/dev/null 2>&1; then
    echo "[!] Docker is unavailable. Start Docker Desktop and enable WSL Integration." >&2
    return 1
  fi
}

container_exists() {
  docker inspect "$1" >/dev/null 2>&1
}

container_running() {
  local state
  state="$(docker inspect --format '{{.State.Running}}' "$1" 2>/dev/null)" || return 1
  [ "$state" = true ]
}

port_listening() {
  local table="$1"
  local port="$2"
  awk -v port="$port" '$2 ~ (":" port "$") && $4 == "0A" { found = 1 } END { exit !found }' \
    <<<"$table"
}

wrapper_ports_ready() {
  local container="$1"
  local table
  table="$(docker exec "$container" cat /proc/net/tcp 2>/dev/null)" || return 1
  port_listening "$table" 2724 &&
    port_listening "$table" 4E34 &&
    port_listening "$table" 7544
}

session_db_valid() {
  if ! command -v python3 >/dev/null 2>&1; then
    return 1
  fi

  python3 - "$SESSION_DB" <<'PY'
import sqlite3
import sys
from pathlib import Path


def database_has_session(path):
    # URI mode=ro prevents validation from creating or mutating a database.
    uri = Path(path).resolve().as_uri() + "?mode=ro"
    connection = sqlite3.connect(uri, uri=True)
    try:
        rows = connection.execute(
            "SELECT key, length(value) FROM value "
            "WHERE key = 'StoreFrontIdentifier' OR key LIKE 'Account.XToken:%'"
        ).fetchall()
        has_storefront = any(
            key == "StoreFrontIdentifier" and size and size > 0
            for key, size in rows
        )
        has_account_token = any(
            isinstance(key, str) and key.startswith("Account.XToken:")
            and size and size > 0
            for key, size in rows
        )
        return has_storefront and has_account_token
    finally:
        connection.close()


try:
    valid = database_has_session(sys.argv[1])
except Exception:
    valid = False
raise SystemExit(0 if valid else 1)
PY
}

wait_for_ports() {
  local container="$1"
  local attempts="$2"
  local attempt
  for (( attempt = 0; attempt < attempts; attempt++ )); do
    if container_running "$container" && wrapper_ports_ready "$container"; then
      return 0
    fi
    if (( attempt + 1 < attempts )); then
      sleep 1
    fi
  done
  return 1
}

status_impl() {
  if ! session_db_valid; then
    echo "[!] Persisted wrapper session is missing or empty. Run: bash \"$SCRIPT_DIR/login.sh\" interactive" >&2
    return 1
  fi
  if ! container_exists "$LOGIN_CONTAINER"; then
    echo "[!] $LOGIN_CONTAINER does not exist. Run: bash \"$SCRIPT_DIR/login.sh\" interactive" >&2
    return 1
  fi
  if ! container_running "$LOGIN_CONTAINER"; then
    echo "[!] $LOGIN_CONTAINER is not running. Run: bash \"$SCRIPT_DIR/login.sh\" start" >&2
    return 1
  fi
  if ! wrapper_ports_ready "$LOGIN_CONTAINER"; then
    echo "[!] $LOGIN_CONTAINER is running but ports 10020/20020/30020 are not listening." >&2
    return 1
  fi
  echo "[+] wrapper is running and listening on 10020/20020/30020"
}

load_credentials() {
  local line
  APPLE_ID_USER=""
  APPLE_ID_PASS=""
  if [ ! -f "$ENV_FILE" ]; then
    echo "[!] $ENV_FILE not found. Run setup.sh first." >&2
    return 1
  fi

  while IFS= read -r line || [ -n "$line" ]; do
    line="${line%$'\r'}"
    case "$line" in
      APPLE_ID_USER=*) APPLE_ID_USER="${line#*=}" ;;
      APPLE_ID_PASS=*) APPLE_ID_PASS="${line#*=}" ;;
    esac
  done < "$ENV_FILE"

  if [ -z "$APPLE_ID_USER" ] || [ -z "$APPLE_ID_PASS" ]; then
    echo "[!] .env is missing Apple credentials. Run setup.sh again." >&2
    return 1
  fi
}

remove_container() {
  local container="$1"
  if container_exists "$container"; then
    docker rm -f "$container" >/dev/null
  fi
}

start_long_lived() {
  if ! session_db_valid; then
    echo "[!] Cannot start the wrapper without valid persisted session data." >&2
    return 1
  fi
  if [ ! -r "$CONFIG_FILE" ]; then
    echo "[!] $CONFIG_FILE is missing or unreadable. Run setup.sh first." >&2
    return 1
  fi
  if container_exists "$LOGIN_CONTAINER"; then
    echo "[!] $LOGIN_CONTAINER already exists; preserving it for diagnostics." >&2
    return 1
  fi

  local login_args=(
    run -d
    --name "$LOGIN_CONTAINER"
    --restart unless-stopped
    --privileged
    --publish 127.0.0.1:10020:10020
    --publish 127.0.0.1:20020:20020
    --publish 127.0.0.1:30020:30020
    --mount "type=bind,source=$DATA_DIR,target=/app/rootfs/data"
    --mount "type=bind,source=$CONFIG_FILE,target=/app/config.yaml,readonly"
    "$WRAPPER_IMAGE"
    /app/wrapper -H 0.0.0.0
  )
  if ! docker "${login_args[@]}" >/dev/null; then
    echo "[!] Long-lived wrapper container could not be created." >&2
    return 1
  fi
  if ! wait_for_ports "$LOGIN_CONTAINER" 45 || ! session_db_valid; then
    echo "[!] Long-lived wrapper did not become healthy." >&2
    return 1
  fi
  echo "[+] amd-wrapper-login is healthy"
}

start_existing_login() {
  if ! session_db_valid; then
    if container_exists "$LOGIN_CONTAINER" && container_running "$LOGIN_CONTAINER"; then
      echo "[!] An existing wrapper is running with invalid session data; refusing concurrent authentication." >&2
      return 2
    fi
    echo "[!] Persisted wrapper session is missing or empty; a fresh login is required." >&2
    return 1
  fi
  if ! container_exists "$LOGIN_CONTAINER"; then
    if start_long_lived; then
      echo "[+] cached wrapper session restored"
      return 0
    fi
    # This container was created by start_long_lived, so removing it does not
    # destroy a pre-existing diagnostic container.
    remove_container "$LOGIN_CONTAINER"
    return 1
  fi

  if ! container_running "$LOGIN_CONTAINER"; then
    if ! docker start "$LOGIN_CONTAINER" >/dev/null; then
      return 2
    fi
  fi

  if wait_for_ports "$LOGIN_CONTAINER" 20; then
    echo "[+] existing wrapper session restored"
    return 0
  fi

  echo "[!] Existing wrapper did not become healthy; preserving it for diagnostics." >&2
  return 2
}

start_authentication() {
  if [ ! -d "$DATA_DIR" ]; then
    echo "[!] $DATA_DIR is missing. Run setup.sh first; session data will not be created here." >&2
    return 1
  fi

  if container_exists "$AUTH_CONTAINER"; then
    if container_running "$AUTH_CONTAINER"; then
      echo "[!] An authentication request is already active; submit its code or wait for it to expire." >&2
      return 2
    fi
    remove_container "$AUTH_CONTAINER"
  fi
  local auth_args=(
    run -d
    --rm
    --name "$AUTH_CONTAINER"
    --privileged
    --mount "type=bind,source=$DATA_DIR,target=/app/rootfs/data"
    "$WRAPPER_IMAGE"
    /app/wrapper -L "$APPLE_ID_USER:$APPLE_ID_PASS" -F -H 0.0.0.0
  )
  if ! docker "${auth_args[@]}" >/dev/null; then
    echo "[!] Could not start the temporary authentication container." >&2
    return 1
  fi
  arm_auth_cleanup

  local attempt
  for (( attempt = 0; attempt < 30; attempt++ )); do
    if ! container_running "$AUTH_CONTAINER"; then
      echo "[!] The temporary authentication container exited before requesting 2FA." >&2
      cleanup_owned_auth || true
      disarm_auth_cleanup
      return 1
    fi
    if docker logs "$AUTH_CONTAINER" 2>&1 | grep -q "Waiting for input"; then
      echo "[+] Authentication started. Submit the six-digit code within the Apple window."
      echo "STATE=NEED_2FA"
      return 0
    fi
    if (( attempt + 1 < 30 )); then
      sleep 1
    fi
  done

  echo "[!] Timed out waiting for the 2FA input path." >&2
  cleanup_owned_auth || true
  disarm_auth_cleanup
  return 1
}

promote_authenticated_session() {
  if ! wait_for_ports "$AUTH_CONTAINER" 45; then
    echo "[!] Authentication did not produce a healthy wrapper." >&2
    remove_container "$AUTH_CONTAINER"
    return 1
  fi
  if ! session_db_valid; then
    echo "[!] Authentication ports opened, but no valid persisted session was written." >&2
    remove_container "$AUTH_CONTAINER"
    return 1
  fi

  remove_container "$AUTH_CONTAINER"
  remove_container "$LOGIN_CONTAINER"
  start_long_lived || return 1
  echo "[+] LOGIN OK: amd-wrapper-login is healthy"
}

submit_code() {
  local code="$1"
  if [[ "$code" =~ ^[0-9]{6}$ ]]; then
    :
  else
    echo "[!] 2FA code must be exactly six digits." >&2
    return 2
  fi

  require_docker || return 1
  if ! container_running "$AUTH_CONTAINER"; then
    echo "[!] No active authentication request. Run: bash \"$SCRIPT_DIR/login.sh\" start" >&2
    return 1
  fi

  arm_auth_cleanup

  # The code is data on stdin; it is never interpolated into a shell command.
  if ! printf '%s' "$code" | docker exec -i "$AUTH_CONTAINER" tee "$TWO_FACTOR_FILE" >/dev/null; then
    echo "[!] Could not submit the 2FA code to the authentication container." >&2
    cleanup_owned_auth || true
    disarm_auth_cleanup
    return 1
  fi
  echo "[*] Code submitted; waiting for the live wrapper ports ..."
  if promote_authenticated_session; then
    disarm_auth_cleanup
    return 0
  fi
  cleanup_owned_auth || true
  disarm_auth_cleanup
  return 1
}

start_mode() {
  require_docker || return 1
  if [ ! -r "$CONFIG_FILE" ]; then
    echo "[!] $CONFIG_FILE is missing or unreadable. Run setup.sh first." >&2
    return 1
  fi
  local existing_result
  if start_existing_login; then
    return 0
  else
    existing_result=$?
  fi
  if (( existing_result == 2 )); then
    return 1
  fi
  load_credentials || return 1
  start_authentication || return $?
}

interactive_mode() {
  require_docker || return 1
  if status_impl >/dev/null 2>&1; then
    echo "[+] Existing wrapper session is healthy."
    return 0
  fi
  if ! start_mode; then
    return 1
  fi
  if status_impl >/dev/null 2>&1; then
    return 0
  fi
  if ! container_running "$AUTH_CONTAINER"; then
    echo "[!] Authentication container is no longer running." >&2
    return 1
  fi
  printf ">>> Enter the six-digit Apple 2FA code: "
  local code
  if ! IFS= read -r code; then
    echo "[!] No 2FA code was provided." >&2
    remove_container "$AUTH_CONTAINER"
    return 1
  fi
  submit_code "$code"
}

case "${1:-interactive}" in
  start)
    [ "$#" -eq 1 ] || { echo "usage: login.sh start" >&2; exit 2; }
    if start_mode; then
      # `start` intentionally leaves a fresh auth container for a later
      # `submit`; interactive mode retains cleanup ownership itself.
      disarm_auth_cleanup
    else
      exit $?
    fi
    ;;
  submit)
    [ "$#" -eq 2 ] || { echo "usage: login.sh submit <six-digit-code>" >&2; exit 2; }
    submit_code "$2"
    ;;
  status)
    [ "$#" -eq 1 ] || { echo "usage: login.sh status" >&2; exit 2; }
    require_docker || exit 1
    status_impl
    ;;
  interactive)
    [ "$#" -le 1 ] || { echo "usage: login.sh interactive" >&2; exit 2; }
    interactive_mode
    ;;
  *)
    echo "usage: login.sh [start|submit <six-digit-code>|status|interactive]" >&2
    exit 2
    ;;
esac
