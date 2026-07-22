#!/usr/bin/env bash
# Synchronize runtime files without regenerating existing credentials or data.
set -euo pipefail

BASE="${1:-/home/rtxwcz/amd}"
SRC="${2:-/mnt/d/amd}"
OWNER="${OWNER:-rtxwcz}"

if [ "$(id -u)" -ne 0 ]; then
  echo "[!] run this synchronizer as WSL root" >&2
  exit 1
fi
if [ "$BASE" != "/home/$OWNER/amd" ] || [ "$SRC" != "/mnt/d/amd" ]; then
  echo "[!] refusing an unexpected source or destination" >&2
  exit 1
fi

for path in "$BASE/.env" "$BASE/config/config.yaml" "$BASE/downloads" "$BASE/wrapper-data"; do
  [ -e "$path" ] || { echo "[!] missing required path: $path" >&2; exit 1; }
done
for name in setup.sh dl.sh login.sh compose.yaml; do
  [ -f "$SRC/$name" ] || { echo "[!] missing canonical file: $SRC/$name" >&2; exit 1; }
done

file_set_signature() {
  local path="$1"
  find "$path" -type f -printf '%P\0%s\0' | LC_ALL=C sort -z | sha256sum | awk '{print $1}'
}

config_without_exit_signature() {
  sed '/^[[:space:]]*exit-on-error:/d' "$1" | sha256sum | awk '{print $1}'
}

env_before="$(sha256sum "$BASE/.env" | awk '{print $1}')"
config_before="$(config_without_exit_signature "$BASE/config/config.yaml")"
downloads_before="$(file_set_signature "$BASE/downloads")"
wrapper_before="$(file_set_signature "$BASE/wrapper-data")"

for spec in setup.sh:0755 dl.sh:0755 login.sh:0755 compose.yaml:0644; do
  name="${spec%%:*}"
  mode="${spec##*:}"
  temporary="$BASE/.$name.new.$$"
  install -o "$OWNER" -g "$OWNER" -m "$mode" "$SRC/$name" "$temporary"
  mv -fT "$temporary" "$BASE/$name"
  cmp -s "$SRC/$name" "$BASE/$name"
done

config="$BASE/config/config.yaml"
[ "$(grep -Ec '^[[:space:]]*exit-on-error:' "$config")" -eq 1 ]
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
backup="$config.bak.repair-$stamp"
cp -p -- "$config" "$backup"
chown "$OWNER:$OWNER" "$backup"
chmod 600 "$backup"
temporary="$BASE/config/.config.yaml.new.$$"
sed -E 's/^([[:space:]]*exit-on-error:[[:space:]]*).*/\1true/' "$config" > "$temporary"
chown "$OWNER:$OWNER" "$temporary"
chmod 600 "$temporary"
[ "$(config_without_exit_signature "$temporary")" = "$config_before" ]
mv -fT "$temporary" "$config"

chown "$OWNER:$OWNER" "$BASE/.env" "$BASE/config" "$config"
chmod 600 "$BASE/.env" "$config"
chmod 700 "$BASE/config"
find "$BASE/wrapper-data" -type d -exec chown root:"$OWNER" {} + -exec chmod 2750 {} +
find "$BASE/wrapper-data" -type f -exec chown root:"$OWNER" {} + -exec chmod 0640 {} +

[ "$(sha256sum "$BASE/.env" | awk '{print $1}')" = "$env_before" ]
[ "$(config_without_exit_signature "$config")" = "$config_before" ]
[ "$(file_set_signature "$BASE/downloads")" = "$downloads_before" ]
[ "$(file_set_signature "$BASE/wrapper-data")" = "$wrapper_before" ]

printf 'SYNC_OK\n'
printf 'downloads_files='; find "$BASE/downloads" -type f -printf . | wc -c
printf 'downloads_bytes='; du -sb "$BASE/downloads" | awk '{print $1}'
printf 'wrapper_files='; find "$BASE/wrapper-data" -type f -printf . | wc -c
printf 'wrapper_bytes='; du -sb "$BASE/wrapper-data" | awk '{print $1}'
