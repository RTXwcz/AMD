package doctor

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"ez-am/internal/platform"
)

const probePayload = "\x1euid=1000\x1euser=alice\x1ehome=/home/alice\x1earchitecture=x86_64" +
	"\x1eos_id=ubuntu\x1eos_id_like=debian\x1eos_version_id=24.04"

type fakeRunner struct {
	calls   []platform.CommandRequest
	handler func(platform.CommandRequest) (platform.CommandResult, error)
}

func (f *fakeRunner) Run(_ context.Context, request platform.CommandRequest) (platform.CommandResult, error) {
	f.calls = append(f.calls, request)
	if f.handler == nil {
		return platform.CommandResult{}, nil
	}
	return f.handler(request)
}

// classify names the logical operation behind a request so a test can script
// answers without depending on exact argv shapes.
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
	case strings.Contains(joined, "docker info"):
		return "docker-info"
	case strings.Contains(joined, "buildx"):
		return "buildx"
	case strings.Contains(joined, "docker images"):
		return "images"
	}
	if len(request.Args) >= 6 && request.Args[2] == "--" && request.Args[3] == "sh" && request.Args[4] == "-c" {
		if strings.Contains(request.Args[5], "os-release") {
			return "probe-distribution"
		}
		return "other"
	}
	if len(request.Args) >= 6 && request.Args[4] == "-lc" {
		script := request.Args[5]
		switch {
		case strings.Contains(script, "os-release"):
			return "probe-distribution"
		case strings.Contains(script, "$HOME/amd/login.sh"):
			return "probe-legacy"
		case strings.Contains(script, "missing="):
			return "probe-runtime"
		case strings.Contains(script, "config.yaml"):
			return "probe-config"
		case strings.Contains(script, "-w"):
			return "probe-downloads"
		}
	}
	if len(request.Args) >= 6 && strings.HasSuffix(request.Args[4], "/login.sh") && request.Args[5] == "status" {
		return "login-status"
	}
	return "other"
}

type environment struct {
	status      string
	distros     string
	probeFails  bool
	docker      bool
	buildx      bool
	legacyBase  string
	runtimeOK   bool
	configOK    bool
	imagesOK    bool
	wrapperLogs string
	wrapperOK   bool
	downloadsOK bool
}

func newRunner(env environment) *fakeRunner {
	return &fakeRunner{handler: func(request platform.CommandRequest) (platform.CommandResult, error) {
		switch classify(request) {
		case "wsl-status":
			return platform.CommandResult{Stdout: []byte(env.status)}, nil
		case "wsl-list":
			return platform.CommandResult{Stdout: []byte(env.distros)}, nil
		case "probe-distribution":
			if env.probeFails {
				return platform.CommandResult{Code: 65}, nil
			}
			return platform.CommandResult{Stdout: []byte(probePayload)}, nil
		case "docker-info":
			if !env.docker {
				return platform.CommandResult{Code: 1, Stderr: []byte("Cannot connect to the Docker daemon")}, nil
			}
			return platform.CommandResult{Stdout: []byte("29.1.3\n")}, nil
		case "buildx":
			if !env.buildx {
				return platform.CommandResult{Code: 1}, nil
			}
			return platform.CommandResult{Stdout: []byte("github.com/docker/buildx v0.38.0\n")}, nil
		case "probe-legacy":
			if env.legacyBase != "" {
				return platform.CommandResult{Stdout: []byte(env.legacyBase)}, nil
			}
			return platform.CommandResult{Code: 1}, nil
		case "probe-runtime":
			if env.runtimeOK {
				return platform.CommandResult{Stdout: []byte("missing=")}, nil
			}
			return platform.CommandResult{Code: 1, Stdout: []byte("missing=setup.sh,login.sh")}, nil
		case "probe-config":
			if env.configOK {
				return platform.CommandResult{}, nil
			}
			return platform.CommandResult{Code: 1}, nil
		case "probe-downloads":
			if env.downloadsOK {
				return platform.CommandResult{}, nil
			}
			return platform.CommandResult{Code: 1}, nil
		case "images":
			if env.imagesOK {
				return platform.CommandResult{Stdout: []byte("ghcr.io/zhaarey/apple-music-downloader:<none>\nlocal/wrapper:latest\n")}, nil
			}
			return platform.CommandResult{Stdout: []byte("redis:7-alpine\n")}, nil
		case "login-status":
			if env.wrapperOK {
				return platform.CommandResult{Stdout: []byte("[+] wrapper is running\n")}, nil
			}
			return platform.CommandResult{Code: 1, Stderr: []byte(env.wrapperLogs)}, nil
		}
		return platform.CommandResult{}, nil
	}}
}

