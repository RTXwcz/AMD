# Unified ez-am Product Design

## Status

Approved for implementation on 2026-07-22.

This document defines the product that replaces the separate deploy and
downloader executables. `D:\amd` is the development workspace only. A released
binary must run from any Windows directory without reading this workspace,
adjacent scripts, or the current working directory.

## Product boundary

The user downloads one Windows executable, `ez-am.exe`. The executable embeds
the application runtime scripts, default configuration, schema version, and
signed asset manifest. It may create persistent application data in the
selected WSL distribution because Docker images, configuration, wrapper session
data, logs, and downloaded media cannot live only inside the executable.

The same Go application is also packaged as a Debian `.deb` and an Arch
`.pkg.tar.zst`. The package formats are native delivery mechanisms for Linux;
they expose the same commands and state machine as the Windows executable.

The first supported architecture is amd64/x86_64. The wrapper release currently
prevents an honest arm64 promise.

## Internal responsibilities

The product has two internal domains, not two programs.

### Control domain

The control domain owns:

- platform and privilege discovery;
- WSL and distribution discovery/installation;
- Docker Desktop or native Docker Engine selection;
- embedded runtime installation and schema migration;
- network, package-manager, and disk preflight;
- Apple credential onboarding, media token collection, and 2FA;
- wrapper lifecycle, health classification, repair, and upgrade;
- bounded, redacted diagnostics and application-owned cache cleanup.

### Media domain

The media domain owns:

- Apple Music URL and public catalog parsing;
- album track and per-track quality selection;
- grouped one-shot download jobs;
- streamed progress, cancellation, and owned-container cleanup;
- output location reporting and media verification hooks.

Every media operation begins with `Control.EnsureReady(DownloadRequest)`. The
media domain never starts a deploy executable and the control domain never
duplicates catalog or track-selection logic.

## Windows startup state machine

1. Inspect Windows, WSL, available distributions, disk space, and resumable
   bootstrap state without making changes.
2. If WSL is unavailable, request UAC and run the official WSL installation
   path with Ubuntu. Persist a non-secret checkpoint before a required restart.
3. If WSL exists but there is no user distribution, install Ubuntu.
   `docker-desktop` and `docker-desktop-data` are never user distributions.
4. If exactly one supported user distribution exists, select it.
5. If more than one user distribution exists, show a selection screen. Never
   guess. Store the explicit choice in application settings.
6. Probe the chosen distribution using its configured default user. Discover
   `id -u`, `id -un`, `$HOME`, `/etc/os-release`, architecture, and WSL version.
   Reject control characters, relative homes, and an unintended `/root` target.
7. Detect Docker Desktop. If it is installed, start it when necessary and wait
   for Docker access from the chosen distribution. If WSL integration is not
   available, report the exact Desktop setting to enable; do not install a
   second daemon silently.
8. If Docker Desktop is not installed, install Docker Engine only inside the
   chosen WSL distribution. Use an APT adapter for Debian-family systems and a
   Pacman adapter for Arch-family systems.
9. Install or upgrade the embedded ez-am runtime under the discovered Linux
   user, then classify wrapper/config/session health.
10. Repair safe failures automatically, request authentication when required,
    and enter the media workflow only after a fresh ready result.

## Distribution and home discovery

WSL commands always carry the selected distribution as a separate argument.
No shell command interpolates a distribution name, user name, home, URL, token,
password, or OTP into shell source.

The runtime probes the selected distribution rather than assuming Ubuntu or a
specific user. Fresh installations use an XDG-compatible base:

```text
$XDG_DATA_HOME/ez-am
or $HOME/.local/share/ez-am
```

Configuration, cache, and state use their respective XDG locations. An existing
valid `$HOME/amd` deployment is detected as a legacy layout and preserved until
the user accepts an in-place migration. Paths displayed through `\\wsl$` or
`\\wsl.localhost` are derived from the selected distribution and probed home.

Supported distribution families are initially Debian/Ubuntu and Arch. An
existing unsupported distribution is reported without modification, and the
user can install Ubuntu through the application.

## Docker backend policy

The chosen backend is persisted as `desktop` or `wsl-engine`; commands never
mix contexts or data roots.

