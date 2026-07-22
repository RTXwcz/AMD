# Unified ez-am Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Produce the first unified `ez-am.exe` that runs without adjacent files or `D:\amd`, discovers and selects a supported WSL distribution and its real user home, installs embedded runtime assets, classifies Docker/runtime health, and enters the existing album/quality/download workflow.

**Architecture:** Evolve the tested `downloader` Go module into the single application while keeping the old deploy module as a non-runtime migration reference. Add platform, runtime, and control packages with injected command runners; wrap the existing media TUI behind a supervisor state machine. Embed compatibility scripts inside the Go module, install them into the selected user's XDG data directory, and pass the selected distro/base explicitly to every WSL command.

**Tech Stack:** Go 1.22+, Bubble Tea, `embed`, `os/exec`, Bash compatibility assets, WSL2, Docker, existing downloader catalog/selection/streaming code.

---

### Task 1: WSL output decoding and distribution inventory

**Files:**
- Create: `downloader/platform_types.go`
- Create: `downloader/wsl_discovery.go`
- Create: `downloader/wsl_discovery_test.go`

- [ ] **Step 1: Write failing parser and policy tests**

Define tests for UTF-16LE and UTF-8 `wsl --list --quiet` output, CR/NUL removal,
exclusion of `docker-desktop` variants, zero/one/multiple user distributions,
and stable de-duplication. The desired public surface is:

```go
type Distribution struct {
    Name       string
    Family     string
    Version    int
    User       string
    Home       string
    Docker     DockerState
    LegacyBase bool
}

func DecodeConsoleOutput(raw []byte) string
func ParseDistributionNames(raw []byte) []string
func SelectableDistributions(items []Distribution) []Distribution
```

- [ ] **Step 2: Run the focused test and verify RED**

Run:

```bash
cd /mnt/d/amd/downloader
go test ./... -run 'TestDecodeConsoleOutput|TestParseDistributionNames|TestSelectableDistributions'
```

Expected: build failure because the types/functions do not exist.

- [ ] **Step 3: Implement the minimal decoder and inventory policy**

Decode BOM-marked or NUL-heavy UTF-16LE without depending on localized labels.
Reject empty/control-character names. Preserve discovery order and remove only
the exact system distributions.

- [ ] **Step 4: Run focused and full tests**

Expected: focused tests and `go test ./...` pass.

### Task 2: Safe distribution probing and dynamic home discovery

**Files:**
- Modify: `downloader/platform_types.go`
- Modify: `downloader/wsl_discovery.go`
- Modify: `downloader/wsl_discovery_test.go`

- [ ] **Step 1: Write failing runner-contract tests**

Use an injected runner:

```go
type CommandRequest struct {
    Name  string
    Args  []string
    Env   []string
    Stdin []byte
}

type CommandResult struct {
    Stdout []byte
    Stderr []byte
    Code   int
}

type CommandRunner interface {
    Run(context.Context, CommandRequest) (CommandResult, error)
}
```

Assert that distro names remain separate `wsl.exe -d <name>` arguments; the
fixed probe script returns a record delimiter followed by `id`, user, home,
architecture, and os-release fields. Cover Debian, Ubuntu, Arch, unsupported
families, `/root`, relative homes, newlines, and missing fields.

- [ ] **Step 2: Verify RED**

Run the focused tests and confirm failure for the missing probe API.

- [ ] **Step 3: Implement probe validation**

Add:

```go
func ProbeDistribution(ctx context.Context, run CommandRunner, name string) (Distribution, error)
func ClassifyFamily(id string, idLike []string) string
func ValidateLinuxHome(home string, uid int) error
```

Do not parse localized WSL status prose. Use Linux-generated machine fields and
reject an unintended root default user with an actionable typed error.

- [ ] **Step 4: Verify GREEN and refactor**

Run focused tests, full Go tests, race tests, and `go vet`.

### Task 3: Embedded runtime manifest and atomic installer

**Files:**
- Create: `downloader/runtime_assets.go`
- Create: `downloader/runtime_assets_test.go`
- Create: `downloader/runtime/setup.sh`
- Create: `downloader/runtime/login.sh`
- Create: `downloader/runtime/dl.sh`
- Create: `downloader/runtime/compose.yaml`

- [ ] **Step 1: Write failing embed and install-plan tests**

Tests require all four assets, deterministic SHA-256 values, no adjacent-file
lookup, an XDG default base, legacy `$HOME/amd` detection, and atomic writes via
stdin. Desired API:

```go
type RuntimeLayout struct {
    Base, Config, Data, Cache, State, Downloads string
}

func ResolveRuntimeLayout(home string, xdg XDGDirs, legacyValid bool) RuntimeLayout
func EmbeddedRuntime() ([]RuntimeAsset, error)
func BuildInstallRequests(distro string, layout RuntimeLayout, assets []RuntimeAsset) ([]CommandRequest, error)
```

- [ ] **Step 2: Verify RED**

Run only runtime asset tests and confirm missing symbols/assets fail.

- [ ] **Step 3: Add embedded compatibility assets**

Copy the currently verified shell runtime into the module, embed it, and build
fixed-shell installation requests that create versioned files, verify hashes,
set modes, then atomically activate them. User paths are arguments, never shell
interpolation.

- [ ] **Step 4: Verify GREEN**

Run asset tests, all downloader tests, and the shell regression suite against
the embedded canonical copies.

### Task 4: Structured doctor and Docker backend classification

**Files:**
- Create: `downloader/doctor.go`
- Create: `downloader/doctor_test.go`
- Modify: `downloader/platform_types.go`

- [ ] **Step 1: Write failing health matrix tests**

Cover no WSL, no user distro, one distro, multiple distros, unsupported distro,
Docker Desktop ready/stopped/integration missing, Desktop absent with/without
WSL Docker, runtime absent, wrapper startable, needs auth, and ready. Define:

