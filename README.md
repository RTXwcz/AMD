# ez-am-downloader

This repository is a small Windows/WSL2 operator layer around two upstream
projects:

- [zhaarey/apple-music-downloader](https://github.com/zhaarey/apple-music-downloader)
- [WorldObservationLog/wrapper](https://github.com/WorldObservationLog/wrapper)

It is intended for personal backups of content that the operator is entitled
to access. Apple Music subscription requirements, DRM restrictions, upstream
availability, and local law still apply. This project does not bypass Apple
account security.

## Architecture

The durable state lives in WSL, not in the Windows checkout:

```text
Windows TUI / shell
        |
        v
WSL ~/amd/login.sh ---- amd-wrapper-login (ports 10020/20020/30020)
        |
        +---- dl.sh ---- one-shot downloader container
        |
        +---- downloads/
        +---- wrapper-data/       persisted Apple session
        +---- config/config.yaml
        +---- .env                Apple ID credentials
```

`dl.sh` never creates an unlogged wrapper. It checks `login.sh status` first,
then runs the pinned downloader image in the wrapper's network namespace.
The published wrapper ports bind to loopback only.

## Prerequisites

1. Windows 10 or 11 with WSL2 and an Ubuntu (or compatible) distro.
2. Docker Desktop running with WSL Integration enabled for that distro.
3. An active Apple Music subscription.
4. Network access to GitHub and `ghcr.io` during installation.
5. A `media-user-token` copied from an authenticated `music.apple.com` session.

The wrapper login may require Apple 2FA. Keep the trusted device ready before
starting an authentication attempt.

## Fresh installation

Run the setup script from the checkout so it can find the sibling runtime
files:

```powershell
wsl.exe -d Ubuntu --exec bash /mnt/d/amd/setup.sh
```

`setup.sh` checks Docker Desktop, pulls the pinned downloader image, builds or
reuses the local wrapper image, and writes `~/amd/.env` and
`~/amd/config/config.yaml`. Existing secret files are timestamped before they
are replaced. It does not install a second Docker daemon or upgrade the WSL
distro.

For an existing deployment where configuration and session data must be
preserved, do not rerun `setup.sh` merely to update scripts. Use the guarded
synchronizer from an elevated WSL shell:

```powershell
wsl.exe -d Ubuntu -u root --exec bash /mnt/d/amd/sync_existing_wsl.sh
```

It atomically installs the four runtime files (`setup.sh`, `dl.sh`, `login.sh`,
and `compose.yaml`), changes only `exit-on-error`, tightens secret/session
permissions, and refuses unexpected source or destination paths. It does not
remove containers, media, `.env`, or `wrapper-data`.

The Windows binaries are optional convenience front ends. They look for
`setup.sh` beside the executable or in its trusted parent directory; they do
not execute a `setup.sh` found in the current working directory.

## Login and recovery

Run authentication in a visible WSL terminal so the 2FA code never passes
through a chat or log:

```bash
bash ~/amd/login.sh interactive
```

The temporary authentication container waits for roughly 60 seconds after
Apple requests a code. Enter the six-digit code immediately in that terminal.
Do not restart the container while it is waiting. The code is written through
`docker exec` and is not embedded in shell source.

Useful lifecycle commands:

```bash
bash ~/amd/login.sh status       # healthy session and all three ports
bash ~/amd/login.sh start       # restore a cached session or start a 2FA attempt
bash ~/amd/login.sh submit 123456
```

`start` leaves an active authentication request for a later `submit`. The
long-lived container is named `amd-wrapper-login`; old diagnostic containers
are not removed automatically.

## Download

Use either the downloader TUI (`dist/ez-am-downloader.exe`) or the shell
engine:

```bash
bash ~/amd/dl.sh 'https://music.apple.com/us/album/example/123' alac-48
```

For album links, the Windows TUI reads the public Apple Music catalog and
shows every track. All tracks are selected by default. Use Up/Down to move,
Space to select, Left/Right to cycle the current track's available quality,
`A` to select or clear all, and `G` to apply the current quality to compatible
selected tracks. Dolby Atmos is offered only when the track advertises it.
Press Enter to download; selected tracks are grouped by quality so one
container handles each profile.

Supported shell profiles are:

| Argument | Meaning |
| --- | --- |
| `alac-48` | ALAC capped at 48 kHz |
| `alac-96` | ALAC capped at 96 kHz |
| `alac-192` | ALAC capped at 192 kHz |
| `atmos` | Dolby Atmos when the source provides it |
| `aac` | AAC 256 |
| `alac`, `hires` | Compatibility aliases for `alac-192` |

To download selected album tracks, pass their numeric Apple Music IDs as
separate arguments. The script adds `--song` and keeps each generated URL as a
separate Docker argument:

```bash
bash ~/amd/dl.sh 'https://music.apple.com/us/album/example/123?l=en-US' \
  alac-48 456 789
```

The downloader returns the real container exit code. Invalid URLs and missing
wrapper sessions fail before a download container is started. Each download
gets a validated container name and signal cleanup. `Ctrl+C` in the Windows
downloader cancels the WSL child, stops that named container, and exits with
code 130 only after both cleanup paths finish. Stable stage markers are
streamed to the TUI, and upstream media/authorization token fields are
redacted before output is displayed or logged.

Files are written below `~/amd/downloads` (for example `ALAC`, `Atmos`, and
`AAC`). Existing media is never removed by setup or login.

## Secret and state boundaries

- `~/amd/.env` contains the Apple ID and password.
- `~/amd/config/config.yaml` contains the media token and downloader settings.
- `~/amd/wrapper-data` contains the persisted wrapper session database.
- Keep `.env` and `config.yaml` mode `0600`; keep their parent config directory
  mode `0700`.
- Never paste an Apple password, media token, session database, or 2FA code
  into an issue, chat, or build log.
- Historical deployment notes may contain credentials. Review and redact them
  before sharing or committing. Rotate the Apple password and media token if
  they have ever been recorded in plaintext.

The repository ignores local backups and deployment logs, but ignore rules do
not remove files that were already committed. Check repository history before
publishing this directory.

## Build from source

Go builds should disable VCS stamping because this deployment directory may be
distributed without a valid Git worktree:

```bash
cd deploy
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -buildvcs=false -o ../dist/ez-am-deploy.exe .
cd ../downloader
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -buildvcs=false -o ../dist/ez-am-downloader.exe .
```

Verification used by this tree:

```bash
go test -count=1 ./...
go vet ./...
```

## Troubleshooting

`login.sh status` is the health check. If it fails, inspect the named
`amd-wrapper-login` container and run `bash ~/amd/login.sh interactive`; do not
let a download script silently create a replacement wrapper. If Docker is not
available, start Docker Desktop and enable WSL Integration rather than
installing another daemon inside Ubuntu.

The `compose.yaml` file is a reference for mounts, loopback ports, and the
canonical container name. `login.sh` owns the authentication lifecycle and is
the supported entry point for starting the wrapper.

## Upstream references

- [zhaarey README-CN](https://github.com/zhaarey/apple-music-downloader/blob/main/README-CN.md)
- [WorldObservationLog/wrapper](https://github.com/WorldObservationLog/wrapper)
- [alacleaker/apple-music-alac-downloader](https://github.com/alacleaker/apple-music-alac-downloader)
- [Deployment notes](./DEPLOY_LOG.md) (private, do not publish without review)

This operator layer is released under the repository's chosen license. Verify
the upstream licenses and Apple terms before distributing binaries or media.
