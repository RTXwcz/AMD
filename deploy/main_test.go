package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

func TestWSLScriptArgsKeepUserValuesOutOfShellSource(t *testing.T) {
	url := "https://music.apple.com/us/album/a song/$()"
	got := wslScriptArgs("Ubuntu", `exec "$HOME/amd/dl.sh" "$@"`, "ez-am", url, "alac")
	want := []string{
		"-d", "Ubuntu", "--exec", "bash", "-lc",
		`exec "$HOME/amd/dl.sh" "$@"`,
		"ez-am", url, "alac",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wsl args = %#v, want %#v", got, want)
	}
}

func TestDeploymentCollectsTokenBeforePrepare(t *testing.T) {
	m := initialModel()
	m.stage = sCreds
	m.inputs[0].SetValue("user@example.com")
	m.inputs[1].SetValue("password")
	m.inputs[2].SetValue("us")
	next, _ := m.handleEnter()
	if next.(model).stage != sToken {
		t.Fatalf("after credentials stage = %v, want token stage", next.(model).stage)
	}
	if !next.(model).inputs[4].Focused() {
		t.Fatal("token input is not focused")
	}
}

func TestTokenInputIsMasked(t *testing.T) {
	m := initialModel()
	if m.inputs[4].EchoMode != textinput.EchoPassword {
		t.Fatalf("token echo mode = %v, want password", m.inputs[4].EchoMode)
	}
}

func TestTwoFAInputIsMasked(t *testing.T) {
	m := initialModel()
	if m.inputs[3].EchoMode != textinput.EchoPassword {
		t.Fatalf("2FA echo mode = %v, want password", m.inputs[3].EchoMode)
	}
}

func TestTwoFASubmissionClearsCodeAndRejectsDuplicateEnter(t *testing.T) {
	m := initialModel()
	m.stage = s2FA
	m.inputs[3].SetValue("123456")

	next, first := m.handleEnter()
	got := next.(model)
	if first == nil {
		t.Fatal("first 2FA submission did not create a command")
	}
	if got.inputs[3].Value() != "" {
		t.Fatal("2FA code remains in the active model")
	}
	if !got.submitting {
		t.Fatal("2FA submission was not marked in flight")
	}

	_, second := got.handleEnter()
	if second != nil {
		t.Fatal("duplicate Enter created a second 2FA command")
	}
}

func TestDeploymentTokenSubmissionStartsPrepare(t *testing.T) {
	m := initialModel()
	m.stage = sToken
	m.inputs[4].SetValue("token")
	next, _ := m.handleEnter()
	if next.(model).stage != sDeploy {
		t.Fatalf("after token stage = %v, want deploy stage", next.(model).stage)
	}
	got := next.(model)
	if got.inputs[1].Value() != "" || got.inputs[4].Value() != "" {
		t.Fatal("password or token remains in the active TUI model")
	}
}

func TestDoneStageCanQuit(t *testing.T) {
	m := initialModel()
	m.stage = sDone
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("Ctrl+C on done stage did not return a quit command")
	}
}

func TestCancelClearsSecretsAndQuitsInputStages(t *testing.T) {
	for _, stage := range []stage{sCreds, sToken, s2FA} {
		m := populatedDeployModel()
		m.stage = stage
		m.inputs[3].SetValue("123456")
		next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
		got := next.(model)
		if cmd == nil {
			t.Fatalf("stage %v Ctrl+C did not quit", stage)
		}
		if !got.cancelled {
			t.Fatalf("stage %v Ctrl+C did not mark cancellation", stage)
		}
		for _, index := range []int{1, 3, 4} {
			if got.inputs[index].Value() != "" {
				t.Fatalf("stage %v secret input %d was not cleared", stage, index)
			}
		}
	}
}

func TestDeployCtrlCCancelsChildAndWaitsForCompletion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := populatedDeployModel()
	m.stage = sDeploy
	m.cancel = cancel
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	got := next.(model)
	if cmd != nil {
		t.Fatal("deploy Ctrl+C quit before the child completed")
	}
	if !got.cancelled {
		t.Fatal("deploy Ctrl+C did not mark cancellation")
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("deploy context was not cancelled")
	}
	for _, index := range []int{1, 3, 4} {
		if got.inputs[index].Value() != "" {
			t.Fatalf("deploy secret input %d was not cleared", index)
		}
	}

	finished, quit := got.Update(deployTick{finished: true, failed: true, log: "cancelled"})
	finishedModel := finished.(model)
	if quit == nil || finishedModel.stage != sDone {
		t.Fatalf("cancelled deploy completion = %#v cmd=%v, want done + quit", finishedModel, quit != nil)
	}
}

