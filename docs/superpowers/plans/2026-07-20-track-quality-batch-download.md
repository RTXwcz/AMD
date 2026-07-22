# Track Quality Batch Download Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend `ez-am-downloader.exe` so album URLs expose track selection, per-track quality choices, real streamed download stages, and safe grouped downloads through the existing WSL wrapper.

**Architecture:** The Windows TUI obtains public Apple Music catalog metadata (album/song IDs, titles, audio traits), lets the user select tracks and a profile per selected track, groups requests by profile, and invokes `dl.sh` once per group. `dl.sh` maps validated profiles to the upstream downloader's real flags and supports multiple `i=<track-id>` song targets. Child stdout/stderr is streamed into the Bubble Tea model and normalized into stage labels; cancellation keeps the existing named-container cleanup contract.

**Tech Stack:** Go, Bubble Tea, `net/http` and `encoding/json`, Bash, WSL2/Docker, upstream `--song`, `--alac-max`, `--atmos`, `--aac`, `--debug` flags.

---

### Task 1: Catalog URL and metadata model

**Files:**
- Create: `downloader/catalog.go`
- Test: `downloader/catalog_test.go`

- [ ] Write failing tests for parsing album URLs, preserving storefront/language/query parameters, rejecting non-Apple URLs, decoding track IDs/names/traits, and grouping available profiles.
- [ ] Run `go test ./...` in `downloader`; confirm the new tests fail because the parser/client types are absent.
- [ ] Implement a small HTTP client that obtains Apple's public storefront token, fetches album JSON including tracks and pagination, and returns typed metadata without touching media credentials or writing files.
- [ ] Run the focused tests, then `go test ./...`; confirm they pass.

### Task 2: Validated grouped Shell download interface

**Files:**
- Modify: `dl.sh`
- Modify: `downloader/main_test.go`
- Modify: `tests/shell_regression_test.sh`

- [ ] Add failing tests for profiles `alac-48`, `alac-96`, `alac-192`, `atmos`, and `aac`; reject malformed IDs/profiles; keep URL and IDs as separate arguments; verify stage markers.
- [ ] Run the focused Go and shell tests and observe the expected failures.
- [ ] Extend `dl.sh` to accept `<url> <profile> [track-id ...]`, validate numeric track IDs, append `i=` and `--song` for targeted tracks, map profiles to real upstream flags, and emit `[stage]` markers before validation, wrapper readiness, container start, and completion.
- [ ] Run shell regression and Go tests; confirm all pass without changing existing cleanup behavior.

### Task 3: TUI track selection and per-track profiles

**Files:**
- Modify: `downloader/main.go`
- Modify: `downloader/main_test.go`

- [ ] Add failing model tests for metadata completion, default-all selection, space/a toggles, cycling profiles, rejecting an Atmos profile when a track lacks `atmos`, grouping selected tracks by profile, and preserving single-song fallback.
- [ ] Run focused tests and verify they fail for missing states/commands.
- [ ] Add album metadata and track-selection states. Keep all tracks selected initially; support selection toggles and profile cycling; expose only profiles supported by each track's audio traits, with a clear unavailable state for the inspected target album.
- [ ] Build grouped jobs that invoke `dl.sh` once per profile with multiple track IDs; preserve the existing format screen for non-album URLs.
- [ ] Run focused model tests and the full downloader test suite.

### Task 4: Streamed stages, cancellation, and display

**Files:**
- Modify: `downloader/main.go`
- Modify: `downloader/main_test.go`

- [ ] Add failing tests for line-by-line process events, stage normalization (`metadata`, `container`, `download`, `decrypt/tag`, `complete`), grouped-job advancement, failure diagnostics, and Ctrl+C cleanup while a stream is active.
- [ ] Run focused tests and verify the expected failures.
- [ ] Replace `CombinedOutput` for downloads with a pipe/scanner event stream, preserve bounded recent logs, advance through grouped jobs, and retain named-container cancellation/cleanup synchronization.
- [ ] Update views to show selected count, profile per track, current group, current stage, latest output, and actionable failure text.
- [ ] Run `go test ./...`, `go test -race ./...`, and `go vet ./...`.

### Task 5: Build, Windows UI debug, and real target download

**Files:**
- Modify: `README.md` only if usage text is stale
- Build: `dist/ez-am-downloader.exe`

- [ ] Cross-build the Windows binary with `-buildvcs=false` and verify its SHA-256.
- [ ] Start the binary with Windows Computer Use, paste the target album URL, inspect the rendered 3-track list, verify Atmos is unavailable and ALAC 24/48 is available, and exercise selection/profile controls without starting a download.
- [ ] Run the real target download using the selected three tracks at ALAC source/24/48 as appropriate, monitoring C:, D:, WSL, Docker container status, and output files.
- [ ] Verify downloaded files with `ffprobe` and compare output count/tags against the selected tracks; retain logs and report any upstream limitations.