func greenEnvironment() environment {
	return environment{
		status:      "Default Distribution: Ubuntu\r\n",
		distros:     "Ubuntu\r\n",
		docker:      true,
		buildx:      true,
		legacyBase:  "/home/alice/amd",
		runtimeOK:   true,
		configOK:    true,
		imagesOK:    true,
		wrapperOK:   true,
		downloadsOK: true,
	}
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

var errNotStarted = &startError{}

type startError struct{}

func (*startError) Error() string { return "executable file not found in %PATH%" }

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
	actions := report.Actions()
	if len(actions) != 1 || actions[0] != ActionChooseDistro {
		t.Fatalf("expected the distro choice action, got %v", actions)
	}
	if !strings.Contains(report.Text(), "Ubuntu, Debian") {
		t.Fatalf("text should list the candidates: %s", report.Text())
	}
}

func TestRunWithoutDockerIsDockerUnavailable(t *testing.T) {
	env := greenEnvironment()
	env.docker = false
	report := Run(context.Background(), Inputs{Runner: newRunner(env)})
	if report.Code != DockerUnavailable {
		t.Fatalf("expected DOCKER_UNAVAILABLE, got %s", report.Code)
	}
	if actions := report.Actions(); len(actions) != 1 || actions[0] != ActionStartDocker {
		t.Fatalf("expected the start-docker action, got %v", actions)
	}
}

func TestRunWithoutRuntimeFilesNeedsRuntime(t *testing.T) {
	env := greenEnvironment()
	env.runtimeOK = false
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
	env.configOK = false
	report := Run(context.Background(), Inputs{Runner: newRunner(env)})
	if report.Code != NeedsDeploy {
		t.Fatalf("expected NEEDS_DEPLOY, got %s", report.Code)
	}
	if actions := report.Actions(); len(actions) != 1 || actions[0] != ActionCollectCreds {
		t.Fatalf("expected the credential action, got %v", actions)
	}
}

func TestRunWithUnhealthyWrapperNeedsAuth(t *testing.T) {
	env := greenEnvironment()
	env.wrapperOK = false
	env.wrapperLogs = "[!] Persisted wrapper session is missing or empty. Run: bash login.sh interactive\n"
	report := Run(context.Background(), Inputs{Runner: newRunner(env)})
	if report.Code != NeedsAuth {
		t.Fatalf("expected NEEDS_AUTH, got %s", report.Code)
	}
	for _, check := range report.Checks {
		if check.ID == "wrapper" {
			if !strings.Contains(check.Detail, "Persisted wrapper session") {
				t.Fatalf("wrapper detail should carry the actionable line, got %q", check.Detail)
			}
			if strings.Contains(check.Detail, "\n") {
				t.Fatalf("wrapper detail must be a single line, got %q", check.Detail)
			}
		}
	}
}

func TestRunAllGreenIsReady(t *testing.T) {
	runner := newRunner(greenEnvironment())
	report := Run(context.Background(), Inputs{Runner: runner})
	for _, call := range runner.calls {
		t.Logf("call %-20s %v", classify(call), call.Args)
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
		t.Fatalf("expected 9 checks, got %d", len(report.Checks))
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

func TestProbeKeepsDistributionAndBaseAsSeparateArguments(t *testing.T) {
	runner := newRunner(greenEnvironment())
	Run(context.Background(), Inputs{Runner: runner})

	var wrapperCalls, runtimeCalls int
	for _, call := range runner.calls {
		switch classify(call) {
		case "login-status":
			wrapperCalls++
			if call.Args[0] != "-d" || call.Args[1] != "Ubuntu" {
				t.Fatalf("distribution must be its own argument: %v", call.Args)
			}
			last := call.Args[len(call.Args)-1]
			if last != "status" {
				t.Fatalf("unexpected trailing argument: %v", call.Args)
			}
			for _, arg := range call.Args {
				if strings.Contains(arg, "$HOME") {
					t.Fatalf("the runtime base must not be resolved through the shell: %v", call.Args)
				}
			}
		case "probe-runtime":
			runtimeCalls++
			if len(call.Args) < 8 {
				t.Fatalf("the base must be passed as $1: %v", call.Args)
			}
			if call.Args[7] != "/home/alice/amd" {
				t.Fatalf("unexpected base argument: %v", call.Args)
			}
			if strings.Contains(call.Args[5], "/home/alice") {
				t.Fatalf("the base leaked into the script source: %v", call.Args[5])
			}
		}
	}
	if wrapperCalls != 1 || runtimeCalls != 1 {
		t.Fatalf("expected one wrapper probe and one runtime probe, got %d/%d", wrapperCalls, runtimeCalls)
	}
}

func TestExplicitDistributionAndBaseAreHonoured(t *testing.T) {
	env := greenEnvironment()
	env.distros = "Ubuntu\r\nDebian\r\n"
	env.legacyBase = ""
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
