#!/usr/bin/env bash
set -u

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
failures=0

pass() { printf 'PASS %s\n' "$1"; }
fail() { printf 'FAIL %s\n' "$1" >&2; failures=$((failures + 1)); }
contains() {
  local file=$1 pattern=$2 name=$3
  if grep -Fq -- "$pattern" "$file"; then pass "$name"; else fail "$name"; fi
}
not_contains() {
  local file=$1 pattern=$2 name=$3
  if grep -Fq -- "$pattern" "$file"; then fail "$name"; else pass "$name"; fi
}

contains "$ROOT/setup.sh" 'install_runtime_file "dl.sh"' "setup installs canonical dl.sh"
contains "$ROOT/setup.sh" 'install_runtime_file "login.sh"' "setup installs canonical login.sh"
not_contains "$ROOT/setup.sh" 'cat > "$BASE/dl.sh"' "setup does not embed a second dl.sh"
contains "$ROOT/setup.sh" 'exit-on-error: true' "setup enables real downloader failures"
contains "$ROOT/setup.sh" 'AMD_BASE' "setup supports an explicit runtime base"
contains "$ROOT/setup.sh" 'umask 077' "setup uses a private creation mask"
contains "$ROOT/setup.sh" 'SCRIPT_DIR=' "setup derives its canonical source directory"
contains "$ROOT/setup.sh" 'install_runtime_file "compose.yaml"' "setup installs canonical compose.yaml"
contains "$ROOT/setup.sh" 'backup_existing' "setup backs up existing secret configuration"
contains "$ROOT/setup.sh" 'chmod 700 "$CFG_DIR"' "setup protects the config directory"
contains "$ROOT/setup.sh" 'chmod 600 "$ENV_FILE" "$CFG_FILE"' "setup protects secret files"
contains "$ROOT/setup.sh" 'IFS= read -r -s -p "media-user-token: " MEDIA_TOKEN' "setup masks media-user-token input"
not_contains "$ROOT/setup.sh" 'sudo ' "setup never asks for sudo"
not_contains "$ROOT/setup.sh" 'apt-get upgrade' "setup never upgrades the distro"
not_contains "$ROOT/setup.sh" 'powershell.exe' "setup does not generate Windows launchers"
not_contains "$ROOT/setup.sh" 'docker rm -f amd-wrapper-login' "setup does not own the login lifecycle"
contains "$ROOT/dl.sh" 'login.sh status' "download checks wrapper status through login.sh"
not_contains "$ROOT/dl.sh" 'docker run -d --name amd-wrapper-login' "download never creates an unlogged wrapper"
contains "$ROOT/dl.sh" 'download_args=(' "download builds Docker arguments as an array"
contains "$ROOT/dl.sh" 'docker "${download_args[@]}"' "download preserves Docker argument boundaries"
not_contains "$ROOT/dl.sh" 'docker start' "download does not own wrapper lifecycle"
contains "$ROOT/login.sh" 'case "${1:-interactive}"' "login exposes explicit lifecycle modes"
contains "$ROOT/login.sh" 'submit)' "login has a submit mode"
contains "$ROOT/login.sh" 'status)' "login has a status mode"
contains "$ROOT/login.sh" 'AMD_BASE' "login supports an explicit runtime base"
contains "$ROOT/login.sh" 'amd-wrapper-auth' "login isolates temporary Apple authentication"
contains "$ROOT/login.sh" '[[ "$code" =~ ^[0-9]{6}$ ]]' "login strictly validates six-digit 2FA"
contains "$ROOT/login.sh" 'docker exec -i "$AUTH_CONTAINER" tee "$TWO_FACTOR_FILE"' "login sends 2FA through stdin to a fixed file"
contains "$ROOT/login.sh" '/proc/net/tcp' "login checks the live TCP table"
contains "$ROOT/login.sh" 'SESSION_DB="$DATA_DIR/data/com.apple.android.music/files/mpl_db/kvs.sqlitedb"' "login identifies the persisted session database"
contains "$ROOT/login.sh" 'session_db_valid' "login validates session database content"
contains "$ROOT/login.sh" 'docker logs "$AUTH_CONTAINER"' "login observes the current authentication prompt"
contains "$ROOT/login.sh" 'STATE=NEED_2FA' "login exposes a machine-readable 2FA state"
not_contains "$ROOT/login.sh" 'docker logs "$LOGIN_CONTAINER"' "login health never trusts historical login logs"
contains "$ROOT/login.sh" '--restart unless-stopped' "login configures the long-lived restart policy"
contains "$ROOT/login.sh" 'start_long_lived' "login can rebuild a runtime from cached session data"
contains "$ROOT/login.sh" 'ensure_data_skeleton' "login prepares the wrapper data skeleton the entrypoint would create"
contains "$ROOT/login.sh" 'DATA_SKELETON_DIR="$DATA_DIR/data/com.apple.android.music/files"' "login targets the wrapper base directory"
contains "$ROOT/login.sh" 'require_docker || return 1' "login does not rely on inherited errexit"
contains "$ROOT/login.sh" 'load_credentials || return 1' "login stops after missing credentials"
contains "$ROOT/login.sh" 'trap handle_auth_signal INT TERM HUP' "login cleans owned authentication on signals"
contains "$ROOT/compose.yaml" 'container_name: amd-wrapper-login' "compose uses the canonical container name"
contains "$ROOT/compose.yaml" './config/config.yaml' "compose mounts the real config path"
contains "$ROOT/compose.yaml" './wrapper-data' "compose preserves wrapper session data"
contains "$ROOT/compose.yaml" 'privileged: true' "compose retains upstream privileged mode"
contains "$ROOT/compose.yaml" '127.0.0.1:10020:10020' "compose binds port 10020 to loopback"
contains "$ROOT/compose.yaml" '127.0.0.1:20020:20020' "compose binds port 20020 to loopback"
contains "$ROOT/compose.yaml" '127.0.0.1:30020:30020' "compose binds port 30020 to loopback"
not_contains "$ROOT/compose.yaml" 'APPLE_ID_' "compose does not persist Apple credentials"
contains "$ROOT/sync_existing_wsl.sh" 'file_set_signature' "sync guards download and wrapper file sets"
contains "$ROOT/sync_existing_wsl.sh" 'config_without_exit_signature' "sync limits configuration changes"
not_contains "$ROOT/sync_existing_wsl.sh" 'rm -rf' "sync never recursively deletes data"
not_contains "$ROOT/sync_existing_wsl.sh" 'bash "$SRC/setup.sh"' "sync never regenerates existing secrets"

