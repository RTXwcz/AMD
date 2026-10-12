#!/usr/bin/env bash
# ez-am-downloader :: deployment engine
# Runs inside WSL and installs the canonical runtime under AMD_BASE.
set -euo pipefail
umask 077

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
AMD_BASE="${AMD_BASE:-${BASE:-$HOME/amd}}"
BASE="$AMD_BASE"
CFG_DIR="${EZ_AM_CONFIG_DIR:-$BASE/config}"
WRAPPER_DIR="${EZ_AM_CACHE_DIR:-$BASE/wrapper-bin}"
DATA_DIR="${EZ_AM_STATE_DIR:-$BASE/wrapper-data}"
DL_DIR="${EZ_AM_DOWNLOADS_DIR:-$BASE/downloads}"
ENV_FILE="$CFG_DIR/.env"
CFG_FILE="$CFG_DIR/config.yaml"
WRAPPER_IMAGE="${WRAPPER_IMAGE:-local/wrapper:latest}"
DOWNLOADER_IMAGE="${DOWNLOADER_IMAGE:-ghcr.io/zhaarey/apple-music-downloader@sha256:16045b4d1c05fca312d301c3e9dc82e70fcb45ea723fe6df71258364a3cb7e4d}"
WRAPPER_URL="https://github.com/WorldObservationLog/wrapper/releases/download/wrapper.x86_64.latest/Wrapper.x86_64.latest.zip"

is_wsl() {
  [ -r /proc/sys/kernel/osrelease ] &&
    grep -qi microsoft /proc/sys/kernel/osrelease
}

docker_help() {
  if is_wsl; then
    echo "[!] Docker is not available in this WSL distro." >&2
    echo "[!] Start Docker Desktop, then enable Settings > Resources > WSL Integration for this distro." >&2
  else
    echo "[!] Docker is unavailable. Install and start Docker before running setup." >&2
  fi
}

if ! command -v docker >/dev/null 2>&1; then
  docker_help
  exit 1
fi
if ! docker info >/dev/null 2>&1; then
  docker_help
  exit 1
fi
if ! docker compose version >/dev/null 2>&1; then
  echo "[!] The Docker Compose plugin is unavailable. Enable or repair Docker Desktop integration." >&2
  exit 1
fi

mkdir -p "$BASE" "$WRAPPER_DIR" "$DATA_DIR" "$DL_DIR" "$CFG_DIR"
chmod 700 "$CFG_DIR"

# Pre-unified installations kept credentials at the data root.  Move that
# secret into the explicit config directory before the normal backup/write
# path, then keep exactly one authoritative location.
LEGACY_ENV_FILE="$BASE/.env"
if [ "$ENV_FILE" != "$LEGACY_ENV_FILE" ] && [ ! -e "$ENV_FILE" ] && [ -f "$LEGACY_ENV_FILE" ]; then
  cp -p -- "$LEGACY_ENV_FILE" "$ENV_FILE"
  chmod 600 "$ENV_FILE"
  rm -f -- "$LEGACY_ENV_FILE"
  echo "==> Migrated legacy credentials into the config directory"
fi

echo "==> Pulling pinned downloader image"
docker pull "$DOWNLOADER_IMAGE"

if ! docker image inspect "$WRAPPER_IMAGE" >/dev/null 2>&1; then
  WRAPPER_ZIP="$WRAPPER_DIR/wrapper.zip"

  if [ ! -f "$WRAPPER_ZIP" ]; then
    if ! command -v curl >/dev/null 2>&1; then
      echo "[!] curl is required to fetch the wrapper release." >&2
      exit 1
    fi
    echo "==> Downloading wrapper release"
    curl --fail --silent --show-error --location \
      --output "$WRAPPER_ZIP.part" "$WRAPPER_URL"
    mv -- "$WRAPPER_ZIP.part" "$WRAPPER_ZIP"
  fi

  if [ ! -f "$WRAPPER_DIR/wrapper" ] || [ ! -f "$WRAPPER_DIR/Dockerfile" ]; then
    echo "==> Extracting wrapper release"
    if command -v unzip >/dev/null 2>&1; then
      unzip -o "$WRAPPER_ZIP" -d "$WRAPPER_DIR" >/dev/null
    elif command -v python3 >/dev/null 2>&1; then
      python3 -c 'import sys, zipfile; zipfile.ZipFile(sys.argv[1]).extractall(sys.argv[2])' \
        "$WRAPPER_ZIP" "$WRAPPER_DIR"
    else
      echo "[!] unzip or python3 is required to extract the wrapper release." >&2
      exit 1
    fi
  fi

  echo "==> Building local wrapper image"
  docker build --tag "$WRAPPER_IMAGE" "$WRAPPER_DIR"
else
  echo "==> Reusing existing local wrapper image"
fi

