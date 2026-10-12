package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"ez-am/internal/platform"
)

// environment describes what a fake distribution reports back.
type environment struct {
	distros string

	uid       string
	user      string
	home      string
	arch      string
	osID      string
	osIDLike  string
	osVersion string

	base        string
	legacy      bool
	missing     string
	credentials bool

	docker      string
	dockerError string
	buildx      string
	images      []string

	wrapperOK     bool
	wrapperDetail string

	downloadsOK bool
}

func greenEnvironment() environment {
	return environment{
		distros:     "Ubuntu\r\n",
		uid:         "1000",
		user:        "alice",
		home:        "/home/alice",
		arch:        "x86_64",
		osID:        "ubuntu",
		osIDLike:    "debian",
		osVersion:   "24.04",
		base:        "/home/alice/amd",
		legacy:      true,
		credentials: true,
		docker:      "29.1.3",
		buildx:      "github.com/docker/buildx v0.38.0",
		images:      []string{"ghcr.io/zhaarey/apple-music-downloader:<none>", "local/wrapper:latest"},
		wrapperOK:   true,
		downloadsOK: true,
	}
}

// payload renders the record stream the real probe emits.
func (env environment) payload() string {
	var builder strings.Builder
	field := func(key, value string) {
		fmt.Fprintf(&builder, "\x1e%s=%s", key, value)
	}
	field("uid", env.uid)
	field("user", env.user)
	field("home", env.home)
	field("architecture", env.arch)
	field("os_id", env.osID)
	field("os_id_like", env.osIDLike)
	field("os_version_id", env.osVersion)
	field("base", env.base)
	if env.legacy {
		field("legacy", "1")
	} else {
		field("legacy", "0")
	}
	field("missing", env.missing)
	if env.credentials {
		field("credentials", "1")
	} else {
		field("credentials", "0")
	}
	field("docker", env.docker)
	if env.dockerError != "" {
		field("docker_error", env.dockerError)
	}
	field("buildx", env.buildx)
	for _, image := range env.images {
		field("image", image)
	}
	if env.wrapperOK {
		field("wrapper", "1")
	} else {
		field("wrapper", "0")
		if env.wrapperDetail != "" {
			field("wrapper_detail", env.wrapperDetail)
		}
	}
	if env.downloadsOK {
		field("downloads", "1")
	} else {
		field("downloads", "0")
	}
	return builder.String()
}

type fakeRunner struct {
	mutex   sync.Mutex
	calls   []platform.CommandRequest
	handler func(platform.CommandRequest) (platform.CommandResult, error)
}

func (f *fakeRunner) Run(_ context.Context, request platform.CommandRequest) (platform.CommandResult, error) {
	f.mutex.Lock()
	f.calls = append(f.calls, request)
	f.mutex.Unlock()
	if f.handler == nil {
		return platform.CommandResult{}, nil
	}
	return f.handler(request)
}

func (f *fakeRunner) recorded() []platform.CommandRequest {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	out := make([]platform.CommandRequest, len(f.calls))
	copy(out, f.calls)
	return out
}

var errNotStarted = &startError{}

type startError struct{}

func (*startError) Error() string { return "executable file not found in %PATH%" }

// classify names the logical operation behind a request.
func classify(request platform.CommandRequest) string {
	if request.Name != "wsl.exe" {
		return "other"
	}
	joined := strings.Join(request.Args, " ")
	switch {
	case len(request.Args) == 1 && request.Args[0] == "--status":
		return "wsl-status"
	case len(request.Args) == 2 && request.Args[0] == "-l":
		return "wsl-list"
	case strings.Contains(joined, "ez-am-probe-facts"):
		return "probe-facts"
	}
	return "other"
}