for script in "$ROOT/setup.sh" "$ROOT/dl.sh" "$ROOT/login.sh" "$ROOT/sync_existing_wsl.sh" \
  "$ROOT/tests/verify_existing_media.sh" "$ROOT/tests/check_runtime_mounts.sh"
do
  if bash -n "$script"; then
    pass "bash syntax $(basename "$script")"
  else
    fail "bash syntax $(basename "$script")"
  fi
done

SANDBOX="$(mktemp -d)"
trap 'rm -rf "$SANDBOX"' EXIT
mkdir -p "$SANDBOX/bin" "$SANDBOX/home/amd"

mkdir -p "$SANDBOX/setup-bin" "$SANDBOX/setup-home"
cat > "$SANDBOX/setup-bin/docker" <<'EOF'
#!/usr/bin/env bash
case "${1:-}" in
  info|pull) exit 0 ;;
  compose) [ "${2:-}" = version ] ;;
  image) [ "${2:-}" = inspect ] ;;
  *) exit 97 ;;
esac
EOF
chmod +x "$SANDBOX/setup-bin/docker"

if python3 - "$ROOT/setup.sh" "$SANDBOX/setup-bin" "$SANDBOX/setup-home" <<'PY'
import errno
import os
import pty
import select
import sys
import time
from pathlib import Path

setup, fake_bin, setup_home = sys.argv[1:]
runtime = Path(setup_home) / "amd-interactive"
password = " leading-password-sentinel "
token = " leading-token-sentinel "
env = os.environ.copy()
env.update({
    "AMD_BASE": str(runtime),
    "HOME": setup_home,
    "PATH": fake_bin + os.pathsep + env.get("PATH", ""),
    "WRAPPER_IMAGE": "local/test-wrapper:latest",
    "DOWNLOADER_IMAGE": "example.invalid/downloader:test",
})

pid, fd = pty.fork()
if pid == 0:
    os.execvpe("bash", ["bash", setup], env)

transcript = bytearray()

def read_until(marker):
    deadline = time.monotonic() + 10
    while marker not in transcript:
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise RuntimeError("prompt timeout")
        ready, _, _ = select.select([fd], [], [], remaining)
        if not ready:
            raise RuntimeError("prompt timeout")
        transcript.extend(os.read(fd, 4096))