if [ -t 0 ]; then
  IFS= read -r -p "Apple ID email: " APPLE_USER
  IFS= read -r -s -p "Apple ID password: " APPLE_PASS
  printf '\n'
  IFS= read -r -s -p "media-user-token: " MEDIA_TOKEN
  printf '\n'
  IFS= read -r -p "storefront (for example us, jp, hk) [us]: " STOREFRONT
  STOREFRONT="${STOREFRONT:-us}"
else
  if [ -z "${EZ_USER:-}" ] || [ -z "${EZ_PASS:-}" ] ||
     [ -z "${EZ_TOKEN:-}" ] || [ -z "${EZ_STOREFRONT:-}" ]; then
    echo "[!] Non-interactive setup requires EZ_USER, EZ_PASS, EZ_TOKEN, and EZ_STOREFRONT." >&2
    exit 1
  fi
  APPLE_USER="$EZ_USER"
  APPLE_PASS="$EZ_PASS"
  MEDIA_TOKEN="$EZ_TOKEN"
  STOREFRONT="$EZ_STOREFRONT"
fi

reject_line_breaks() {
  local label="$1"
  local value="$2"
  case "$value" in
    *$'\n'*|*$'\r'*)
      echo "[!] $label must not contain a line break." >&2
      exit 1
      ;;
  esac
}

reject_line_breaks "Apple ID" "$APPLE_USER"
reject_line_breaks "Apple password" "$APPLE_PASS"
reject_line_breaks "media-user-token" "$MEDIA_TOKEN"
reject_line_breaks "storefront" "$STOREFRONT"

if [[ ! "$STOREFRONT" =~ ^[[:alpha:]]{2}$ ]]; then
  echo "[!] storefront must be a two-letter code such as us, jp, or hk." >&2
  exit 1
fi
STOREFRONT="${STOREFRONT,,}"

yaml_quote() {
  local value="$1"
  value="${value//\\/\\\\}"
  value="${value//\"/\\\"}"
  printf '"%s"' "$value"
}

MEDIA_TOKEN_YAML="$(yaml_quote "$MEDIA_TOKEN")"
BACKUP_STAMP="$(date -u +%Y%m%dT%H%M%SZ).$$"

backup_existing() {
  local path="$1"
  if [ -f "$path" ]; then
    cp -p -- "$path" "$path.bak.$BACKUP_STAMP"
    chmod 600 "$path.bak.$BACKUP_STAMP"
    echo "==> Backed up existing $(basename "$path")"
  fi
}

backup_existing "$ENV_FILE"
backup_existing "$CFG_FILE"

ENV_TMP="$CFG_DIR/.env.tmp.$$"
CFG_TMP="$CFG_DIR/config.yaml.tmp.$$"
cleanup_secret_temps() {
  rm -f -- "$ENV_TMP" "$CFG_TMP"
}
trap cleanup_secret_temps EXIT

printf 'APPLE_ID_USER=%s\nAPPLE_ID_PASS=%s\n' \
  "$APPLE_USER" "$APPLE_PASS" > "$ENV_TMP"
chmod 600 "$ENV_TMP"

cat > "$CFG_TMP" <<EOF
media-user-token: $MEDIA_TOKEN_YAML
authorization-token: "your-authorization-token"
language: ""
lrc-type: "lyrics"
lrc-format: "lrc"
embed-lrc: true
save-lrc-file: false
save-artist-cover: false
save-animated-artwork: false
emby-animated-artwork: false
embed-cover: true
cover-size: 5000x5000
cover-format: jpg
tag-sort-order: true
tag-itunes-id: true
alac-save-folder: "/downloads/ALAC"
atmos-save-folder: "/downloads/Atmos"
aac-save-folder: "/downloads/AAC"
mv-save-folder: "/downloads/MV"
max-memory-limit: 256
decrypt-m3u8-port: "127.0.0.1:10020"
get-m3u8-port: "127.0.0.1:20020"
get-m3u8-from-device: true
exit-on-error: true
get-m3u8-mode: hires
aac-type: aac-lc
alac-max: 192000
atmos-max: 2768
limit-max: 200
album-folder-format: "{AlbumName}"
playlist-folder-format: "{PlaylistName}"
song-file-format: "{SongNumer}. {SongName}"
artist-folder-format: "{UrlArtistName}"
explicit-choice: "[E]"
clean-choice: "[C]"
apple-master-choice: "[M]"
use-songinfo-for-playlist: false
dl-albumcover-for-playlist: false
mv-audio-type: atmos
mv-max: 2160
storefront: "$STOREFRONT"
alac-fix: false
convert-after-download: false
EOF
chmod 600 "$CFG_TMP"

mv -- "$ENV_TMP" "$ENV_FILE"
mv -- "$CFG_TMP" "$CFG_FILE"
chmod 600 "$ENV_FILE" "$CFG_FILE"
chmod 700 "$CFG_DIR"
trap - EXIT

echo
echo "=== SETUP COMPLETE ==="
echo "Runtime: $BASE"
echo "Next: bash \"$SCRIPT_DIR/login.sh\""
echo "Downloads and wrapper session data were preserved."