func TestWSLDistroDefaultsAndCanBeOverridden(t *testing.T) {
	t.Setenv("EZ_WSL_DISTRO", "")
	if got := wslDistro(); got != "Ubuntu" {
		t.Fatalf("default distro = %q, want Ubuntu", got)
	}
	t.Setenv("EZ_WSL_DISTRO", "Debian")
	if got := wslDistro(); got != "Debian" {
		t.Fatalf("overridden distro = %q, want Debian", got)
	}
}

func TestLocateSetupScriptSearchOrder(t *testing.T) {
	root := t.TempDir()
	exeDir := filepath.Join(root, "bin")
	cwd := filepath.Join(root, "cwd")
	if err := os.MkdirAll(exeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}

	exeSetup := filepath.Join(exeDir, "setup.sh")
	parentSetup := filepath.Join(root, "setup.sh")
	cwdSetup := filepath.Join(cwd, "setup.sh")
	for _, path := range []string{exeSetup, parentSetup, cwdSetup} {
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	executable := filepath.Join(exeDir, "ez-am-deploy.exe")
	for _, tc := range []struct {
		remove  string
		want    string
		wantErr bool
	}{
		{"", exeSetup, false},
		{exeSetup, parentSetup, false},
		{parentSetup, "", true},
	} {
		if tc.remove != "" {
			if err := os.Remove(tc.remove); err != nil {
				t.Fatal(err)
			}
		}
		got, err := locateSetupScript(executable, cwd)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("located untrusted cwd setup %q", got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("locate setup: %v", err)
		}
		if got != tc.want {
			t.Fatalf("located %q, want %q", got, tc.want)
		}
	}
}

func TestLocateSetupScriptRejectsAdjacentSymlink(t *testing.T) {
	root := t.TempDir()
	exeDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(exeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	parentSetup := filepath.Join(root, "setup.sh")
	if err := os.WriteFile(parentSetup, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(exeDir, "setup.sh")
	if err := os.Symlink(parentSetup, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	got, err := locateSetupScript(filepath.Join(exeDir, "ez-am-deploy.exe"), filepath.Join(root, "cwd"))
	if err != nil {
		t.Fatalf("locate setup: %v", err)
	}
	if got != parentSetup {
		t.Fatalf("located %q, want trusted regular parent %q", got, parentSetup)
	}
}

func TestMergedWSLEnvReplacesConflictingModifiers(t *testing.T) {
	got := mergedWSLEnv(
		"EXISTING/u:EZ_PASS/p:ez_token/w:Ez_User/l",
		"EZ_USER", "EZ_PASS", "EZ_TOKEN", "EZ_STOREFRONT",
	)
	want := "EXISTING/u:EZ_USER/u:EZ_PASS/u:EZ_TOKEN/u:EZ_STOREFRONT/u"
	if got != want {
		t.Fatalf("merged WSLENV = %q, want %q", got, want)
	}
}

func TestSetupAndLoginCommandsUseContextAndWaitDelay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	setup := newSetupCommandContext(ctx, `C:\amd\setup.sh`, "user", "pass", "token", "us")
	if setup.Cancel == nil || setup.WaitDelay <= 0 {
		t.Fatalf("setup command cancellation = %v wait delay = %v", setup.Cancel != nil, setup.WaitDelay)
	}
	login := newLoginCommandContext(ctx, "status")
	if login.Cancel == nil || login.WaitDelay <= 0 {
		t.Fatalf("login command cancellation = %v wait delay = %v", login.Cancel != nil, login.WaitDelay)
	}
}

func TestSetupCommandPassesSecretsOnlyThroughEnvironment(t *testing.T) {
	t.Setenv("EZ_WSL_DISTRO", "Debian")
	t.Setenv("WSLENV", "EXISTING")
	values := struct {
		user, pass, token, storefront string
	}{"user@example.com", "pa ss$()", "token-secret", "jp"}
	script := `C:\Program Files\ez-am\setup.sh`

	cmd := newSetupCommand(script, values.user, values.pass, values.token, values.storefront)
	joinedArgs := strings.Join(cmd.Args, "\x00")
	for _, secret := range []string{values.user, values.pass, values.token, values.storefront} {
		if strings.Contains(joinedArgs, secret) {
			t.Fatalf("secret %q leaked into argv %#v", secret, cmd.Args)
		}
	}
	wantArgs := append([]string{"wsl.exe"}, wslScriptArgs("Debian", setupScriptShell, "ez-am-setup", script)...)
	if !reflect.DeepEqual(cmd.Args, wantArgs) {
		t.Fatalf("setup args = %#v, want %#v", cmd.Args, wantArgs)
	}
	for key, want := range map[string]string{
		"EZ_USER": values.user, "EZ_PASS": values.pass,
		"EZ_TOKEN": values.token, "EZ_STOREFRONT": values.storefront,
	} {
		if got := envValue(cmd.Env, key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
	wslEnv := envValue(cmd.Env, "WSLENV")
	for _, name := range []string{"EXISTING", "EZ_USER", "EZ_PASS", "EZ_TOKEN", "EZ_STOREFRONT"} {
		if !strings.Contains(wslEnv, name) {
			t.Fatalf("WSLENV %q does not include %q", wslEnv, name)
		}
	}
}

func TestDeploymentRunsSetupLoginStartAndStatusInOrder(t *testing.T) {
	m := populatedDeployModel()
	var commands []*exec.Cmd
	runner := func(cmd *exec.Cmd) ([]byte, error) {
		commands = append(commands, cmd)
		return []byte("ok"), nil
	}

	result := m.runDeployment(`C:\amd\setup.sh`, runner)
	if result.failed || result.needs2FA {
		t.Fatalf("deployment result = %#v, want healthy success", result)
	}
	if len(commands) != 3 {
		t.Fatalf("command count = %d, want 3", len(commands))
	}
	if got := commands[1].Args[len(commands[1].Args)-1]; got != "start" {
		t.Fatalf("second command action = %q, want start", got)
	}
	if got := commands[2].Args[len(commands[2].Args)-1]; got != "status" {
		t.Fatalf("third command action = %q, want status", got)
	}
}

func TestDeploymentStopsFor2FAWithoutRunningStatus(t *testing.T) {
	m := populatedDeployModel()
	call := 0
	runner := func(cmd *exec.Cmd) ([]byte, error) {
		call++
		if call == 2 {
			return []byte("STATE=NEED_2FA\n"), nil
		}
		return []byte("ok"), nil
	}

	result := m.runDeployment(`C:\amd\setup.sh`, runner)
	if result.failed || !result.needs2FA {
		t.Fatalf("deployment result = %#v, want needs 2FA", result)
	}
	if call != 2 {
		t.Fatalf("command count = %d, want setup and login start only", call)
	}
}

func TestDeploymentRequiresHealthyStatus(t *testing.T) {
	m := populatedDeployModel()
	call := 0
	runner := func(cmd *exec.Cmd) ([]byte, error) {
		call++
		if call == 3 {
			return []byte("unhealthy"), errors.New("exit status 1")
		}
		return []byte("ok"), nil
	}

	result := m.runDeployment(`C:\amd\setup.sh`, runner)
	if !result.failed || !strings.Contains(result.err, "login status") {
		t.Fatalf("deployment result = %#v, want status failure", result)
	}
}

func TestDeploymentDiagnosticsRedactSecrets(t *testing.T) {
	m := initialModel()
	m.inputs[0].SetValue("user-sentinel@example.com")
	m.inputs[1].SetValue("password-sentinel")
	m.inputs[2].SetValue("us")
	m.inputs[4].SetValue("token-sentinel")
	runner := func(_ *exec.Cmd) ([]byte, error) {
		return []byte("output user-sentinel@example.com password-sentinel token-sentinel"), nil
	}

	result := m.runDeployment(`C:\amd\setup.sh`, runner)
	for _, secret := range []string{"user-sentinel@example.com", "password-sentinel", "token-sentinel"} {
		if strings.Contains(result.log, secret) || strings.Contains(result.err, secret) {
			t.Fatalf("deployment diagnostic leaked %q: %#v", secret, result)
		}
	}
	if !strings.Contains(result.log, "REDACTED") {
		t.Fatalf("deployment diagnostic did not retain redacted evidence: %q", result.log)
	}
}

func TestTwoFADiagnosticsRedactCode(t *testing.T) {
	code := "123456"
	runner := func(_ *exec.Cmd) ([]byte, error) {
		return []byte("submitted code=" + code), errors.New("code=" + code)
	}
	result := initialModel().run2FA(code, runner)
	if result.ok || !result.failed {
		t.Fatalf("2FA result = %#v, want failure", result)
	}
	for _, text := range []string{result.log, result.err} {
		if strings.Contains(text, code) {
			t.Fatalf("2FA diagnostic leaked code: %q", text)
		}
	}
}

func TestFailedDeployViewUsesSafeActionableDiagnostics(t *testing.T) {
	m := initialModel()
	m.stage = sDeploy
	next, _ := m.Update(deployTick{
		finished: true,
		failed:   true,
		err:      "setup failed: password-sentinel",
		log:      "setup failed: password-sentinel\ncheck Docker Desktop and login status",
	})
	got := next.(model)
	view := got.View()
	if strings.Contains(view, "password-sentinel") {
		t.Fatalf("failed deployment view leaked a secret: %q", view)
	}
	if !strings.Contains(view, "Diagnostics") || !strings.Contains(view, "login status") {
		t.Fatalf("failed deployment view lost actionable diagnostics: %q", view)
	}
}

func Test2FARequiresExactlySixASCIIDigits(t *testing.T) {
	for _, code := range []string{"1234", "12345a", "1234567", "１２３４５６"} {
		m := initialModel()
		m.stage = s2FA
		m.inputs[3].SetValue(code)
		next, cmd := m.handleEnter()
		got := next.(model)
		if got.stage != s2FA || cmd != nil || got.errMsg == "" {
			t.Fatalf("code %q accepted: stage=%v cmd=%v err=%q", code, got.stage, cmd != nil, got.errMsg)
		}
	}
}

func TestDeployTickFocuses2FAOnlyWhenRequested(t *testing.T) {
	m := initialModel()
	m.stage = sDeploy
	next, _ := m.Update(deployTick{finished: true, needs2FA: true})
	got := next.(model)
	if got.stage != s2FA || !got.inputs[3].Focused() {
		t.Fatalf("stage=%v focused=%v, want focused 2FA", got.stage, got.inputs[3].Focused())
	}
}

func Test2FASubmissionRunsSubmitThenStatus(t *testing.T) {
	m := initialModel()
	var commands []*exec.Cmd
	runner := func(cmd *exec.Cmd) ([]byte, error) {
		commands = append(commands, cmd)
		return []byte("ok"), nil
	}

	result := m.run2FA("123456", runner)
	if result.failed || !result.ok {
		t.Fatalf("2FA result = %#v, want success", result)
	}
	if len(commands) != 2 {
		t.Fatalf("command count = %d, want submit and status", len(commands))
	}
	wantTail := [][]string{{"submit", "123456"}, {"status"}}
	for i, want := range wantTail {
		got := commands[i].Args[len(commands[i].Args)-len(want):]
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("command %d tail = %#v, want %#v", i, got, want)
		}
	}
}

func TestProgramExitCodeReflectsFinalFailure(t *testing.T) {
	if got := programExitCode(model{cancelled: true}, nil); got != 130 {
		t.Fatalf("cancelled model exit code = %d, want 130", got)
	}
	if got := programExitCode(model{failed: true}, nil); got != 1 {
		t.Fatalf("failed model exit code = %d, want 1", got)
	}
	if got := programExitCode(model{}, errors.New("run failed")); got != 1 {
		t.Fatalf("run error exit code = %d, want 1", got)
	}
	if got := programExitCode(model{}, nil); got != 0 {
		t.Fatalf("success exit code = %d, want 0", got)
	}
}

func populatedDeployModel() model {
	m := initialModel()
	m.inputs[0].SetValue("user@example.com")
	m.inputs[1].SetValue("password")
	m.inputs[2].SetValue("us")
	m.inputs[4].SetValue("token")
	return m
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], prefix) {
			return strings.TrimPrefix(env[i], prefix)
		}
	}
	return ""
}

var _ tea.Model = model{}