func newRunner(env environment) *fakeRunner {
	return &fakeRunner{handler: func(request platform.CommandRequest) (platform.CommandResult, error) {
		switch classify(request) {
		case "wsl-status":
			return platform.CommandResult{Stdout: []byte("Default Distribution: " + strings.TrimSpace(env.distros))}, nil
		case "wsl-list":
			return platform.CommandResult{Stdout: []byte(env.distros)}, nil
		case "probe-facts":
			return platform.CommandResult{Stdout: []byte(env.payload())}, nil
		}
		return platform.CommandResult{}, nil
	}}
}

func TestRunWithoutWSLReportsUnsupportedPlatform(t *testing.T) {
	runner := &fakeRunner{handler: func(platform.CommandRequest) (platform.CommandResult, error) {
		return platform.CommandResult{}, errNotStarted
	}}
	report := Run(context.Background(), Inputs{Runner: runner})
	if report.Code != UnsupportedPlatform {
		t.Fatalf("expected UNSUPPORTED_PLATFORM, got %s", report.Code)
	}
	if !strings.Contains(report.Text(), "wsl.exe is not available") {
		t.Fatalf("text should explain the missing wsl.exe: %s", report.Text())
	}
}

func TestRunWithoutDistributionsNeedsDistro(t *testing.T) {
	env := greenEnvironment()
	env.distros = "docker-desktop\r\n"
	report := Run(context.Background(), Inputs{Runner: newRunner(env)})
	if report.Code != NeedsDistro {
		t.Fatalf("expected NEEDS_DISTRO, got %s (%v)", report.Code, report.Checks)
	}
}

func TestRunWithTwoDistributionsNeedsSelection(t *testing.T) {
	env := greenEnvironment()
	env.distros = "Ubuntu\r\nDebian\r\n"
	report := Run(context.Background(), Inputs{Runner: newRunner(env)})
	if report.Code != NeedsSelection {
		t.Fatalf("expected NEEDS_SELECTION, got %s", report.Code)
	}
	if actions := report.Actions(); len(actions) != 1 || actions[0] != ActionChooseDistro {
		t.Fatalf("expected the distro choice action, got %v", actions)
	}
	if !strings.Contains(report.Text(), "Ubuntu, Debian") {
		t.Fatalf("text should list the candidates: %s", report.Text())
	}
}

func TestRunWithoutDockerIsDockerUnavailable(t *testing.T) {
	env := greenEnvironment()
	env.docker = ""
	env.dockerError = "Cannot connect to the Docker daemon at unix:///var/run/docker.sock"
	report := Run(context.Background(), Inputs{Runner: newRunner(env)})
	if report.Code != DockerUnavailable {
		t.Fatalf("expected DOCKER_UNAVAILABLE, got %s (%v)", report.Code, report.Checks)
	}
	if actions := report.Actions(); len(actions) != 1 || actions[0] != ActionStartDocker {
		t.Fatalf("expected the start-docker action, got %v", actions)
	}
}

func TestRunWithoutRuntimeFilesNeedsRuntime(t *testing.T) {
	env := greenEnvironment()
	env.missing = "dl.sh,login.sh"
	report := Run(context.Background(), Inputs{Runner: newRunner(env)})
	if report.Code != NeedsRuntime {
		t.Fatalf("expected NEEDS_RUNTIME, got %s", report.Code)
	}
	if actions := report.Actions(); len(actions) != 1 || actions[0] != ActionInstallRuntime {
		t.Fatalf("expected the install-runtime action, got %v", actions)
	}
	if !strings.Contains(report.Text(), "missing under /home/alice/amd") {
		t.Fatalf("text should name the base directory: %s", report.Text())
	}
}

func TestRunWithoutCredentialsNeedsDeploy(t *testing.T) {
	env := greenEnvironment()
	env.credentials = false
	report := Run(context.Background(), Inputs{Runner: newRunner(env)})
	if report.Code != NeedsDeploy {
		t.Fatalf("expected NEEDS_DEPLOY, got %s", report.Code)
	}
	if actions := report.Actions(); len(actions) != 1 || actions[0] != ActionCollectCreds {
		t.Fatalf("expected the credential action, got %v", actions)
	}
}

