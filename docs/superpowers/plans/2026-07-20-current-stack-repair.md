# Current Stack Repair Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restore the existing zhaarey downloader plus WorldObservationLog wrapper deployment to a repeatable, diagnosable, and safe-to-restart working state without replacing or deleting existing downloads and wrapper session data.

**Architecture:** Shell scripts under `D:/amd` are the single source of truth and are copied into `$HOME/amd` in WSL. The Windows Go programs only collect input and invoke those scripts through an explicit `wsl.exe -d <distro> --exec` boundary. Wrapper session state remains in `$HOME/amd/wrapper-data`; download jobs remain one-off containers sharing the wrapper network namespace.

**Tech Stack:** Bash, Docker Desktop/WSL2, Docker CLI, Go 1.22, Bubble Tea, PowerShell verification.

---

### Task 1: Establish regression tests and data guards

**Files:**
- Create: `D:/amd/tests/shell_regression_test.sh`
- Create: `D:/amd/deploy/main_test.go`
- Create: `D:/amd/downloader/main_test.go`

- [ ] **Step 1: Write shell contract tests**

Test that `setup.sh` installs the canonical `dl.sh` and `login.sh`, generated configuration enables error propagation, no download path creates a login container implicitly, scripts accept an overrideable base directory, and secrets are written with mode `0600`.

- [ ] **Step 2: Verify shell tests fail against the current installer**

Run:

```powershell
wsl.exe -d Ubuntu --exec bash /mnt/d/amd/tests/shell_regression_test.sh
```

Expected: non-zero with failures for the embedded stale `dl.sh`, missing `login.sh` installation, and `exit-on-error: false`.

- [ ] **Step 3: Write Go command-boundary tests**

Tests call a pure command builder and require this argument shape:

```text
wsl.exe -d Ubuntu --exec bash -lc <fixed-script> ez-am-downloader <user-url> <format>
```

User-controlled values must be separate arguments, never interpolated into shell source. Deployment tests also require token collection before deployment begins.

- [ ] **Step 4: Verify Go tests fail for the missing command builder and current state order**

Run:

```powershell
go test ./...
```

from both `D:/amd/deploy` and `D:/amd/downloader`. Expected: compile/test failure caused by the missing safe WSL boundary.

### Task 2: Make runtime scripts the single source of truth

**Files:**
- Modify: `D:/amd/setup.sh`
- Modify: `D:/amd/dl.sh`
- Modify: `D:/amd/login.sh`
- Create: `D:/amd/compose.yaml`

- [ ] **Step 1: Refactor setup without altering user data**

Set `umask 077`, derive `SCRIPT_DIR`, preserve `downloads` and `wrapper-data`, back up an existing `.env` and `config/config.yaml` before replacement, and install sibling runtime scripts instead of embedding copies. On WSL, missing Docker must fail with the Docker Desktop WSL Integration instruction rather than installing a competing daemon.

- [ ] **Step 2: Separate deployment from Apple 2FA login**

`setup.sh` writes configuration and images only. `login.sh start`, `login.sh submit <code>`, `login.sh status`, and the default interactive mode own the complete wrapper lifecycle. A six-digit code is sent through stdin to `docker exec`, not interpolated into shell code.

- [ ] **Step 3: Make wrapper readiness observable**

Require a running container and listening port `10020` before starting a downloader. A missing session returns a non-zero status and a direct login instruction. Never create an unparameterized wrapper container as a download fallback.

- [ ] **Step 4: Make format and failure behavior honest**

Expose `alac`, `atmos`, and `aac`; retain `hires` only as a backward-compatible alias for `alac`. Generate `exit-on-error: true`, use argument arrays, and return the downloader container's real exit status.

- [ ] **Step 5: Add a consistent compose reference**

Use container name `amd-wrapper-login`, mount `./wrapper-data`, bind diagnostic ports to `127.0.0.1`, and mount `./config/config.yaml`. Do not pass Apple credentials in the long-lived service definition.

- [ ] **Step 6: Run shell tests until green**

Run the command from Task 1. Expected: every assertion reports `PASS` and the test exits zero.

### Task 3: Repair the Windows controllers

**Files:**
- Modify: `D:/amd/deploy/main.go`
- Modify: `D:/amd/downloader/main.go`
- Modify: `D:/amd/deploy/main_test.go`
- Modify: `D:/amd/downloader/main_test.go`

- [ ] **Step 1: Implement explicit WSL command builders**

Default to distro `Ubuntu`, allow `EZ_WSL_DISTRO` override, call `wsl.exe` directly on Windows, and keep URL, format, 2FA code, and paths as distinct process arguments.

- [ ] **Step 2: Correct deployment state order**

Collect Apple ID, password, storefront, and media token before invoking setup. Run setup non-interactively, start wrapper login, show 2FA only when requested, submit the code, and display success only after `login.sh status` succeeds.

- [ ] **Step 3: Correct downloader presentation**

Remove the false Plain/Hi-Res distinction, show actual child output on completion, and retain the child's non-zero status as a failed result.

- [ ] **Step 4: Run Go tests and builds**

Run `go test ./...` and `go build` in both modules. Expected: zero failures and two Windows executables produced under `D:/amd/dist`.

### Task 4: Synchronize and recover the current WSL deployment

**Files:**
- Update from canonical sources: `/home/rtxwcz/amd/setup.sh`
- Update from canonical sources: `/home/rtxwcz/amd/dl.sh`
- Update from canonical sources: `/home/rtxwcz/amd/login.sh`
- Update from canonical sources: `/home/rtxwcz/amd/compose.yaml`
- Preserve: `/home/rtxwcz/amd/downloads`
- Preserve: `/home/rtxwcz/amd/wrapper-data`

- [ ] **Step 1: Record checksums and make a timestamped script/config backup**

Do not include `.env`, media token, or password contents in logs. Do not copy media or wrapper session data.

- [ ] **Step 2: Enable Docker Desktop integration for Ubuntu**

After integration, verify `docker version`, `docker compose version`, and daemon connectivity from the `rtxwcz` user.

- [ ] **Step 3: Synchronize scripts and tighten permissions**

Use modes `0700` for secret/session directories, `0600` for `.env` and `config.yaml`, and `0755` for executable scripts.

- [ ] **Step 4: Restore wrapper without destroying session state**

If a cached session exists, start and verify it. If no session exists, stop at the interactive 2FA prompt and have the user enter the current code directly; never delete `wrapper-data`.

### Task 5: End-to-end verification

**Files:**
- Modify: `D:/amd/README.md`

- [ ] **Step 1: Run static and unit verification**

Run shell syntax checks, shell regression tests, Go tests, `go vet`, and Windows cross-builds.

- [ ] **Step 2: Run runtime health verification**

Verify wrapper container state, ports `10020/20020/30020`, persisted session restoration, config readability inside the one-off downloader container, and output directory writability.

- [ ] **Step 3: Run safe failure-path verification**

Use an invalid Apple Music URL or downloader help invocation to prove errors return non-zero and the Windows controller does not display success. Do not download media solely for testing.

- [ ] **Step 4: Update operator documentation**

Document Docker Desktop preflight, one-time login, status/recovery commands, truthful formats, credential locations, backup boundaries, and known upstream licensing/DRM risks.

- [ ] **Step 5: Review the final diff and report residual blockers**

Confirm no secret values were introduced into `D:/amd`, no existing media/session paths were changed, and clearly identify any verification that still requires user 2FA or an entitled test URL.