try:
    read_until(b"Apple ID email: ")
    os.write(fd, b"test@example.invalid\n")
    read_until(b"Apple ID password: ")
    os.write(fd, (password + "\n").encode())
    read_until(b"media-user-token: ")
    os.write(fd, (token + "\n").encode())
    read_until(b"storefront (for example us, jp, hk) [us]: ")
    os.write(fd, b"us\n")
    while True:
        ready, _, _ = select.select([fd], [], [], 10)
        if not ready:
            raise RuntimeError("setup exit timeout")
        transcript.extend(os.read(fd, 4096))
except OSError as error:
    if error.errno != errno.EIO:
        raise
except Exception:
    os.kill(pid, 9)
    os.waitpid(pid, 0)
    raise SystemExit(1)

_, status = os.waitpid(pid, 0)
if os.waitstatus_to_exitcode(status) != 0:
    raise SystemExit(1)
if password.encode() in transcript or token.encode() in transcript:
    raise SystemExit(1)
if b"media-user-token: \r\nstorefront" not in transcript and b"media-user-token: \nstorefront" not in transcript:
    raise SystemExit(1)
if "APPLE_ID_PASS=" + password not in (runtime / ".env").read_text():
    raise SystemExit(1)
if 'media-user-token: "' + token + '"' not in (runtime / "config/config.yaml").read_text():
    raise SystemExit(1)
PY
then
  pass "setup masks interactive secrets and preserves exact input"
else
  fail "setup masks interactive secrets and preserves exact input"
fi

NONINTERACTIVE_BASE="$SANDBOX/setup-home/amd-noninteractive"
setup_output="$(
  HOME="$SANDBOX/setup-home" AMD_BASE="$NONINTERACTIVE_BASE" \
    PATH="$SANDBOX/setup-bin:$PATH" \
    WRAPPER_IMAGE="local/test-wrapper:latest" \
    DOWNLOADER_IMAGE="example.invalid/downloader:test" \
    EZ_USER="noninteractive@example.invalid" \
    EZ_PASS=" noninteractive-password-sentinel " \
    EZ_TOKEN=" noninteractive-token-sentinel " \
    EZ_STOREFRONT="us" \
    bash "$ROOT/setup.sh" </dev/null 2>&1
)"
setup_rc=$?
if (( setup_rc == 0 )) &&
   [[ "$setup_output" != *"noninteractive-password-sentinel"* ]] &&
   [[ "$setup_output" != *"noninteractive-token-sentinel"* ]] &&
   grep -Fq 'APPLE_ID_PASS= noninteractive-password-sentinel ' "$NONINTERACTIVE_BASE/.env" &&
   grep -Fq 'media-user-token: " noninteractive-token-sentinel "' "$NONINTERACTIVE_BASE/config/config.yaml"
then
  pass "setup preserves noninteractive secrets without logging them"
else
  fail "setup preserves noninteractive secrets without logging them"
fi

cat > "$SANDBOX/bin/docker" <<'EOF'
#!/usr/bin/env bash
printf 'called\n' >> "$DOCKER_MARKER"
exit 99
EOF
chmod +x "$SANDBOX/bin/docker"

for invalid_case in \
  'not-an-apple-url alac' \
  'https://music.apple.com/us/album/example/1 flac'
do
  set -- $invalid_case
  marker="$SANDBOX/docker-called"
  rm -f "$marker"
  HOME="$SANDBOX/home" AMD_BASE="$SANDBOX/home/amd" DOCKER_MARKER="$marker" \
    PATH="$SANDBOX/bin:$PATH" bash "$ROOT/dl.sh" "$1" "$2" >/dev/null 2>&1
  rc=$?
  if (( rc != 0 )) && [ ! -e "$marker" ]; then
    pass "download rejects $invalid_case before Docker"
  else
    fail "download rejects $invalid_case before Docker"
  fi
done

mkdir -p "$SANDBOX/runtime/config" "$SANDBOX/runtime/downloads"
: > "$SANDBOX/runtime/config/config.yaml"
mkdir -p "$SANDBOX/runtime/wrapper-data/data/com.apple.android.music/files/mpl_db"
dd if=/dev/zero of="$SANDBOX/runtime/wrapper-data/data/com.apple.android.music/files/mpl_db/kvs.sqlitedb" bs=4096 count=1 status=none
cat > "$SANDBOX/runtime/login.sh" <<'EOF'
#!/usr/bin/env bash
[ "${1:-}" = status ]
EOF
chmod +x "$SANDBOX/runtime/login.sh"
cat > "$SANDBOX/bin/docker" <<'EOF'
#!/usr/bin/env bash
case "${1:-}" in
  info) exit 0 ;;
  run) exit 42 ;;
  *) exit 97 ;;