func TestRunWithMissingImagesOnlyWarns(t *testing.T) {
	env := greenEnvironment()
	env.images = []string{"redis:7-alpine"}
	report := Run(context.Background(), Inputs{Runner: newRunner(env)})
	if !report.Ready() {
		t.Fatalf("missing images are a warning, not a failure: %s (%v)", report.Code, report.Checks)
	}
	for _, check := range report.Checks {
		if check.ID != "images" {
			continue
		}
		if check.Status != StatusWarn || !strings.Contains(check.Detail, "downloader") || !strings.Contains(check.Detail, "wrapper") {
			t.Fatalf("unexpected images row: %+v", check)
		}
	}
}

func TestRunWithUnhealthyWrapperNeedsAuth(t *testing.T) {
	env := greenEnvironment()
	env.wrapperOK = false
	env.wrapperDetail = "[!] Persisted wrapper session is missing or empty. Run: bash login.sh interactive"
	report := Run(context.Background(), Inputs{Runner: newRunner(env)})
	if report.Code != NeedsAuth {
		t.Fatalf("expected NEEDS_AUTH, got %s", report.Code)
	}
	for _, check := range report.Checks {
		if check.ID != "wrapper" {
			continue
		}
		if check.Action != ActionLogin {
			t.Fatalf("the wrapper row must offer the login action: %+v", check)
		}
		if strings.Contains(check.Detail, "\n") || !strings.Contains(check.Detail, "Persisted wrapper session") {
			t.Fatalf("unexpected wrapper detail %q", check.Detail)
		}
	}
}

func TestRunRejectsRootDefaultUserAndUnsupportedFamily(t *testing.T) {
	rootEnv := greenEnvironment()
	rootEnv.uid = "0"
	rootEnv.user = "root"
	rootEnv.home = "/root"
	rootReport := Run(context.Background(), Inputs{Runner: newRunner(rootEnv)})
	if rootReport.Ready() {
		t.Fatal("a root default user must not be considered ready")
	}
	if !strings.Contains(rootReport.Text(), "root") {
		t.Fatalf("text should explain the root user: %s", rootReport.Text())
	}

	familyEnv := greenEnvironment()
	familyEnv.osID = "fedora"
	familyEnv.osIDLike = ""
	familyReport := Run(context.Background(), Inputs{Runner: newRunner(familyEnv)})
	if familyReport.Ready() {
		t.Fatal("an unsupported family must not be considered ready")
	}
	if !strings.Contains(familyReport.Text(), "unsupported distribution family") {
		t.Fatalf("text should explain the family: %s", familyReport.Text())
	}
}

