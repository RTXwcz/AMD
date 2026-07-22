#!/usr/bin/env bash
set -euo pipefail

if (( $# != 2 )); then
  echo "usage: verify_album_media.sh <album-directory> <expected-track-count>" >&2
  exit 2
fi

album_dir="$1"
expected_count="$2"
[[ "$expected_count" =~ ^[0-9]+$ ]]
[ -d "$album_dir" ]
command -v ffprobe >/dev/null 2>&1

mapfile -d '' media_files < <(find "$album_dir" -maxdepth 1 -type f -name '*.m4a' -print0 | sort -z)
if (( ${#media_files[@]} != expected_count )); then
  echo "[!] found ${#media_files[@]} M4A files, expected $expected_count" >&2
  exit 1
fi

for media_file in "${media_files[@]}"; do
  probe="$(ffprobe -v error -select_streams a:0 \
    -show_entries stream=codec_name,sample_rate,channels,bits_per_raw_sample \
    -of csv=p=0 "$media_file")"
  IFS=',' read -r codec sample_rate channels bit_depth <<< "$probe"
  if [ "$codec" != alac ] || [ "$sample_rate" != 48000 ] || [ "$channels" != 2 ] || [ "$bit_depth" != 24 ]; then
    echo "[!] unexpected audio stream for $media_file: $probe" >&2
    exit 1
  fi
  printf 'MEDIA_OK codec=%s sample_rate=%s channels=%s bit_depth=%s file=%s\n' \
    "$codec" "$sample_rate" "$channels" "$bit_depth" "$media_file"
  ffprobe -v error -show_entries format=duration,size:format_tags=title,album,artist,track \
    -of default=noprint_wrappers=1 "$media_file"
done
