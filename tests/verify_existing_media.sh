#!/usr/bin/env bash
set -euo pipefail

DOWNLOAD_DIR="${1:-$HOME/amd/downloads}"
command -v ffprobe >/dev/null 2>&1

media_file=""
while IFS= read -r -d '' candidate; do
  media_file="$candidate"
  break
done < <(find "$DOWNLOAD_DIR" -type f -name '*.m4a' -print0)

if [ -z "$media_file" ]; then
  echo "[!] no existing M4A file found under $DOWNLOAD_DIR" >&2
  exit 1
fi

printf 'FILE=%s\n' "$media_file"
ffprobe -v error -select_streams a:0 \
  -show_entries stream=codec_name,codec_type,sample_rate,channels,bits_per_raw_sample \
  -of default=noprint_wrappers=1 "$media_file"