func TestRunAllGreenIsReady(t *testing.T) {
	runner := newRunner(greenEnvironment())
	report := Run(context.Background(), Inputs{Runner: runner})
	for _, call := range runner.recorded() {
		t.Logf("call %-12s %v", classify(call), call.Args)
	}
	if !report.Ready() {
		t.Fatalf("expected READY, got %s (%v)", report.Code, report.Checks)
	}
	if report.Distro != "Ubuntu" {
		t.Fatalf("expected the Ubuntu distribution, got %q", report.Distro)
	}
	if report.Base != "/home/alice/amd" {
		t.Fatalf("expected the legacy base, got %q", report.Base)
	}
	if len(report.Checks) != 9 {
		t.Fatalf("expected 9 checks, got %d: %v", len(report.Checks), report.Checks)
	}

	text := report.Text()
	for _, want := range []string{"WSL", "Distribution", "Docker", "Runtime files", "Credentials", "Wrapper session", "READY"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text %q does not mention %q", text, want)
		}
	}
	if strings.ContainsAny(text, "\x1b") {
		t.Fatal("the plain-text report must not contain ANSI escapes")
	}

	encoded, err := report.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Code   string `json:"code"`
		Ready  bool   `json:"ready"`
		Checks []struct {
			ID string `json:"id"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Code != string(Ready) || !decoded.Ready || len(decoded.Checks) != 9 {
		t.Fatalf("unexpected JSON payload: %s", encoded)
	}
	if strings.Contains(string(encoded), "password") || strings.Contains(string(encoded), "APPLE_ID") {
		t.Fatalf("the report must never carry credential material: %s", encoded)
	}
}

// The point of the consolidated probe: a fixed, small number of wsl.exe
// invocations no matter how many facts the report needs.
func TestDoctorUsesAtMostThreeWSLInvocations(t *testing.T) {
	runner := newRunner(greenEnvironment())
	Run(context.Background(), Inputs{Runner: runner})

	counts := map[string]int{}
	for _, call := range runner.recorded() {
		counts[classify(call)]++
	}
	if counts["probe-facts"] != 1 {
		t.Fatalf("expected exactly one consolidated probe, got %v", counts)
	}
	if counts["wsl-status"] != 1 || counts["wsl-list"] != 1 {
		t.Fatalf("expected one status and one inventory call, got %v", counts)
	}
	if total := len(runner.recorded()); total > 3 {
		t.Fatalf("expected at most 3 wsl.exe invocations, got %d: %v", total, counts)
	}
}

func TestProbeKeepsDynamicValuesOutOfTheScript(t *testing.T) {
	runner := newRunner(greenEnvironment())
	Run(context.Background(), Inputs{Runner: runner, Base: "/opt/ez-am/"})

	var probe platform.CommandRequest
	for _, call := range runner.recorded() {
		if classify(call) == "probe-facts" {
			probe = call
		}
	}
	if len(probe.Args) == 0 {
		t.Fatal("the consolidated probe was never invoked")
	}
	if probe.Args[0] != "-d" || probe.Args[1] != "Ubuntu" {
		t.Fatalf("the distribution must be its own argument: %v", probe.Args)
	}
	script := probe.Args[5]
	if !strings.Contains(script, "ez-am-probe-facts") {
		t.Fatalf("unexpected probe script: %q", script)
	}
	for _, dynamic := range []string{"Ubuntu", "/opt/ez-am", "alice"} {
		if strings.Contains(script, dynamic) {
			t.Fatalf("dynamic value %q leaked into the script source", dynamic)
		}
	}
	if last := probe.Args[len(probe.Args)-1]; last != "/opt/ez-am" {
		t.Fatalf("the explicit base must travel as its own argument, got %q in %v", last, probe.Args)
	}
}

func TestExplicitDistributionAndBaseAreHonoured(t *testing.T) {
	env := greenEnvironment()
	env.distros = "Ubuntu\r\nDebian\r\n"
	env.base = "/opt/ez-am"
	env.legacy = false
	report := Run(context.Background(), Inputs{Runner: newRunner(env), Distro: "Debian", Base: "/opt/ez-am/"})
	if !report.Ready() {
		t.Fatalf("expected READY with explicit inputs, got %s (%v)", report.Code, report.Checks)
	}
	if report.Distro != "Debian" {
		t.Fatalf("expected Debian, got %q", report.Distro)
	}
	if report.Base != "/opt/ez-am" {
		t.Fatalf("expected the explicit base without a trailing slash, got %q", report.Base)
	}
}

func TestParseFactsRejectsMalformedPayloads(t *testing.T) {
	if _, err := parseFacts([]byte("no separator here")); err == nil {
		t.Fatal("a payload without records must be rejected")
	}
	if _, err := parseFacts([]byte("\x1euid=1000\x1euser=alice")); err == nil {
		t.Fatal("a payload with missing required fields must be rejected")
	}
	if _, err := parseFacts([]byte("\x1euid=x\x1euser=alice\x1ehome=/home/alice\x1eos_id=ubuntu\x1ebase=/home/alice/amd")); err == nil {
		t.Fatal("a non-numeric uid must be rejected")
	}
	if _, err := parseFacts([]byte("\x1euid=1000\x1euser=alice\x1ehome=/home/alice\x1eos_id=ubuntu\x1ebase=/b\x1eunknown=1")); err == nil {
		t.Fatal("an unknown field must be rejected")
	}
}
