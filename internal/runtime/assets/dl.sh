#!/usr/bin/env bash
# ez-am-downloader :: one-shot download engine
# Usage: dl.sh <apple-music-url> [profile] [track-id ...]
set -euo pipefail

echo "[stage] validating"

if (( $# < 1 )); then
  echo "usage: dl.sh <apple-music-url> [alac-48|alac-96|alac-192|atmos|aac] [track-id ...]" >&2
  exit 2
fi

URL="$1"
REQUESTED_PROFILE="${2:-alac-192}"
track_ids=("${@:3}")

case "$URL" in
  https://music.apple.com/*) ;;
  *)
    echo "[!] URL must start with https://music.apple.com/" >&2
    exit 2
    ;;
esac

format_args=()
case "$REQUESTED_PROFILE" in
  alac-48)
    FORMAT="alac"
    PROFILE="alac-48"
    format_args=(--alac-max 48000)
    ;;
  alac-96)
    FORMAT="alac"
    PROFILE="alac-96"
    format_args=(--alac-max 96000)
    ;;
  alac-192|alac|hires)
    FORMAT="alac"
    PROFILE="alac-192"
    format_args=(--alac-max 192000)
    ;;
  atmos)
    FORMAT="atmos"
    PROFILE="atmos"
    format_args=(--atmos)
    ;;
  aac)
    FORMAT="aac"
    PROFILE="aac"
    format_args=(--aac)
    ;;
  *)
    echo "[!] profile must be one of: alac-48, alac-96, alac-192, atmos, aac" >&2
    exit 2
    ;;
esac

for track_id in "${track_ids[@]}"; do
  if [[ ! "$track_id" =~ ^[0-9]+$ ]]; then
    echo "[!] track IDs must contain digits only" >&2
    exit 2
  fi
done