esac
EOF
chmod +x "$SANDBOX/bin/docker"

output=""
output="$(AMD_BASE="$SANDBOX/runtime" PATH="$SANDBOX/bin:$PATH" \
  bash "$ROOT/dl.sh" 'https://music.apple.com/us/album/example/1' alac 2>&1)"
rc=$?
if (( rc == 42 )); then
  pass "download propagates the Docker exit code"
else
  fail "download propagates the Docker exit code"
fi
if [[ "$output" != *'done'* ]]; then
  pass "download prints done only after success"
else
  fail "download prints done only after success"
fi

cat > "$SANDBOX/bin/docker" <<'EOF'
#!/usr/bin/env bash
case "${1:-}" in
  run)
    printf '%s\n' "$*" > "$SIGNAL_RUN_LOG"
    : > "$SIGNAL_STARTED"
    trap 'exit 130' INT TERM HUP
    while :; do sleep 1; done
    ;;
  rm)
    printf '%s\n' "$*" > "$SIGNAL_RM_LOG"
    exit 0
    ;;
  *) exit 97 ;;
esac
EOF
chmod +x "$SANDBOX/bin/docker"

rm -f "$SANDBOX/signal-started" "$SANDBOX/signal-run-log" "$SANDBOX/signal-rm-log"
AMD_BASE="$SANDBOX/runtime" PATH="$SANDBOX/bin:$PATH" \
  EZ_DOWNLOAD_CONTAINER="amd-downloader-signal-test" \
  SIGNAL_STARTED="$SANDBOX/signal-started" \
  SIGNAL_RUN_LOG="$SANDBOX/signal-run-log" \
  SIGNAL_RM_LOG="$SANDBOX/signal-rm-log" \
  setsid bash "$ROOT/dl.sh" 'https://music.apple.com/us/album/example/1' alac \
  >/dev/null 2>&1 &
signal_pid=$!
for _ in $(seq 1 100); do
  [ -e "$SANDBOX/signal-started" ] && break
  sleep 0.05
done
if [ -e "$SANDBOX/signal-started" ]; then
  kill -TERM -- "-$signal_pid" 2>/dev/null || true
fi
wait "$signal_pid" 2>/dev/null
signal_rc=$?
if kill -0 "$signal_pid" 2>/dev/null; then
  kill -KILL -- "-$signal_pid" 2>/dev/null || true
  wait "$signal_pid" 2>/dev/null || true
fi
if (( signal_rc != 0 )) &&
   grep -Fq -- '--name amd-downloader-signal-test' "$SANDBOX/signal-run-log" 2>/dev/null &&
   grep -Fq -- 'rm -f amd-downloader-signal-test' "$SANDBOX/signal-rm-log" 2>/dev/null
then
  pass "download signal removes its named container"
else
  fail "download signal removes its named container"
fi

cat > "$SANDBOX/bin/docker" <<'EOF'
#!/usr/bin/env bash
case "${1:-}" in
  info) exit 0 ;;
  inspect) printf 'true\n' ;;
  exec) cat "$TCP_TABLE" ;;
  logs)
    : > "$DOCKER_LOG_MARKER"
    printf 'listening 0.0.0.0:10020 / 20020 / 30020\n'
    ;;
  *) exit 97 ;;
esac
EOF
chmod +x "$SANDBOX/bin/docker"
cat > "$SANDBOX/tcp-healthy" <<'EOF'
  0: 00000000:2724 00000000:0000 0A
  1: 00000000:4E34 00000000:0000 0A
  2: 00000000:7544 00000000:0000 0A
EOF
cat > "$SANDBOX/tcp-missing-port" <<'EOF'
  0: 00000000:2724 00000000:0000 0A
  1: 00000000:4E34 00000000:0000 0A
EOF

if AMD_BASE="$SANDBOX/runtime" TCP_TABLE="$SANDBOX/tcp-healthy" \
  DOCKER_LOG_MARKER="$SANDBOX/docker-log-called" PATH="$SANDBOX/bin:$PATH" \
  bash "$ROOT/login.sh" status >/dev/null 2>&1
then
  fail "login status rejects an empty session database"
else
  pass "login status rejects an empty session database"
