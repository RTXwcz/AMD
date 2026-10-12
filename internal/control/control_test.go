package control

import (
	"context"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"ez-am/internal/doctor"
	"ez-am/internal/platform"
)

type fakeRunner struct {
	requests []platform.CommandRequest
	respond  func(platform.CommandRequest) (platform.CommandResult, error)
}

func (f *fakeRunner) Run(_ context.Context, request platform.CommandRequest) (platform.CommandResult, error) {
	f.requests = append(f.requests, request)
	if f.respond != nil {
		return f.respond(request)
	}
	return platform.CommandResult{}, nil
}

func checksFor(pairs ...doctor.Check) doctor.Report {
	report := doctor.Report{Code: doctor.Ready, Checks: pairs}
	for _, check := range pairs {
		if check.Status == doctor.StatusFail {
			report.Code = doctor.NeedsRuntime
			break
		}
	}
	return report
}

func TestActionsForMapsFailingChecksToOfferableActions(t *testing.T) {
	report := checksFor(
		doctor.Check{ID: "wsl", Title: "WSL", Status: doctor.StatusOK},
		doctor.Check{ID: "runtime", Title: "Runtime files", Status: doctor.StatusFail, Action: doctor.ActionInstallRuntime},
		doctor.Check{ID: "wrapper", Title: "Wrapper session", Status: doctor.StatusFail, Action: doctor.ActionLogin},
		doctor.Check{ID: "images", Title: "Images", Status: doctor.StatusWarn},
	)
	actions := actionsFor(report)

	got := make([]string, 0, len(actions))
	for _, item := range actions {
		got = append(got, item.title)
	}
	joined := strings.Join(got, " | ")
	if !strings.Contains(joined, "Install the bundled runtime files") {
		t.Fatalf("runtime action missing from %q", joined)
	}
	if !strings.Contains(joined, "Log in to Apple (2FA)") {
		t.Fatalf("login action missing from %q", joined)
	}
	if last := actions[len(actions)-1]; last.kind != actionQuit {
		t.Fatalf("expected the last action to quit, got %v", last.kind)
	}
	if slices.Contains(got, "Re-run the doctor") == false {
		t.Fatalf("re-run action missing from %q", joined)
	}
}

func TestActionsForReadyReportOnlyOffersRerunAndQuit(t *testing.T) {
	actions := actionsFor(checksFor(doctor.Check{ID: "wsl", Title: "WSL", Status: doctor.StatusOK}))
	if len(actions) != 2 {
		t.Fatalf("expected 2 actions, got %d", len(actions))
	}
	if actions[0].kind != actionRerun || actions[1].kind != actionQuit {
		t.Fatalf("unexpected action kinds: %v %v", actions[0].kind, actions[1].kind)
	}
}

func TestReportViewListsEveryCheckAndTheHeadline(t *testing.T) {
	m := newModel(context.Background(), Options{Runner: &fakeRunner{}})
	m.report = checksFor(
		doctor.Check{ID: "wsl", Title: "WSL", Status: doctor.StatusOK, Detail: "ok"},
		doctor.Check{ID: "wrapper", Title: "Wrapper session", Status: doctor.StatusFail, Detail: "session missing"},
	)
	m.phase = phaseReport
	view := m.renderReport()

	for _, want := range []string{"WSL", "Wrapper session", "session missing", "Setup needed"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view %q does not contain %q", view, want)
		}
	}
}

func TestStartSetupRefusesIncompleteCredentials(t *testing.T) {
	m := newModel(context.Background(), Options{Runner: &fakeRunner{}, Distro: "Ubuntu", Base: "/home/x/amd"})
	m.inputs[0].SetValue("someone@example.invalid")
	m.inputs[1].SetValue("")
	m.inputs[3].SetValue("token")

	updated, cmd := m.startSetup()
	if cmd != nil {
		t.Fatal("expected no command to run without a password")
	}
	final, ok := updated.(model)
	if !ok {
		t.Fatalf("unexpected model type %T", updated)
	}
	if final.phase == phaseRunning {
		t.Fatal("setup must not start with incomplete credentials")
	}
	if !strings.Contains(final.log, "required") {
		t.Fatalf("expected a validation message, got %q", final.log)
	}
}

