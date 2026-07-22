#!/usr/bin/env bash
set -u

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
SANDBOX="$(mktemp -d)"
trap 'rm -rf "$SANDBOX"' EXIT

failures=0
pass() { printf 'PASS %s\n' "$1"; }
fail() { printf 'FAIL %s\n' "$1" >&2; failures=$((failures + 1)); }

RUNTIME="$SANDBOX/runtime"
BIN="$SANDBOX/bin"
mkdir -p "$RUNTIME/config" "$RUNTIME/downloads" "$BIN"
: > "$RUNTIME/config/config.yaml"

cat > "$RUNTIME/login.sh" <<'EOF'
#!/usr/bin/env bash
[ "${1:-}" = status ]
EOF
chmod +x "$RUNTIME/login.sh"

cat > "$BIN/docker" <<'EOF'
#!/usr/bin/env bash
if [ "${1:-}" = run ]; then
  printf '%s\n' "$@" > "$DOCKER_ARGV_LOG"
  printf '{MediaUserToken:super-secret-token AuthorizationToken:super-secret-auth}\n'
  exit 0
fi
if [ "${1:-}" = rm ]; then
  exit 0
fi
exit 97
EOF
chmod +x "$BIN/docker"

run_download() {
  local output_file=$1
  local argv_file=$2
  shift 2
  AMD_BASE="$RUNTIME" PATH="$BIN:$PATH" DOCKER_ARGV_LOG="$argv_file" \
    EZ_DOWNLOAD_CONTAINER="amd-downloader-args-test" \
    bash "$ROOT/dl.sh" "$@" >"$output_file" 2>&1
}

all_args_present() {
  local argv_file=$1
  shift
  local expected
  for expected in "$@"; do
    grep -Fxq -- "$expected" "$argv_file" || return 1
  done
}

assert_profile() {
  local profile=$1
  shift
  local output="$SANDBOX/output-$profile"
  local argv="$SANDBOX/argv-$profile"
  if run_download "$output" "$argv" \
      'https://music.apple.com/us/album/example/123' "$profile" &&
     all_args_present "$argv" "$@"
  then
    pass "profile $profile maps to upstream flags"
  else
    fail "profile $profile maps to upstream flags"
  fi
}

assert_profile alac-48 --alac-max 48000
assert_profile alac-96 --alac-max 96000
assert_profile alac-192 --alac-max 192000
assert_profile atmos --atmos
assert_profile aac --aac

output="$SANDBOX/output-tracks"
argv="$SANDBOX/argv-tracks"
if run_download "$output" "$argv" \
    'https://music.apple.com/us/album/example/123?l=zh-Hans-CN&i=999' \
    alac-96 111 222 &&
   grep -Fxq -- '--song' "$argv" &&
   grep -Fxq -- 'https://music.apple.com/us/album/example/123?l=zh-Hans-CN&i=111' "$argv" &&
   grep -Fxq -- 'https://music.apple.com/us/album/example/123?l=zh-Hans-CN&i=222' "$argv" &&
   ! grep -Fq -- 'i=999' "$argv" &&
   [ "$(grep -Fc -- 'https://music.apple.com/us/album/example/123?l=zh-Hans-CN&i=' "$argv")" -eq 2 ]
then
  pass "multiple track IDs become separate song URL arguments"
else
  fail "multiple track IDs become separate song URL arguments"
fi

output="$SANDBOX/output-existing-i"
argv="$SANDBOX/argv-existing-i"
if run_download "$output" "$argv" \
    'https://music.apple.com/us/album/example/123?l=zh-Hans-CN&i=333' \
    aac &&
   grep -Fxq -- '--song' "$argv" &&
   grep -Fxq -- 'https://music.apple.com/us/album/example/123?l=zh-Hans-CN&i=333' "$argv"
then
  pass "existing numeric i parameter enables single-song mode"
else
  fail "existing numeric i parameter enables single-song mode"
fi

for invalid_case in \
  'alac-44' \
  'alac-48 1x' \
  'alac-48 -1'
do
  set -- $invalid_case
  output="$SANDBOX/output-invalid-${1//[^a-zA-Z0-9]/_}"
  argv="$SANDBOX/argv-invalid-${1//[^a-zA-Z0-9]/_}"
  rm -f "$argv"
  if run_download "$output" "$argv" \
      'https://music.apple.com/us/album/example/123' "$@"
  then
    fail "invalid download arguments are rejected: $invalid_case"
  elif [ -e "$argv" ]; then
    fail "invalid download arguments reach Docker: $invalid_case"
  else
    pass "invalid download arguments are rejected: $invalid_case"
  fi
done

output="$SANDBOX/output-stages"
argv="$SANDBOX/argv-stages"
if run_download "$output" "$argv" \
    'https://music.apple.com/us/album/example/123' alac-192 &&
   awk '
     /^\[stage\] validating$/ { if (state == 0) state = 1; next }
     /^\[stage\] wrapper$/    { if (state == 1) state = 2; next }
     /^\[stage\] container$/  { if (state == 2) state = 3; next }
     /^\[stage\] download$/   { if (state == 3) state = 4; next }
     /^\[stage\] complete$/   { if (state == 4) state = 5; next }
     END { exit state == 5 ? 0 : 1 }
   ' "$output"
then
  pass "download emits ordered stage markers"
else
  fail "download emits ordered stage markers"
fi

if ! grep -Fq -- 'super-secret-token' "$output" &&
   ! grep -Fq -- 'super-secret-auth' "$output" &&
   grep -Fq -- 'MediaUserToken:<redacted>' "$output" &&
   grep -Fq -- 'AuthorizationToken:<redacted>' "$output"
then
  pass "download redacts upstream token fields"
else
  fail "download redacts upstream token fields"
fi

if (( failures )); then
  printf '%d dl.sh assertion(s) failed\n' "$failures" >&2
  exit 1
fi
printf 'all dl.sh argument assertions passed\n'