- Installed Docker Desktop is preferred and automatically launched if stopped.
- Docker Desktop is never automatically downloaded or installed.
- When Docker Desktop is absent, Docker Engine is installed inside the selected
  WSL distribution.
- A present but broken Desktop installation is diagnosed. Switching to a WSL
  Engine requires an explicit user decision because two daemons create
  ambiguous images, containers, sockets, and disk usage.
- Native Debian and Arch packages use the host Docker Engine.

Arch installation never performs an unsupported partial upgrade. Any required
`pacman -Syu` is displayed and confirmed before execution. Debian installs only
the exact dependencies required by ez-am; a full `apt upgrade` is a separate,
explicit maintenance action.

## Runtime assets and supply chain

The executable does not locate `setup.sh` beside itself. Compatibility scripts
are embedded with `go:embed`, extracted to a versioned application-owned
directory, verified after writing, and atomically activated.

The wrapper archive and third-party native libraries are not embedded in the
public executable unless their redistribution licenses have been verified.
Until then the executable is the only file the user downloads manually, and it
bootstraps the official wrapper asset at runtime using a content-locked
manifest. A locally observed archive hash is not treated as authenticated
upstream identity without an independent release/asset record.

Every network asset is described by a versioned manifest containing URL,
architecture, size, and SHA-256. Container images are pinned by digest. A mirror
may replace the transport URL but never the expected hash or signature. Partial
downloads use `.part` files and are renamed only after verification.

The wrapper asset must be fixed by content hash before a public release. The
current floating `wrapper.x86_64.latest` URL without verification is a release
blocker.

## Health and repair contract

Health is structured data, not parsed terminal prose. Stable outcomes include:

```text
READY
STARTABLE
NEEDS_AUTH
AUTH_PENDING
NEEDS_DEPLOY
BROKEN_RUNTIME
DOCKER_UNAVAILABLE
UNSUPPORTED_PLATFORM
```

Safe automatic repair is ordered and bounded:

1. start a stopped wrapper with a valid session;
2. recreate a missing wrapper from valid config/session and a verified image;
3. restart a running wrapper with failed ports once;
4. recreate it once if restart fails;
5. pull a missing downloader image by digest;
6. request Apple authentication when the session is absent or invalid.

Wrapper lifecycle mutation is protected by a cross-process lock. Download jobs
may be parallel in a future version, but wrapper repair and authentication are
serialized. A media request receives at most one infrastructure repair/retry
budget so control and media cannot recurse indefinitely.

## Credentials and logs

Passwords and OTPs must not appear in process arguments, inherited environment,
or logs. They are delivered through stdin or a short-lived, mode-0600 channel.
The Apple password is requested for authentication and is not retained for
normal downloading. Media tokens and session state are stored only where the
runtime requires them and with restrictive permissions.

User-facing failures contain a stage, stable code, direct cause, and one next
action. Detailed logs are bounded, rotated, centrally redacted, and available
through `ez-am logs`. No issue-report bundle contains credentials or session
databases.

## Disk and cleanup policy

Disk space is checked before bootstrap, image pulls, package installation, and
downloads. The application estimates temporary and final space separately.

Automatic cleanup is restricted to application-owned WSL cache, `D:\amd` while
developing this instance, failed `.part` files, and containers created by the
current operation. Cleaning other Windows paths, package-manager caches, Docker
global cache, images not owned by ez-am, downloads, configuration, or wrapper
session data requires an explicit list-and-confirm step.

## Delivery milestones

1. Unified Windows executable for an existing supported WSL distribution and
   working Docker backend. It embeds runtime assets, discovers the real home,
   selects among multiple distributions, performs doctor/repair, and preserves
   the verified media workflow.
2. Windows bootstrap for missing WSL/distribution, Docker Desktop auto-start,
   and WSL-native Docker Engine installation with restart resume.
3. Secure onboarding, migration, update, logging, and bounded cleanup.
4. Native Debian and Arch adapters plus signed `.deb` and `.pkg.tar.zst`
   artifacts.
5. Clean-VM, upgrade, corruption, weak-network, low-disk, cancellation, and
   redaction release qualification.

Each milestone must leave working, testable software and retain the previous
downloader's behavior until the unified executable passes the same regression
and real-media checks.
