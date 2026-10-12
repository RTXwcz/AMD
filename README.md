# ez-am-downloader

One Windows executable, one flow: **run it, it checks your machine, fixes what
it can, then downloads.**

`ez-am.exe` is a small operator layer around two upstream projects:

- [zhaarey/apple-music-downloader](https://github.com/zhaarey/apple-music-downloader) — fetches Apple Music tracks
- [WorldObservationLog/wrapper](https://github.com/WorldObservationLog/wrapper) — decrypts them (needs an active Apple Music subscription)

It exists to make the boring parts repeatable: one entry point, one deployment
engine, one wrapper lifecycle owner, and no secret ever written into a script or
a log.

Intended for personal backups of content you are entitled to access. Apple Music
subscription terms, DRM restrictions, upstream availability and local law still
apply. This project does not bypass Apple account security.

## What's in the box

| Path | Role |
| --- | --- |
| `dist/ez-am.exe` | The unified Windows front end. Doctor, guided fixes, downloader. |
| `cmd/ez-am` | Its entry point: subcommand routing and the `doctor` CLI. |
| `internal/doctor` | Read-only diagnosis: WSL, distribution, Docker, BuildKit, runtime files, credentials, images, wrapper session, output directory. |
| `internal/control` | The supervisor TUI: runs the doctor, offers the fixes it can perform, re-checks, then hands over to the media engine. |
| `internal/media` | Apple Music URL/catalog parsing, track and quality selection, grouped download jobs, cancellation. |
| `internal/i18n` | The English and Chinese message catalog, locale detection and the saved choice. |
| `internal/platform`, `internal/runtime` | WSL discovery and probing, embedded runtime assets and their install plan. |
| `setup.sh`, `login.sh`, `dl.sh`, `compose.yaml` | The shell engine the app drives; also usable directly. |
| `tests/` | Shell regression suite and media verification helpers. |

## Requirements

1. Windows 10/11 with WSL2 and an Ubuntu-compatible distribution.
2. Docker reachable **from inside that distro** — a Docker daemon installed in
   the distro, or Docker Desktop with WSL integration. Docker Desktop is not
   required.
3. Docker BuildKit (`docker buildx`) — needed once, to build the wrapper image.
   The app reports it as a warning and tells you how to install it.
4. An active Apple Music subscription and a `media-user-token`.
5. Network access to `github.com` and `ghcr.io` during installation.

**On a completely fresh machine the app cannot do everything for you.** It
detects a missing WSL, distribution, Docker or BuildKit, and for each of those
it prints the exact command to run and offers a re-check action — but installing
WSL2 needs administrator rights and a reboot, so that step stays in your hands:

```powershell
wsl --install -d Ubuntu                 # elevated PowerShell, then reboot
wsl -d Ubuntu --exec sudo apt-get install -y docker.io
                                        # or install Docker Desktop with WSL integration
```

Everything after that is handled inside the app: runtime files, the Apple
account and media token, the image build, 2FA, and the wrapper session.

## Quick start (Windows)

1. **Get the checkout.** Download the ZIP (green *Code* button) or
   `git clone https://github.com/RTXwcz/AMD`, then unpack it somewhere permanent.
2. **Double-click `dist\ez-am.exe`.** It starts with the doctor:

   | Check | What it looks at |
   | --- | --- |
   | WSL / Distribution | `wsl.exe`, and which distributions you can use |
   | Docker / BuildKit | whether the daemon answers inside the distro |
   | Runtime files | `dl.sh`, `login.sh`, `compose.yaml` under the runtime base |
   | Credentials | `~/amd/.env` + `config/config.yaml` (existence only — never their contents) |
   | Images | the pinned downloader image and `local/wrapper:latest` |
   | Wrapper session | `login.sh status`, i.e. the three published ports |
   | Output directory | whether media can be written |

   Green rows are fine. Red rows come with actions: install the bundled runtime,
   enter the Apple account and media token, log in (2FA), pick a distribution, or
   instructions to start Docker / install buildx. The app re-checks after each
   fix and starts the downloader as soon as everything is green.
3. **Paste an Apple Music link** in the downloader, pick tracks and quality with
   the keyboard, press Enter. Finished media lands in
   `\\wsl.localhost\Ubuntu\home\<you>\amd\downloads`.

Windows SmartScreen may warn about an unsigned binary the first time: choose
*More info → Run anyway*.

### Getting the `media-user-token`

While signed in on `music.apple.com`, use either:

- **Application tab** (Chromium): DevTools → Application → Storage → Cookies →
  search `media-user-token`.
- **Network tab**: DevTools → Network → reload → inspect several requests'
  `Cookie:` headers for `media-user-token=`. It is not always in the first
  request — scroll through a few.

## Command line

The same executable doubles as a CLI, which is also what CI and headless setups
should use:

```bash
ez-am                  # doctor, guided fixes, then the downloader
ez-am doctor           # diagnosis only: exit 0 when ready, 2 when it is not
ez-am doctor --json    # machine-readable report
ez-am download         # skip the checks and go straight to the downloader

ez-am --distro Ubuntu --base /home/you/amd    # explicit target
ez-am --lang en                               # English interface
```

## Language

The interface ships in English and Simplified Chinese and follows the system
locale by default (a zh-CN Windows starts in Chinese). Switch it three ways:

| How | Scope |
| --- | --- |
| `--lang zh` / `--lang en` | this run, and remembered for the next one |
| `EZ_AM_LANG` environment variable | this run |
| `L` key in the app | immediately, in every screen except the text fields |

The saved choice lives in `%AppData%\ez-am\settings.json` (0600); the flag wins
over the environment, which wins over the saved value, which wins over the
system locale. The choice made in the app carries into the downloader, so the
whole session stays in one language.

The shell engine remains available on its own:

```bash
bash setup.sh                  # interactive: Apple ID, password, media token, storefront
bash login.sh interactive      # enter the 6-digit code in this terminal
bash login.sh status           # expect: listening on 10020/20020/30020
bash dl.sh 'https://music.apple.com/us/album/example/123' alac-192
```

`setup.sh` also accepts `EZ_USER`, `EZ_PASS`, `EZ_TOKEN` and `EZ_STOREFRONT`
for unattended installs, and never echoes the values. Existing secret files are
timestamped (`.bak.*`) before they are replaced.

## Download profiles

| Argument | Meaning |
| --- | --- |
| `alac-48` / `alac-96` / `alac-192` | ALAC capped at 48 / 96 / 192 kHz |
| `atmos` | Dolby Atmos when the source provides it |
| `aac` | AAC 256 |
| `alac`, `hires` | Aliases for `alac-192` |

Pass numeric Apple Music track IDs after the profile to download individual
tracks from an album link. The downloader's real exit code is propagated and
upstream token fields are redacted from streamed output.

## Layout

```text
Windows: ez-am.exe  ──►  wsl.exe
                          │
                          ├── doctor + install + credentials + 2FA (control)
                          └── dl.sh ── one-shot downloader container
                                        (shares the wrapper network namespace)
             WSL state (default ~/amd, XDG when fresh):
               .env, config/config.yaml, wrapper-data/, downloads/
```

Runtime paths are resolved from the selected distribution and the real home
directory; an existing `~/amd` deployment is detected and kept.

## Troubleshooting

| Symptom | Cause / fix |
| --- | --- |
| `ez-am doctor` prints `NEEDS_RUNTIME` | Runtime files are missing under the reported base. Accept the offered install, or run `setup.sh` from the checkout. |
| `NEEDS_AUTH` / wrapper not listening | No valid Apple session. Use the login action (2FA) — the code must be typed within ~60 seconds and the container must not be restarted while it waits. |
| `DOCKER_UNAVAILABLE` | Start Docker Desktop, or `sudo service docker start` inside the distro, then re-run the doctor. |
| BuildKit warning, image build fails with `the --mount option requires BuildKit` | Install the `docker buildx` plugin (package or binary in `~/.docker/cli-plugins/`). Only needed once. |
| `auth error: code=-1703664312` right after starting a login | The wrapper could not provision machine data. Check that `<base>/data/com.apple.android.music/files` exists; `login.sh` creates it automatically. |
| Docker is available in WSL but the app says otherwise | The daemon must answer **inside** the distro: `wsl -d <distro> --exec docker info`. |
| Nothing works after a WSL restart | The wrapper takes a moment to publish its ports; re-run the doctor. Do not delete `wrapper-data`. |

## Security notes

- `~/amd/.env` holds the Apple ID and password; `config/config.yaml` holds the
  media token. Both stay `0600` inside a `0700` config directory.
- The doctor never reads credential contents — only their existence.
- Passwords and codes are never passed as shell source: they travel as separate
  arguments, environment entries or stdin, depending on the step.
- Never paste a password, media token, session database or 2FA code into an
  issue, a chat or a build log. Rotate anything that has been stored in
  plaintext.
- `~/amd/wrapper-data` **is** the login state. Deleting it (or the container)
  forces a new Apple authentication.

## Build and verify

```bash
# Linux/WSL host, cross-compiling the single Windows binary
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -buildvcs=false -trimpath -o dist/ez-am.exe ./cmd/ez-am

go vet ./...
go test -count=1 ./...
bash tests/shell_regression_test.sh   # needs bash + python3 (run it in WSL)
```

The committed `dist/ez-am.exe` is built from this tree with those flags.

## Upstream and licensing

- [zhaarey/apple-music-downloader README-CN](https://github.com/zhaarey/apple-music-downloader/blob/main/README-CN.md)
- [WorldObservationLog/wrapper](https://github.com/WorldObservationLog/wrapper)
- [alacleaker/apple-music-alac-downloader](https://github.com/alacleaker/apple-music-alac-downloader)

This layer is a thin operator wrapper: it does not bundle upstream code, and it
builds the wrapper image from the upstream release archive at install time.
Check the upstream licenses and Apple's terms before redistributing binaries or
media. Add an explicit `LICENSE` file if you intend to publish this repository.
