# ez-am-downloader

A small Windows + WSL2 operator layer around two upstream projects:

- [zhaarey/apple-music-downloader](https://github.com/zhaarey/apple-music-downloader) — fetches Apple Music tracks
- [WorldObservationLog/wrapper](https://github.com/WorldObservationLog/wrapper) — decrypts them (needs an active Apple Music subscription)

It exists to make the boring parts repeatable: one deployment engine, one
wrapper lifecycle owner, one download entry point, and no secret ever written
into a script or a log.

On Windows you normally use it through the two front ends in `dist/`: run
`ez-am-deploy.exe` once (wizard → images → 2FA), then `ez-am-downloader.exe`
whenever you want media. The shell scripts those two drive are the same engine,
documented below for headless installs and for anyone who prefers a terminal.

Intended for personal backups of content you are entitled to access. Apple
Music subscription terms, DRM restrictions, upstream availability and local law
still apply. This project does not bypass Apple account security.

## What's in the box

| File | Role |
| --- | --- |
| `setup.sh` | Deployment engine. Checks Docker, pulls the pinned downloader image, builds or reuses the local wrapper image, and writes `~/amd/.env` + `~/amd/config/config.yaml`. |
| `login.sh` | The only owner of the wrapper lifecycle (`start` / `submit` / `status` / `interactive`). Runs Apple authentication in a visible terminal so a 2FA code never passes through a chat or a log. |
| `dl.sh` | One-shot download engine. Refuses to run without a healthy wrapper and runs the downloader in the wrapper's network namespace. |
| `compose.yaml` | Reference only: mounts, loopback ports and the canonical container name. `login.sh` is the supported entry point. |
| `sync_existing_wsl.sh` | Guarded synchronizer for an existing deployment: installs the runtime files without regenerating credentials or touching media/session data. Its safety guard expects the checkout at `/mnt/d/amd` and the runtime owner `rtxwcz`; set `SRC_EXPECTED` and `OWNER` for a different layout. |
| `dist/ez-am-deploy.exe`, `dist/ez-am-downloader.exe` | Prebuilt Windows front ends (wizard + downloader TUI). Both are Go, no runtime needed. |
| `deploy/`, `downloader/` | Their Go sources, plus the tests that cover them. |
| `tests/` | Shell regression suite plus media verification helpers. |

## Requirements

1. Windows 10/11 with WSL2 and an Ubuntu-compatible distribution.
2. Docker reachable **from inside that distro** — either a Docker daemon
   installed in the distro itself, or Docker Desktop with WSL integration
   enabled for it.
3. Docker BuildKit (the `docker buildx` CLI plugin). The wrapper image's
   upstream `Dockerfile` uses `RUN --mount=type=cache`, which the legacy builder
   rejects with `the --mount option requires BuildKit`.
4. An active Apple Music subscription.
5. A `media-user-token` copied from an authenticated `music.apple.com` session.
6. Network access to `github.com` and `ghcr.io` during installation.

Keep a trusted Apple device at hand: the wrapper login may require 2FA.

## Quick start (Windows, recommended)

The intended way to use this project on Windows is two double-clicks. No shell,
no typed commands.

1. **Get the checkout.** Download the ZIP (green *Code* button) or
   `git clone https://github.com/RTXwcz/AMD`, then unpack it somewhere permanent
   such as `D:\amd`. Keep the layout intact — the wizard looks for `setup.sh`
   next to its own executable or one directory above it, and that is exactly
   what `dist\ez-am-deploy.exe` sees.
2. **Double-click `dist\ez-am-deploy.exe`** and follow the wizard:

   | Step | What you provide |
   | --- | --- |
   | 1/3 Account | Apple ID email, password, storefront (default `us`) |
   | 2/3 media-user-token | the token copied from `music.apple.com` DevTools (see below) |
   | 3/3 Deploying | nothing — it checks Docker, pulls the pinned images, builds the wrapper image and writes the config |
   | Additional verification — 2FA | the 6-digit code Apple sends to your trusted device |

   It ends with `✓ Deployment complete!` and points you at the downloader.
3. **Double-click `dist\ez-am-downloader.exe`.** Paste an Apple Music link, pick
   tracks and quality with the keyboard, press Enter. Finished media shows up in
   `\\wsl.localhost\Ubuntu\home\<you>\amd\downloads`.

Windows SmartScreen may warn about an unsigned binary the first time you run it:
choose *More info → Run anyway*.

The wizard needs WSL2 with a distro, Docker reachable from that distro, an Apple
Music subscription and GitHub access. It hands the credentials to `setup.sh`
through environment variables and clears its own copies afterwards; it never
writes a secret to disk itself.

### Getting the `media-user-token`

Open `music.apple.com` in a browser where you are signed in, then use either:

- **Application tab** (Chromium): DevTools → Application → Storage → Cookies →
  search `media-user-token`.
- **Network tab**: DevTools → Network → reload → inspect several requests'
  `Cookie:` headers for `media-user-token=`. It is not always in the first
  request — scroll through a few.

## Quick start (command line)

The shell scripts are the same engine the wizard drives, and remain the right
tool for headless installs, servers and CI. Run them from the checkout —
`setup.sh` locates its sibling runtime files relative to itself:

```bash
# In a WSL shell, inside the checkout (e.g. /mnt/e/amd)
bash setup.sh                  # Apple ID, password, media-user-token, storefront
bash login.sh interactive      # enter the 6-digit code in this terminal
bash login.sh status           # expect: listening on 10020/20020/30020
bash dl.sh 'https://music.apple.com/us/album/example/123' alac-192
```

Prefer a PowerShell one-liner? Adapt the mount path to where the checkout lives:

```powershell
wsl.exe -d Ubuntu --exec bash /mnt/e/amd/setup.sh
```

`setup.sh` is interactive by default. For unattended installs it reads
`EZ_USER`, `EZ_PASS`, `EZ_TOKEN` and `EZ_STOREFRONT` from the environment
instead, and never echoes the values.

Existing secret files are never overwritten silently: they are copied to a
timestamped `.bak.*` next to the original before new ones are written.

## Commands

### `login.sh`

```bash
bash login.sh status           # health check: session database + live ports
bash login.sh interactive      # log in (or reuse a cached session) and submit 2FA here
bash login.sh start            # restore a cached session, or open a 2FA attempt for a later submit
bash login.sh submit 123456    # send a code to an attempt started with `start`
```

The long-lived container is named `amd-wrapper-login` and publishes
10020/20020/30020 on loopback only. Its restart policy is `unless-stopped`.
`login.sh` also prepares the wrapper's data directory — the upstream image
entrypoint does that, but `login.sh` starts `/app/wrapper` directly, so a fresh
runtime needs it too.

### `dl.sh`

```bash
bash dl.sh <apple-music-url> [profile] [track-id ...]
```

| Profile | Meaning |
| --- | --- |
| `alac-48` | ALAC capped at 48 kHz |
| `alac-96` | ALAC capped at 96 kHz |
| `alac-192` | ALAC capped at 192 kHz (default) |
| `atmos` | Dolby Atmos when the source provides it |
| `aac` | AAC 256 |
| `alac`, `hires` | Compatibility aliases for `alac-192` |

Pass numeric Apple Music track IDs after the profile to download individual
tracks from an album link; the script adds `--song` and keeps every generated
URL as a separate Docker argument. Files land under `~/amd/downloads`
(`ALAC/`, `Atmos/`, `AAC/`, `MV/`). The downloader's real exit code is
propagated, and upstream token fields are redacted from streamed output.

### Overrides

Every script honours `AMD_BASE` (runtime location, default `~/amd`),
`WRAPPER_IMAGE` and `DOWNLOADER_IMAGE`.

## Layout

The durable state lives in WSL, not in the Windows checkout:

```text
Windows shell / Go TUI
        |
        v
WSL ~/amd/setup.sh   ---- writes .env + config/config.yaml
WSL ~/amd/login.sh   ---- amd-wrapper-login  (loopback 10020/20020/30020)
        |
        +---- dl.sh  ---- one-shot downloader container in the wrapper netns
        |
        +---- downloads/       finished media
        +---- wrapper-data/    persisted Apple session (do not delete)
        +---- config/config.yaml
        +---- .env
```

## The two Windows front ends

Both binaries are committed under `dist/`, built from the sources in this tree
with `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -buildvcs=false`. They
carry no runtime dependency; they call `wsl.exe` under the hood.

**`ez-am-deploy.exe`** — the first-run wizard. It locates `setup.sh` beside
itself or in the parent directory (so it works from `dist/`), collects the Apple
ID, password, storefront and media-user-token, runs `setup.sh` with them as
environment variables, then drives `login.sh start` / `login.sh submit <code>`
for the 2FA step. Credentials are kept in memory only and cleared after the
deploy stage; the wizard shows redacted diagnostics when a step fails.

**`ez-am-downloader.exe`** — the day-to-day front end. It shells out to
`~/amd/dl.sh`, so it inherits every safety rule of the shell engine: it refuses
to run without a healthy wrapper, propagates the real container exit code, and
redacts token fields from the streamed log. For album links it reads the public
Apple Music catalog and lists every track (all selected by default); Up/Down
moves, Space toggles, Left/Right cycles the current track's quality, `A` selects
or clears all, and `G` applies the current quality to compatible selected
tracks. The selection is grouped by quality so each profile goes through one
container.

**Interface language.** The deploy wizard (`ez-am-deploy.exe`) is English; the
downloader TUI (`ez-am-downloader.exe`) is currently Chinese only. Both keep
their text in plain Go string literals, so translating one is a contained
change.

### Rebuilding them

Go must be available somewhere; on Windows-only machines you can cross-compile
from WSL:

```bash
cd deploy
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -buildvcs=false -o ../dist/ez-am-deploy.exe .
cd ../downloader
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -buildvcs=false -o ../dist/ez-am-downloader.exe .
```

Verification used by this tree:

```bash
go vet ./...
go test -count=1 ./...                # in deploy/ and downloader/
bash tests/shell_regression_test.sh   # needs bash + python3 (run it in WSL)
```

## Configuration

`setup.sh` writes a complete `config/config.yaml`. The keys that matter most:

| Key | Purpose |
| --- | --- |
| `media-user-token` | Enables lyrics, artwork, AAC and music videos. |
| `storefront` | Two-letter storefront (`us`, `jp`, `hk`, ...). |
| `decrypt-m3u8-port` | `127.0.0.1:10020` — the wrapper's decrypt port. |
| `exit-on-error` | `true`, so a failed download fails the run instead of hanging. |
| `alac-max` / `atmos-max` / `aac-type` | Quality ceilings handed to the downloader. |
| `*-save-folder` | Output directories inside the container (`/downloads/...`). |

## Troubleshooting

| Symptom | Cause / fix |
| --- | --- |
| `setup.sh not found; checked ...` from the wizard | `ez-am-deploy.exe` is not sitting inside the checkout. It only looks beside itself and one directory up — keep it in `dist/`, or place it next to `setup.sh`. |
| SmartScreen blocks the download or the first run | The binaries are unsigned. Unblock the ZIP before extracting (right-click → Properties → *Unblock*), or choose *More info → Run anyway*. |
| `docker build` stops with `the --mount option requires BuildKit` | The CLI has no buildx plugin. Install it (e.g. the `docker-buildx` package, or the `docker-buildx` binary into `~/.docker/cli-plugins/`). `setup.sh` reuses an existing `local/wrapper:latest`, so the build only matters on the first run or after an image change. |
| `[!] The temporary authentication container exited before requesting 2FA.` | Usually the wrapper could not provision machine data. Check `docker logs amd-wrapper-auth` for `anisetteRequest status: -45061` / `auth error: code=-1703664312`. Confirm `~/amd/wrapper-data/data/com.apple.android.music/files/mpl_db` exists (current `login.sh` creates it automatically). |
| Login asks for a code you did not request, or the code is rejected | The wrapper waits roughly 60 seconds; do **not** restart or re-create the container while it waits, or Apple invalidates the code. Repeated failures can trigger a 15–30 minute Apple cooldown. |
| `[!] Persisted wrapper session is missing or empty` | `~/amd/wrapper-data` is empty or was cleared. Run `bash login.sh interactive` again. |
| `[!] amd-wrapper-login is running but ports ... are not listening` | Look at `docker logs amd-wrapper-login`. Do not remove the container before capturing the log. |
| `fetch failed` from a wrapper-side client | The container takes a moment to open its ports after a WSL restart; `bash login.sh status` is the authoritative check. |
| Everything worked yesterday, nothing works today | `docker ps -a --filter name=amd-`: if the container is missing, restore it with `bash login.sh start`; never let a download start a replacement wrapper. |

## Security notes

- `~/amd/.env` holds the Apple ID and password; `~/amd/config/config.yaml`
  holds the media token. Keep them `0600` with a `0700` config directory —
  `setup.sh` does this for you.
- `.gitignore` excludes `.env`, `config/config.yaml`, `wrapper-data/`,
  `downloads/`, `*.bak.*` and deployment logs. Do not weaken it.
- Never paste a password, media token, session database or 2FA code into an
  issue, a chat or a build log. If one of them has ever been stored in
  plaintext, rotate it.
- `~/amd/wrapper-data` **is** the login state. Deleting it (or the container)
  forces a new Apple authentication, including 2FA, and an empty directory tree
  will break the login until the data skeleton exists again.

## Upstream and licensing

- [zhaarey/apple-music-downloader README-CN](https://github.com/zhaarey/apple-music-downloader/blob/main/README-CN.md)
- [WorldObservationLog/wrapper](https://github.com/WorldObservationLog/wrapper)
- [alacleaker/apple-music-alac-downloader](https://github.com/alacleaker/apple-music-alac-downloader)

This layer is a thin operator wrapper: it does not bundle upstream code, and it
builds the wrapper image from the upstream release archive at install time.
Check the upstream licenses and Apple's terms before redistributing binaries or
media. Add an explicit `LICENSE` file if you intend to publish this repository.