fi

if command -v python3 >/dev/null 2>&1; then
  rm -f "$SANDBOX/runtime/wrapper-data/data/com.apple.android.music/files/mpl_db/kvs.sqlitedb"
  python3 - "$SANDBOX/runtime/wrapper-data/data/com.apple.android.music/files/mpl_db/kvs.sqlitedb" <<'PY'
import sqlite3
import sys
db = sqlite3.connect(sys.argv[1])
db.execute("create table session (key text primary key, value text)")
db.execute("insert into session values ('authenticated', 'yes')")
db.commit()
db.close()
PY
else
  fail "python3 is required to create a valid SQLite session fixture"
fi

if AMD_BASE="$SANDBOX/runtime" TCP_TABLE="$SANDBOX/tcp-healthy" \
  DOCKER_LOG_MARKER="$SANDBOX/docker-log-called" PATH="$SANDBOX/bin:$PATH" \
  bash "$ROOT/login.sh" status >/dev/null 2>&1
then
  fail "login status rejects unrelated SQLite rows"
else
  pass "login status rejects unrelated SQLite rows"
fi

if command -v python3 >/dev/null 2>&1; then
  rm -f "$SANDBOX/runtime/wrapper-data/data/com.apple.android.music/files/mpl_db/kvs.sqlitedb"
  python3 - "$SANDBOX/runtime/wrapper-data/data/com.apple.android.music/files/mpl_db/kvs.sqlitedb" <<'PY'
import sqlite3
import sys
db = sqlite3.connect(sys.argv[1])
db.execute("create table value (key text primary key, value text)")
db.execute("insert into value values ('StoreFrontIdentifier', '143441')")
db.execute("insert into value values ('Account.XToken:test@example.invalid', 'token')")
db.commit()
db.close()
PY
fi

if AMD_BASE="$SANDBOX/runtime" TCP_TABLE="$SANDBOX/tcp-healthy" \
  DOCKER_LOG_MARKER="$SANDBOX/docker-log-called" PATH="$SANDBOX/bin:$PATH" \
  bash "$ROOT/login.sh" status >/dev/null 2>&1
then
  pass "login status accepts a running three-port wrapper"
else
  fail "login status accepts a running three-port wrapper"
fi

rm -f "$SANDBOX/docker-log-called"
if AMD_BASE="$SANDBOX/runtime" TCP_TABLE="$SANDBOX/tcp-missing-port" \
  DOCKER_LOG_MARKER="$SANDBOX/docker-log-called" PATH="$SANDBOX/bin:$PATH" \
  bash "$ROOT/login.sh" status >/dev/null 2>&1
then
  fail "login status rejects stale logs and a missing live port"
elif [ -e "$SANDBOX/docker-log-called" ]; then
  fail "login status rejects stale logs and a missing live port"
else
  pass "login status rejects stale logs and a missing live port"
fi

cat > "$SANDBOX/runtime/.env" <<'EOF'
APPLE_ID_USER=test@example.invalid
APPLE_ID_PASS=test-password
EOF
chmod 600 "$SANDBOX/runtime/.env"
cat > "$SANDBOX/bin/sleep" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
chmod +x "$SANDBOX/bin/sleep"
cat > "$SANDBOX/bin/docker" <<'EOF'
#!/usr/bin/env bash
target="${*: -1}"
case "${1:-}" in
  info) exit 0 ;;
  inspect)
    case "$target" in
      amd-wrapper-login) [ -e "$LOGIN_STATE" ] || exit 1 ;;
      amd-wrapper-auth) [ -e "$AUTH_STATE" ] || exit 1 ;;
      *) exit 1 ;;
    esac
    if [ "${2:-}" = --format ]; then printf 'true\n'; fi
    ;;
  start) exit 0 ;;
  exec) cat "$TCP_TABLE" ;;
  logs) printf 'Waiting for input\n' ;;
  rm)
    if [ -n "${DOCKER_RM_LOG:-}" ]; then printf '%s\n' "$target" >> "$DOCKER_RM_LOG"; fi
    case "$target" in
      amd-wrapper-login) rm -f "$LOGIN_STATE" ;;
      amd-wrapper-auth) rm -f "$AUTH_STATE" ;;
    esac
    ;;
  run)
    printf '%s\n' "$*" > "$DOCKER_RUN_LOG"
    if [[ " $* " == *' --name amd-wrapper-login '* ]]; then
      : > "$LOGIN_STATE"
    else
      : > "$AUTH_STATE"
    fi
    ;;
  *) exit 97 ;;