```go
type HealthCode string
const (
    HealthReady HealthCode = "READY"
    HealthNeedsDistro HealthCode = "NEEDS_DISTRO"
    HealthNeedsSelection HealthCode = "NEEDS_SELECTION"
    HealthDockerUnavailable HealthCode = "DOCKER_UNAVAILABLE"
    HealthNeedsDeploy HealthCode = "NEEDS_DEPLOY"
    HealthNeedsAuth HealthCode = "NEEDS_AUTH"
)

type HealthReport struct {
    Code HealthCode
    Stage string
    Summary string
    Action string
    Distribution *Distribution
}
```

- [ ] **Step 2: Verify RED**

Run focused doctor tests and confirm the missing classifier fails.

- [ ] **Step 3: Implement read-only doctor**

Doctor performs no install, start, repair, login, or cleanup. It consumes typed
probe results and returns one stable report with bounded redacted diagnostics.

- [ ] **Step 4: Verify GREEN**

Run focused, full, race, and vet checks.

### Task 5: Supervisor TUI and explicit multi-distro selection

**Files:**
- Create: `downloader/supervisor.go`
- Create: `downloader/supervisor_test.go`
- Modify: `downloader/main.go`

- [ ] **Step 1: Write failing state-transition tests**

Cover startup doctor, automatic use of exactly one distro, selection UI for two
or more distros, persisted explicit choice, unsupported/root-user failures,
runtime-install consent, and transition into the unchanged media model.

- [ ] **Step 2: Verify RED**

Run focused model tests and confirm missing supervisor states fail.

- [ ] **Step 3: Implement the outer supervisor**

The supervisor owns discovery/control screens and delegates media keys/messages
only after `READY`. It injects selected distro and runtime layout into media
commands; it does not mutate process-global environment as hidden state.

- [ ] **Step 4: Verify GREEN and small-window rendering**

Run model tests at narrow and standard terminal sizes, then all Go tests.

### Task 6: Parameterize media WSL commands and preserve cancellation

**Files:**
- Modify: `downloader/main.go`
- Modify: `downloader/main_test.go`
- Modify: `downloader/stream.go`

- [ ] **Step 1: Write failing command-construction tests**

Require every download/cancel command to use the selected distro and runtime
base, keep URL/profile/track IDs separate, and preserve named-container cleanup.
No command may contain `Ubuntu`, `rtxwcz`, `D:\amd`, or a literal
`$HOME/amd/dl.sh` contract.

- [ ] **Step 2: Verify RED**

Run command construction and cancellation tests; observe the old hard-coded
contract fail.

- [ ] **Step 3: Inject a RuntimeTarget**

```go
type RuntimeTarget struct {
    Distribution string
    Base string
}
```

Use it for download, immediate cleanup, final cleanup, and displayed output
paths. Preserve streaming, bounded logs, grouped jobs, and Ctrl+C synchronization.

- [ ] **Step 4: Verify GREEN**

Run full Go tests, race tests, vet, and shell regressions.

### Task 7: Safe automatic wrapper recovery

**Files:**
- Create: `downloader/repair.go`
- Create: `downloader/repair_test.go`
- Modify: `downloader/runtime/login.sh`

- [ ] **Step 1: Write failing repair-budget tests**

Cover healthy, stopped+valid-session, missing-container+valid-session,
bad-ports restart once, recreate once, invalid-session needs-auth, active auth,
and concurrent lifecycle lock. Assert one repair/retry budget per media request.

- [ ] **Step 2: Verify RED**

Run focused tests and confirm the repair planner is absent.

- [ ] **Step 3: Implement a typed repair plan**

Keep destructive/session-reset operations out of automatic repair. Serialize
wrapper mutation and return structured next states instead of parsing stderr.

- [ ] **Step 4: Verify GREEN**

Run unit, race, and shell integration tests.

### Task 8: Single executable build and independence verification

**Files:**
- Modify: `downloader/go.mod` only if the module name or dependencies change
- Modify: `README.md`
- Build: `dist/ez-am.exe`

- [ ] **Step 1: Add failing artifact contract tests**

Create tests that copy only the built EXE to a temporary directory with no
scripts, change the working directory, run `ez-am doctor`, and assert it never
mentions or opens `D:\amd`. Add a source scan rejecting runtime literals for
`rtxwcz` and `/home/rtxwcz`.

- [ ] **Step 2: Verify RED**

Run artifact tests before the unified binary exists.

- [ ] **Step 3: Build the unified Windows binary**

Run:

```bash
cd /mnt/d/amd/downloader
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -buildvcs=false -trimpath -o ../dist/ez-am.exe .
```

- [ ] **Step 4: Verify from an unrelated directory**

Run `doctor` and a no-download TUI smoke test from a temporary Windows path
containing only the EXE. Verify embedded runtime inventory, distro selection,
real dynamic home, and Docker/wrapper classification.

- [ ] **Step 5: Run complete verification**

Run Go tests, race tests, vet, shell regressions, Windows build, SHA-256, secret
literal scan, C/D/WSL free-space checks, and existing real-media verification.

---

## Follow-on plans

After this foundation passes, write separate TDD plans for:

1. missing-WSL/Ubuntu installation, UAC, reboot checkpoint, Docker Desktop
   auto-start, Debian/Arch WSL Docker Engine installation, and network mirrors;
2. password/OTP stdin transport, onboarding, updates, structured logging,
   migrations, and application-owned cleanup;
3. native Debian and Arch platform backends, `.deb`, `.pkg.tar.zst`, signing,
   SBOM, and clean-environment release tests.

These are separate plans because each is independently testable and system
bootstrap/package installation has a much larger mutation boundary than the
foundation.