func TestSubmitCodeRequiresSixDigits(t *testing.T) {
	for _, invalid := range []string{"", "12345", "1234567", "12a456", "１２３４５６"} {
		m := newModel(context.Background(), Options{Runner: &fakeRunner{}, Distro: "Ubuntu", Base: "/home/x/amd"})
		m.phase = phaseLogin
		m.inputs[0].SetValue(invalid)
		updated, cmd := m.submitCode()
		if cmd != nil {
			t.Fatalf("code %q must not be submitted", invalid)
		}
		final := updated.(model)
		if final.phase == phaseRunning {
			t.Fatalf("code %q must not start a submission", invalid)
		}
	}

	m := newModel(context.Background(), Options{Runner: &fakeRunner{}, Distro: "Ubuntu", Base: "/home/x/amd"})
	m.phase = phaseLogin
	m.inputs[0].SetValue("123456")
	updated, cmd := m.submitCode()
	if cmd == nil {
		t.Fatal("a six-digit code must start the submission")
	}
	if final := updated.(model); final.phase != phaseRunning {
		t.Fatalf("expected the running phase, got %v", final.phase)
	}
	if value := updated.(model).inputs[0].Value(); value != "" {
		t.Fatalf("the code must be cleared from the form, got %q", value)
	}
}

func TestSubmitCodeNeverPassesTheCodeThroughShellSource(t *testing.T) {
	runner := &fakeRunner{}
	m := newModel(context.Background(), Options{Runner: runner, Distro: "Ubuntu", Base: "/home/x/amd"})
	m.phase = phaseLogin
	m.inputs[0].SetValue("123456")
	_, cmd := m.submitCode()
	if cmd == nil {
		t.Fatal("expected a command")
	}
	if msg := cmd(); msg == nil {
		t.Fatal("expected a completion message")
	}
	if len(runner.requests) != 1 {
		t.Fatalf("expected one request, got %d", len(runner.requests))
	}
	request := runner.requests[0]
	if request.Args[len(request.Args)-1] != "123456" {
		t.Fatalf("the code must be its own argument, got %v", request.Args)
	}
	for _, arg := range request.Args {
		if strings.Contains(arg, "123456") && arg != "123456" {
			t.Fatalf("the code leaked into %q", arg)
		}
	}
}

func TestSetupPassesCredentialsThroughTheEnvironmentOnly(t *testing.T) {
	runner := &fakeRunner{}
	m := newModel(context.Background(), Options{Runner: runner, Distro: "Ubuntu", Base: "/home/x/amd"})
	m.inputs[0].SetValue("someone@example.invalid")
	m.inputs[1].SetValue("hunter2")
	m.inputs[2].SetValue("")
	m.inputs[3].SetValue("media-token")
	_, cmd := m.startSetup()
	if cmd == nil {
		t.Fatal("expected a command")
	}
	cmd()

	if len(runner.requests) != 1 {
		t.Fatalf("expected one request, got %d", len(runner.requests))
	}
	request := runner.requests[0]
	for _, arg := range request.Args {
		if strings.Contains(arg, "hunter2") || strings.Contains(arg, "media-token") {
			t.Fatalf("credentials must not appear in arguments: %v", request.Args)
		}
	}
	env := strings.Join(request.Env, " ")
	if !strings.Contains(env, "EZ_PASS=hunter2") || !strings.Contains(env, "EZ_STOREFRONT=us") {
		t.Fatalf("credentials and the storefront default must travel in the environment, got %v", request.Env)
	}
}

func TestResolveLayoutPrefersExplicitBase(t *testing.T) {
	legacy, err := resolveLayout("/home/alice", true, "")
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Base != "/home/alice/amd" {
		t.Fatalf("legacy layout should keep $HOME/amd, got %q", legacy.Base)
	}

	explicit, err := resolveLayout("/home/alice", false, "/opt/ez-am/")
	if err != nil {
		t.Fatal(err)
	}
	if explicit.Base != "/opt/ez-am" || explicit.Downloads != "/opt/ez-am/downloads" {
		t.Fatalf("explicit base not honoured: %+v", explicit)
	}

	if _, err := resolveLayout("", false, ""); err == nil {
		t.Fatal("an unknown home must be rejected")
	}
}

var _ tea.Model = model{}
