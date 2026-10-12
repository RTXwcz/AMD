package media

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestDownloaderWSLArgsKeepURLAsOneArgument(t *testing.T) {
	url := "https://music.apple.com/us/album/a song/$()"
	got := wslScriptArgs("Ubuntu", `exec "$HOME/amd/dl.sh" "$@"`, "ez-am-downloader", url, "aac")
	want := []string{
		"-d", "Ubuntu", "--exec", "bash", "-lc",
		`exec "$HOME/amd/dl.sh" "$@"`,
		"ez-am-downloader", url, "aac",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wsl args = %#v, want %#v", got, want)
	}
}

func TestFormatsDoNotPromiseTwoIdenticalALACModes(t *testing.T) {
	if len(formats) != 3 {
		t.Fatalf("format count = %d, want 3", len(formats))
	}
	for _, f := range formats {
		if f.name == "Plain ALAC" || f.name == "Hi-Res ALAC 24/96" {
			t.Fatalf("stale format label %q remains", f.name)
		}
	}
	want := []struct {
		name string
		arg  string
	}{
		{"ALAC (source quality, up to 24/192)", "alac"},
		{"Dolby Atmos", "atmos"},
		{"AAC 256", "aac"},
	}
	if !reflect.DeepEqual(formats, want) {
		t.Fatalf("formats = %#v, want %#v", formats, want)
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

func TestDownloadCommandUsesWSLAndKeepsURLSeparate(t *testing.T) {
	t.Setenv("EZ_WSL_DISTRO", "Debian")
	url := "https://music.apple.com/us/album/a song/$()"
	cmd := newDownloadCommand(url, "atmos")
	want := append([]string{"wsl.exe"}, wslScriptArgs("Debian", downloadScriptShell, "ez-am-downloader", url, "atmos")...)
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("download args = %#v, want %#v", cmd.Args, want)
	}
	if strings.Contains(cmd.Args[6], url) {
		t.Fatalf("URL leaked into shell source %q", cmd.Args[6])
	}
}

func TestDownloadCommandPassesValidatedContainerOnlyThroughEnvironment(t *testing.T) {
	t.Setenv("EZ_WSL_DISTRO", "Debian")
	t.Setenv("WSLENV", "EXISTING/u:ez_download_container/w")
	name := "amd-downloader-123-456"
	cmd, err := newDownloadCommandContextWithContainer(
		context.Background(), "https://music.apple.com/us/album/test/1", "alac", name,
	)
	if err != nil {
		t.Fatal(err)
	}
	if cmd.WaitDelay <= 0 {
		t.Fatal("download command has no WaitDelay")
	}
	if got := testEnvValue(cmd.Env, downloadContainerEnv); got != name {
		t.Fatalf("container env = %q, want %q", got, name)
	}
	if got := testEnvValue(cmd.Env, "WSLENV"); got != "EXISTING/u:EZ_DOWNLOAD_CONTAINER/u" {
		t.Fatalf("WSLENV = %q", got)
	}
	if strings.Contains(strings.Join(cmd.Args, "\x00"), name) {
		t.Fatalf("container name leaked into primary command argv: %#v", cmd.Args)
	}
}

func TestCancelCommandKeepsContainerNameOutOfShellSource(t *testing.T) {
	name := "amd-downloader-123-456"
	cmd, err := newCancelDownloadCommandContext(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	if cmd.WaitDelay <= 0 {
		t.Fatal("cancel command has no WaitDelay")
	}
	if strings.Contains(cmd.Args[6], name) {
		t.Fatalf("container name leaked into shell source %q", cmd.Args[6])
	}
	if got := cmd.Args[len(cmd.Args)-1]; got != name {
		t.Fatalf("cancel command tail = %q, want %q", got, name)
	}
}

func TestFinalCancelCommandPollsForLateContainerCreation(t *testing.T) {
	cmd, err := newFinalCancelDownloadCommandContext(context.Background(), "amd-downloader-123-456")
	if err != nil {
		t.Fatal(err)
	}
	shellSource := cmd.Args[6]
	for _, want := range []string{"for attempt", "sleep 0.2", "docker rm -f"} {
		if !strings.Contains(shellSource, want) {
			t.Fatalf("final cleanup shell %q does not contain %q", shellSource, want)
		}
	}
}

func TestDownloadContainerNameValidation(t *testing.T) {
	for _, valid := range []string{"amd-downloader-1", "amd-downloader-123-abc.def"} {
		if !validDownloadContainerName(valid) {
			t.Fatalf("valid name rejected: %q", valid)
		}
	}
	for _, invalid := range []string{"", "amd-wrapper-login", "amd-downloader-../../bad", "amd-downloader-X", strings.Repeat("a", 100)} {
		if validDownloadContainerName(invalid) {
			t.Fatalf("invalid name accepted: %q", invalid)
		}
		if _, err := newCancelDownloadCommandContext(context.Background(), invalid); err == nil {
			t.Fatalf("cancel command accepted invalid name: %q", invalid)
		}
	}
	if generated := newDownloadContainerName(); !validDownloadContainerName(generated) {
		t.Fatalf("generated invalid name: %q", generated)
	}
}

func TestDownloadFailureIncludesProcessError(t *testing.T) {
	m := initialModel()
	m.url.SetValue("https://music.apple.com/us/album/test")
	runner := func(_ *exec.Cmd) ([]byte, error) {
		return []byte("wsl stderr"), errors.New("executable file not found")
	}

	result := m.runDownload(runner)
	if !result.failed {
		t.Fatal("download failure was reported as success")
	}
	for _, want := range []string{"wsl stderr", "executable file not found"} {
		if !strings.Contains(result.log, want) {
			t.Fatalf("download log %q does not include %q", result.log, want)
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

func TestCtrlCCancelsActiveDownload(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := initialModel()
	m.stage = sDownload
	m.cancel = cancel
	m.jobName = "amd-downloader-123-456"
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	got := next.(model)
	if cmd == nil || !got.cancelled {
		t.Fatalf("cancel update = %#v cmd=%v", got, cmd != nil)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("download context was not cancelled")
	}
}

func TestRepeatedCtrlCDoesNotStartDuplicateImmediateCleanup(t *testing.T) {
	m := initialModel()
	m.stage = sDownload
	m.cancelled = true
	m.cleanupStarted = true
	m.jobName = "amd-downloader-123-456"
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd != nil {
		t.Fatal("repeated Ctrl+C started duplicate immediate cleanup")
	}
}

func TestCancelledDownloadRunsFinalCleanupAfterChildStops(t *testing.T) {
	m := initialModel()
	m.stage = sDownload
	m.cancelled = true
	m.cleanupStarted = true
	m.runID = 7
	m.jobName = "amd-downloader-123-456"
	next, cmd := m.Update(processDoneMsg{runID: 7, jobIndex: 0, err: errors.New("signal: killed")})
	childStopped := next.(model)
	if cmd == nil || !childStopped.childStopped || !childStopped.finalCleanupStarted || childStopped.stage != sDownload {
		t.Fatalf("child completion = %#v cmd=%v", childStopped, cmd != nil)
	}

	finished, quit := childStopped.Update(cancelTick{runID: 7, jobIndex: 0, final: true})
	got := finished.(model)
	if quit == nil || got.stage != sDone || !got.finalCleanupDone || !got.cancelled || !got.failed {
		t.Fatalf("cancel completion = %#v cmd=%v", got, quit != nil)
	}
}

func TestCancelledDownloadRepeatsCleanupWhenImmediateCleanupFinishesFirst(t *testing.T) {
	m := initialModel()
	m.stage = sDownload
	m.cancelled = true
	m.cleanupStarted = true
	m.runID = 8
	m.jobName = "amd-downloader-123-456"
	next, cmd := m.Update(cancelTick{runID: 8, jobIndex: 0})
	cleanupFinished := next.(model)
	if cmd != nil || !cleanupFinished.cleanupDone || cleanupFinished.stage != sDownload {
		t.Fatalf("cleanup completion = %#v cmd=%v", cleanupFinished, cmd != nil)
	}

	child, finalCleanup := cleanupFinished.Update(processDoneMsg{runID: 8, jobIndex: 0, err: errors.New("signal: killed")})
	childStopped := child.(model)
	if finalCleanup == nil || !childStopped.childStopped || !childStopped.finalCleanupStarted || childStopped.stage != sDownload {
		t.Fatalf("post-child cleanup = %#v cmd=%v", childStopped, finalCleanup != nil)
	}

	finished, quit := childStopped.Update(cancelTick{runID: 8, jobIndex: 0, final: true})
	got := finished.(model)
	if quit == nil || got.stage != sDone || !got.finalCleanupDone || !got.failed {
		t.Fatalf("final cleanup = %#v cmd=%v", got, quit != nil)
	}
}

func TestChildCompletionPreservesCleanupDiagnostics(t *testing.T) {
	m := initialModel()
	m.stage = sDownload
	m.cancelled = true
	m.cleanupDone = true
	m.finalCleanupStarted = true
	m.dlLog = "cleanup warning"
	next, _ := m.Update(dlTick{finished: true, failed: true, log: "signal: killed"})
	got := next.(model)
	for _, want := range []string{"cleanup warning", "signal: killed"} {
		if !strings.Contains(got.dlLog, want) {
			t.Fatalf("download log %q does not include %q", got.dlLog, want)
		}
	}
}

func TestShellDownloaderOwnsAndCleansNamedContainer(t *testing.T) {
	script, err := os.ReadFile("../../dl.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	for _, want := range []string{
		"EZ_DOWNLOAD_CONTAINER", "--name", "trap", "docker rm -f",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("dl.sh does not contain %q", want)
		}
	}
}

func testEnvValue(env []string, key string) string {
	for i := len(env) - 1; i >= 0; i-- {
		name, value, found := strings.Cut(env[i], "=")
		if found && strings.EqualFold(name, key) {
			return value
		}
	}
	return ""
}