esac
EOF
chmod +x "$SANDBOX/bin/docker"

rm -f "$SANDBOX/login-state" "$SANDBOX/auth-state" "$SANDBOX/docker-run-args"
if AMD_BASE="$SANDBOX/runtime" TCP_TABLE="$SANDBOX/tcp-healthy" \
  LOGIN_STATE="$SANDBOX/login-state" AUTH_STATE="$SANDBOX/auth-state" \
  DOCKER_RUN_LOG="$SANDBOX/docker-run-args" PATH="$SANDBOX/bin:$PATH" \
  bash "$ROOT/login.sh" start >/dev/null 2>&1 &&
  grep -Fq -- '--name amd-wrapper-login' "$SANDBOX/docker-run-args" &&
  ! grep -Fq -- ' -L ' "$SANDBOX/docker-run-args"
then
  pass "login rebuilds a missing runtime from cached session without credentials"
else
  fail "login rebuilds a missing runtime from cached session without credentials"
fi

: > "$SANDBOX/login-state"
rm -f "$SANDBOX/auth-state" "$SANDBOX/docker-run-args"
if AMD_BASE="$SANDBOX/runtime" TCP_TABLE="$SANDBOX/tcp-missing-port" \
  LOGIN_STATE="$SANDBOX/login-state" AUTH_STATE="$SANDBOX/auth-state" \
  DOCKER_RUN_LOG="$SANDBOX/docker-run-args" PATH="$SANDBOX/bin:$PATH" \
  bash "$ROOT/login.sh" start >/dev/null 2>&1
then
  fail "login blocks authentication while an unhealthy runtime is preserved"
elif [ -e "$SANDBOX/docker-run-args" ]; then
  fail "login blocks authentication while an unhealthy runtime is preserved"
else
  pass "login blocks authentication while an unhealthy runtime is preserved"
fi

rm -f "$SANDBOX/runtime/wrapper-data/data/com.apple.android.music/files/mpl_db/kvs.sqlitedb"
dd if=/dev/zero of="$SANDBOX/runtime/wrapper-data/data/com.apple.android.music/files/mpl_db/kvs.sqlitedb" bs=4096 count=1 status=none
mv "$SANDBOX/runtime/.env" "$SANDBOX/runtime/.env.saved"
rm -f "$SANDBOX/login-state" "$SANDBOX/auth-state" "$SANDBOX/docker-run-args"
if AMD_BASE="$SANDBOX/runtime" TCP_TABLE="$SANDBOX/tcp-healthy" \
  LOGIN_STATE="$SANDBOX/login-state" AUTH_STATE="$SANDBOX/auth-state" \
  DOCKER_RUN_LOG="$SANDBOX/docker-run-args" PATH="$SANDBOX/bin:$PATH" \
  bash "$ROOT/login.sh" interactive </dev/null >/dev/null 2>&1
then
  fail "interactive login stops before auth when credentials are missing"
elif [ -e "$SANDBOX/docker-run-args" ]; then
  fail "interactive login stops before auth when credentials are missing"
else
  pass "interactive login stops before auth when credentials are missing"
fi
mv "$SANDBOX/runtime/.env.saved" "$SANDBOX/runtime/.env"

: > "$SANDBOX/auth-state"
rm -f "$SANDBOX/login-state" "$SANDBOX/docker-run-args" "$SANDBOX/docker-rm-log"
if AMD_BASE="$SANDBOX/runtime" TCP_TABLE="$SANDBOX/tcp-healthy" \
  LOGIN_STATE="$SANDBOX/login-state" AUTH_STATE="$SANDBOX/auth-state" \
  DOCKER_RUN_LOG="$SANDBOX/docker-run-args" DOCKER_RM_LOG="$SANDBOX/docker-rm-log" \
  PATH="$SANDBOX/bin:$PATH" bash "$ROOT/login.sh" start >/dev/null 2>&1
then
  fail "login preserves an active authentication attempt"
elif [ -e "$SANDBOX/docker-run-args" ] || grep -Fq 'amd-wrapper-auth' "$SANDBOX/docker-rm-log" 2>/dev/null
then
  fail "login preserves an active authentication attempt"
else
  pass "login preserves an active authentication attempt"
fi

if (( failures )); then
  printf '%d shell regression assertion(s) failed\n' "$failures" >&2
  exit 1
fi
printf 'all shell regression assertions passed\n'