if (( ${#track_ids[@]} > 0 )) && [[ "$URL" != *"/album/"* ]]; then
  echo "[!] track IDs require an Apple Music album URL" >&2
  exit 2
fi

# Split and rebuild the query so an existing i= is replaced, never duplicated.
URL_WITHOUT_FRAGMENT="$URL"
FRAGMENT=""
if [[ "$URL_WITHOUT_FRAGMENT" == *#* ]]; then
  FRAGMENT="#${URL_WITHOUT_FRAGMENT#*#}"
  URL_WITHOUT_FRAGMENT="${URL_WITHOUT_FRAGMENT%%#*}"
fi

URL_PATH="$URL_WITHOUT_FRAGMENT"
QUERY=""
if [[ "$URL_WITHOUT_FRAGMENT" == *\?* ]]; then
  URL_PATH="${URL_WITHOUT_FRAGMENT%%\?*}"
  QUERY="${URL_WITHOUT_FRAGMENT#*\?}"
fi

existing_track_id=""
query_parts=()
if [[ -n "$QUERY" ]]; then
  IFS='&' read -r -a raw_query_parts <<< "$QUERY"
  for query_part in "${raw_query_parts[@]}"; do
    if [[ "${query_part%%=*}" == i ]]; then
      candidate="${query_part#*=}"
      if [[ "$query_part" != *=* || ! "$candidate" =~ ^[0-9]+$ ]]; then
        echo "[!] URL i parameter must be a numeric track ID" >&2
        exit 2
      fi
      if [[ -n "$existing_track_id" && "$existing_track_id" != "$candidate" ]]; then
        echo "[!] URL contains conflicting i parameters" >&2
        exit 2
      fi
      existing_track_id="$candidate"
    elif [[ -n "$query_part" ]]; then
      query_parts+=("$query_part")
    fi
  done
fi

BASE_URL="$URL_PATH"
if (( ${#query_parts[@]} > 0 )); then
  printf -v CLEAN_QUERY '%s&' "${query_parts[@]}"
  BASE_URL+="?${CLEAN_QUERY%&}"
fi

append_track_id() {
  local base_url=$1
  local track_id=$2
  local separator='?'
  if [[ "$base_url" == *\?* ]]; then
    separator='&'
  fi
  printf '%s%s%s%s' "$base_url" "$separator" "i=$track_id" "$FRAGMENT"
}

target_urls=()
if (( ${#track_ids[@]} > 0 )); then
  format_args+=(--song)
  for track_id in "${track_ids[@]}"; do
    target_urls+=("$(append_track_id "$BASE_URL" "$track_id")")
  done
elif [[ -n "$existing_track_id" ]]; then
  if [[ "$URL_PATH" != *"/album/"* ]]; then
    echo "[!] URL i parameter requires an Apple Music album URL" >&2
    exit 2
  fi
  format_args+=(--song)
  target_urls+=("$(append_track_id "$BASE_URL" "$existing_track_id")")
else
  target_urls+=("$URL")
fi

AMD_BASE="${AMD_BASE:-$HOME/amd}"
LOGIN_SCRIPT="$AMD_BASE/login.sh"
DOWNLOAD_DIR="$AMD_BASE/downloads"
CONFIG_FILE="$AMD_BASE/config/config.yaml"
LOGIN_CONTAINER="amd-wrapper-login"
DOWNLOADER_IMAGE="${DOWNLOADER_IMAGE:-ghcr.io/zhaarey/apple-music-downloader@sha256:16045b4d1c05fca312d301c3e9dc82e70fcb45ea723fe6df71258364a3cb7e4d}"
DOWNLOAD_CONTAINER="${EZ_DOWNLOAD_CONTAINER:-amd-downloader-$$}"

if [[ ! "$DOWNLOAD_CONTAINER" =~ ^amd-downloader-[a-z0-9][a-z0-9_.-]{0,80}$ ]]; then
  echo "[!] invalid downloader container name" >&2
  exit 2
fi

if [ ! -f "$LOGIN_SCRIPT" ] || [ ! -r "$CONFIG_FILE" ] || [ ! -d "$DOWNLOAD_DIR" ]; then
  echo "[!] Runtime files are incomplete under $AMD_BASE. Run setup.sh first." >&2
  exit 1
fi

# login.sh status is the only wrapper readiness and lifecycle contract.
echo "[stage] wrapper"
if ! bash "$LOGIN_SCRIPT" status >/dev/null 2>&1; then
  echo "[!] No healthy logged-in wrapper is available." >&2
  echo "[!] Run explicitly: bash \"$LOGIN_SCRIPT\" start" >&2
  echo "[!] Or complete login: bash \"$LOGIN_SCRIPT\" interactive" >&2
  exit 1
fi

download_args=(
  run --rm
  --name "$DOWNLOAD_CONTAINER"
  --network "container:$LOGIN_CONTAINER"
  --volume "$DOWNLOAD_DIR:/downloads"
  --volume "$CONFIG_FILE:/app/config.yaml:ro"
  "$DOWNLOADER_IMAGE"
)

DOWNLOAD_STARTED=false
cleanup_download() {
  local status=$?
  trap - EXIT INT TERM HUP
  if [ "$DOWNLOAD_STARTED" = true ]; then
    docker rm -f "$DOWNLOAD_CONTAINER" >/dev/null 2>&1 || true
  fi
  exit "$status"
}

handle_download_signal() {
  exit 130
}

redact_downloader_output() {
  sed -u -E \
    -e 's/(MediaUserToken:)[^[:space:]}]+/\1<redacted>/g' \
    -e 's/(AuthorizationToken:)[^[:space:]}]+/\1<redacted>/g' \
    -e 's/(media-user-token[=:][[:space:]]*)[^[:space:]}]+/\1<redacted>/g'
}

trap cleanup_download EXIT
trap handle_download_signal INT TERM HUP

echo "[stage] container"
echo "[*] profile: $PROFILE  format: $FORMAT  targets: ${#target_urls[@]}"
echo "[stage] download"
DOWNLOAD_STARTED=true
if docker "${download_args[@]}" "${format_args[@]}" "${target_urls[@]}" 2>&1 | redact_downloader_output; then
  DOWNLOAD_STARTED=false
  echo "[stage] complete"
  echo "[*] done -> $DOWNLOAD_DIR"
else
  status=$?
  DOWNLOAD_STARTED=false
  echo "[!] downloader failed with exit code $status" >&2
  exit "$status"
fi
