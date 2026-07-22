package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ---------- styles (opencode-like: dark + cyan) ----------
var (
	primary = lipgloss.Color("#22d3ee") // cyan
	green   = lipgloss.Color("#34d399")
	yellow  = lipgloss.Color("#fbbf24")
	red     = lipgloss.Color("#f87171")
	dim     = lipgloss.Color("#64748b")

	titleStyle  = lipgloss.NewStyle().Foreground(primary).Bold(true).Padding(0, 1)
	stepStyle   = lipgloss.NewStyle().Foreground(primary).Bold(true)
	hintStyle   = lipgloss.NewStyle().Foreground(dim)
	okStyle     = lipgloss.NewStyle().Foreground(green)
	warnStyle   = lipgloss.NewStyle().Foreground(yellow)
	errStyle    = lipgloss.NewStyle().Foreground(red)
	panelStyle  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(primary).Padding(1, 2)
	inputStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#e2e8f0"))
	promptStyle = lipgloss.NewStyle().Foreground(primary)
)

// ---------- state ----------
type stage int

const (
	sWelcome stage = iota
	sCreds
	sToken
	sDeploy
	s2FA
	sDone
)

type model struct {
	stage      stage
	inputs     []textinput.Model
	focus      int
	baseDir    string
	deployLog  string
	done       bool
	failed     bool
	errMsg     string
	cancel     context.CancelFunc
	cancelled  bool
	submitting bool
}

func initialModel() model {
	m := model{stage: sWelcome, baseDir: "~/amd"}
	// inputs for creds: 0 appleid, 1 password, 2 storefront, 3 twofa, 4 token
	specs := []struct {
		label  string
		secret bool
		prompt string
	}{
		{"Apple ID email", false, "your Apple ID email"},
		{"Apple ID password", true, "your Apple ID password"},
		{"Storefront", false, "us / jp / hk ... (default us)"},
		{"2FA code", true, "6-digit code from trusted device"},
		{"media-user-token", true, "paste the token (see README)"},
	}
	for _, s := range specs {
		ti := textinput.New()
		ti.Placeholder = s.prompt
		ti.EchoMode = textinput.EchoNormal
		if s.secret {
			ti.EchoMode = textinput.EchoPassword
			ti.EchoCharacter = '•'
		}
		ti.CharLimit = 2000
		m.inputs = append(m.inputs, ti)
	}
	m.inputs[0].Focus()
	return m
}

// ---------- init ----------
func (m model) Init() tea.Cmd {
	return textinput.Blink
}

// ---------- update ----------
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			if m.stage == sWelcome || m.stage == sDone {
				return m, tea.Quit
			}
			m.clearSecrets()
			m.cancelled = true
			if m.cancel != nil {
				m.cancel()
			}
			if m.stage == sDeploy || m.submitting {
				return m, nil
			}
			return m, tea.Quit
		case "esc":
			if m.stage == sWelcome {
				return m, tea.Quit
			}
		case "enter":
			return m.handleEnter()
		case "tab", "shift+tab", "up", "down":
			if m.stage == sCreds {
				m.focus = (m.focus + 1) % 3
				for i := range m.inputs {
					if i == m.focus {
						m.inputs[i].Focus()
					} else {
						m.inputs[i].Blur()
					}
				}
			}
		}
	case deployTick:
		m.deployLog = redactDiagnosticText(msg.log)
		if msg.finished {
			m.releaseCommand()
			if m.cancelled {
				m.errMsg = cancellationSummary
				m.stage = sDone
				m.done = true
				m.failed = true
				return m, tea.Quit
			}
			if msg.failed {
				m.errMsg = deploymentFailureSummary
				m.stage = sDone
				m.done = true
				m.failed = true
			} else if msg.needs2FA {
				m.stage = s2FA
				m.focusInput(3)
			} else {
				m.stage = sDone
				m.done = true
			}
		}
		return m, nil
	case twofaTick:
		m.deployLog = appendLog(m.deployLog, redactDiagnosticText(msg.log))
		m.releaseCommand()
		m.submitting = false
		m.clearSecrets()
		m.stage = sDone
		m.done = true
		if m.cancelled {
			m.errMsg = cancellationSummary
			m.failed = true
			return m, tea.Quit
		}
		if !msg.ok {
			m.errMsg = twoFAFailureSummary
			m.failed = true
		}
		return m, nil
	}
	// route text input
	if m.stage == sCreds || m.stage == s2FA || m.stage == sToken {
		var cmd tea.Cmd
		m.inputs[m.focusOrToken()], cmd = m.inputs[m.focusOrToken()].Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m model) focusOrToken() int {
	switch m.stage {
	case sCreds:
		return m.focus
	case s2FA:
		return 3
	case sToken:
		return 4
	}
	return 0
}

func (m *model) focusInput(index int) {
	m.focus = index
	for i := range m.inputs {
		if i == index {
			m.inputs[i].Focus()
		} else {
			m.inputs[i].Blur()
		}
	}
}

func (m model) handleEnter() (tea.Model, tea.Cmd) {
	switch m.stage {
	case sWelcome:
		m.stage = sCreds
		m.focusInput(0)
		return m, nil
	case sCreds:
		// validate basics
		if strings.TrimSpace(m.inputs[0].Value()) == "" {
			m.errMsg = "Apple ID email required"
			return m, nil
		}
		if m.inputs[1].Value() == "" {
			m.errMsg = "Apple ID password required"
			return m, nil
		}
		m.errMsg = ""
		m.stage = sToken
		m.focusInput(4)
		return m, nil
	case s2FA:
		if m.submitting {
			return m, nil
		}
		code := strings.TrimSpace(m.inputs[3].Value())
		if !isSixDigitCode(code) {
			m.errMsg = "enter the 6-digit code"
			return m, nil
		}
		m.errMsg = ""
		ctx, cancel := context.WithCancel(context.Background())
		m.cancel = cancel
		m.cancelled = false
		m.submitting = true
		cmd := submit2FAWithContext(ctx, code)
		m.inputs[3].SetValue("")
		return m, cmd
	case sToken:
		tok := strings.TrimSpace(m.inputs[4].Value())
		if tok == "" {
			m.errMsg = "media-user-token required"
			return m, nil
		}
		m.errMsg = ""
		m.stage = sDeploy
		ctx, cancel := context.WithCancel(context.Background())
		m.cancel = cancel
		m.cancelled = false
		deployCmd := m.startDeployWithContext(ctx)
		m.clearSecrets()
		return m, deployCmd
	}
	return m, nil
}

// ---------- deploy ----------
const (
	setupScriptShell         = `script="$(wslpath -u "$1")"; exec bash "$script"`
	loginScriptShell         = `exec "$HOME/amd/login.sh" "$@"`
	need2FAMarker            = "STATE=NEED_2FA"
	commandWaitDelay         = 3 * time.Second
	deploymentFailureSummary = "Deployment did not complete. Review the diagnostics and verify Docker Desktop and wrapper status before retrying."
	twoFAFailureSummary      = "2FA login did not complete. Request a fresh code, then retry and verify wrapper status."
	cancellationSummary      = "Operation cancelled after the active child process stopped."
)

type commandRunner func(*exec.Cmd) ([]byte, error)

type deployTick struct {
	log      string
	finished bool
	failed   bool
	needs2FA bool
	err      string
}

func (m model) startDeploy() tea.Cmd {
	return m.startDeployWithContext(context.Background())
}

func (m model) startDeployWithContext(ctx context.Context) tea.Cmd {
	user := strings.TrimSpace(m.inputs[0].Value())
	pass := m.inputs[1].Value()
	token := strings.TrimSpace(m.inputs[4].Value())
	storefront := orDefault(m.inputs[2].Value(), "us")
	return func() tea.Msg {
		executable, err := os.Executable()
		if err != nil {
			return deployTick{finished: true, failed: true, err: fmt.Sprintf("locate executable: %v", err)}
		}
		setupPath, err := locateSetupScript(executable, "")
		if err != nil {
			return deployTick{finished: true, failed: true, err: err.Error()}
		}
		return runDeploymentWithCredentialsContext(ctx, setupPath, user, pass, token, storefront, combinedOutput)
	}
}

func (m model) runDeployment(setupPath string, run commandRunner) deployTick {
	return runDeploymentWithCredentials(
		setupPath,
		strings.TrimSpace(m.inputs[0].Value()),
		m.inputs[1].Value(),
		strings.TrimSpace(m.inputs[4].Value()),
		orDefault(m.inputs[2].Value(), "us"),
		run,
	)
}

func runDeploymentWithCredentials(setupPath, user, pass, token, storefront string, run commandRunner) deployTick {
	return runDeploymentWithCredentialsContext(
		context.Background(), setupPath, user, pass, token, storefront, run,
	)
}

func runDeploymentWithCredentialsContext(
	ctx context.Context,
	setupPath, user, pass, token, storefront string,
	run commandRunner,
) deployTick {
	var log string
	secrets := []string{user, pass, token}

	setupOut, err := run(newSetupCommandContext(ctx, setupPath, user, pass, token, storefront))
	log = appendLog(log, redactSecrets(string(setupOut), secrets...))
	if err != nil {
		return deployFailure(log, "setup", err, secrets...)
	}

	startOut, err := run(newLoginCommandContext(ctx, "start"))
	log = appendLog(log, redactSecrets(string(startOut), secrets...))
	if err != nil {
		return deployFailure(log, "login start", err, secrets...)
	}
	if strings.Contains(string(startOut), need2FAMarker) {
		return deployTick{log: log, finished: true, needs2FA: true}
	}

	statusOut, err := run(newLoginCommandContext(ctx, "status"))
	log = appendLog(log, redactSecrets(string(statusOut), secrets...))
	if err != nil {
		return deployFailure(log, "login status", err, secrets...)
	}
	return deployTick{log: log, finished: true}
}

func deployFailure(log, step string, err error, secrets ...string) deployTick {
	message := redactSecrets(fmt.Sprintf("%s failed: %v", step, err), secrets...)
	return deployTick{
		log:      appendLog(log, message),
		finished: true,
		failed:   true,
		err:      message,
	}
}

type twofaTick struct {
	ok     bool
	failed bool
	log    string
	err    string
}

func (m model) submit2FA(code string) tea.Cmd {
	return submit2FAWithContext(context.Background(), code)
}

func submit2FAWithContext(ctx context.Context, code string) tea.Cmd {
	return func() tea.Msg {
		return run2FAWithContext(ctx, code, combinedOutput, code)
	}
}

func (m model) run2FA(code string, run commandRunner) twofaTick {
	return run2FAWithContext(context.Background(), code, run,
		m.inputs[0].Value(), m.inputs[1].Value(), m.inputs[4].Value(), code,
	)
}

func run2FAWithContext(ctx context.Context, code string, run commandRunner, secrets ...string) twofaTick {
	var log string
	secrets = append(secrets, code)
	submitOut, err := run(newLoginCommandContext(ctx, "submit", code))
	log = appendLog(log, redactSecrets(string(submitOut), secrets...))
	if err != nil {
		message := redactSecrets(fmt.Sprintf("2FA submit failed: %v", err), secrets...)
		return twofaTick{failed: true, log: appendLog(log, message), err: message}
	}

	statusOut, err := run(newLoginCommandContext(ctx, "status"))
	log = appendLog(log, redactSecrets(string(statusOut), secrets...))
	if err != nil {
		message := redactSecrets(fmt.Sprintf("login status after 2FA failed: %v", err), secrets...)
		return twofaTick{failed: true, log: appendLog(log, message), err: message}
	}
	return twofaTick{ok: true, log: log}
}

func (m *model) clearSecrets() {
	for _, index := range []int{1, 3, 4} {
		m.inputs[index].SetValue("")
	}
}

func (m *model) releaseCommand() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.submitting = false
}

func newSetupCommand(scriptPath, user, pass, token, storefront string) *exec.Cmd {
	return newSetupCommandContext(context.Background(), scriptPath, user, pass, token, storefront)
}

func newSetupCommandContext(ctx context.Context, scriptPath, user, pass, token, storefront string) *exec.Cmd {
	args := wslScriptArgs(wslDistro(), setupScriptShell, "ez-am-setup", scriptPath)
	cmd := exec.CommandContext(ctx, "wsl.exe", args...)
	cmd.WaitDelay = commandWaitDelay
	env := os.Environ()
	wslEnv := mergedWSLEnv(environmentValue(env, "WSLENV"), "EZ_USER", "EZ_PASS", "EZ_TOKEN", "EZ_STOREFRONT")
	for _, item := range []struct{ key, value string }{
		{"EZ_USER", user},
		{"EZ_PASS", pass},
		{"EZ_TOKEN", token},
		{"EZ_STOREFRONT", storefront},
		{"WSLENV", wslEnv},
	} {
		env = setEnvironmentValue(env, item.key, item.value)
	}
	cmd.Env = env
	return cmd
}

func newLoginCommand(args ...string) *exec.Cmd {
	return newLoginCommandContext(context.Background(), args...)
}

func newLoginCommandContext(ctx context.Context, args ...string) *exec.Cmd {
	wslArgs := wslScriptArgs(wslDistro(), loginScriptShell, "ez-am-login", args...)
	cmd := exec.CommandContext(ctx, "wsl.exe", wslArgs...)
	cmd.WaitDelay = commandWaitDelay
	return cmd
}

func locateSetupScript(executablePath, _ string) (string, error) {
	executableDir := filepath.Dir(executablePath)
	candidates := []string{
		filepath.Join(executableDir, "setup.sh"),
		filepath.Join(filepath.Dir(executableDir), "setup.sh"),
	}
	seen := make(map[string]bool, len(candidates))
	checked := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		key := strings.ToLower(candidate)
		if seen[key] {
			continue
		}
		seen[key] = true
		checked = append(checked, candidate)
		info, err := os.Lstat(candidate)
		if err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("setup.sh not found; checked %s", strings.Join(checked, ", "))
}

func combinedOutput(cmd *exec.Cmd) ([]byte, error) {
	return cmd.CombinedOutput()
}

func appendLog(current, next string) string {
	next = strings.TrimSpace(next)
	if next == "" {
		return current
	}
	if current == "" {
		return next
	}
	return current + "\n" + next
}

// ---------- view ----------
func (m model) View() string {
	switch m.stage {
	case sWelcome:
		return panelStyle.Render(
			titleStyle.Render("ez-am-deploy") + "\n\n" +
				"Deploy Apple Music lossless downloader on WSL2 + Docker.\n\n" +
				hintStyle.Render("Prerequisites: WSL2, Docker Desktop, Apple Music subscription, GitHub access.") + "\n\n" +
				promptStyle.Render("Press ENTER to start · CTRL+C to quit"),
		)
	case sCreds:
		var b strings.Builder
		b.WriteString(titleStyle.Render("Step 1/3 — Account") + "\n\n")
		labels := []string{"Apple ID email", "Apple ID password", "Storefront (default us)"}
		for i, lb := range labels {
			focus := m.focus == i
			prefix := "  "
			if focus {
				prefix = "› "
			}
			b.WriteString(fmt.Sprintf("%s%s\n%s\n\n", prefix, hintStyle.Render(lb), inputStyle.Render(m.inputs[i].View())))
		}
		b.WriteString(hintStyle.Render("TAB/↑↓ to move · ENTER to continue"))
		if m.errMsg != "" {
			b.WriteString("\n" + errStyle.Render(m.errMsg))
		}
		return panelStyle.Render(b.String())
	case sDeploy:
		return panelStyle.Render(
			titleStyle.Render("Step 3/3 — Deploying") + "\n\n" +
				hintStyle.Render("Checking Docker, preparing images, and writing config (this can take minutes)") + "\n\n" +
				okStyle.Render("● running") + "\n" +
				hintStyle.Render("Logs:\n") + m.deployLog,
		)
	case s2FA:
		if m.submitting {
			return panelStyle.Render(
				titleStyle.Render("Additional verification — 2FA") + "\n\n" +
					okStyle.Render("Submitting the code and checking wrapper health") + "\n\n" +
					hintStyle.Render("CTRL+C cancels after the active child process stops"),
			)
		}
		var b strings.Builder
		b.WriteString(titleStyle.Render("Additional verification — 2FA") + "\n\n")
		b.WriteString(hintStyle.Render("wrapper is waiting for your 6-digit Apple code.") + "\n")
		b.WriteString(hintStyle.Render("Check your trusted device / phone, then type it below.\n\n"))
		b.WriteString(inputStyle.Render(m.inputs[3].View()) + "\n")
		b.WriteString(hintStyle.Render("ENTER to submit"))
		if m.errMsg != "" {
			b.WriteString("\n" + errStyle.Render(m.errMsg))
		}
		return panelStyle.Render(b.String())
	case sToken:
		var b strings.Builder
		b.WriteString(titleStyle.Render("Step 2/3 — media-user-token") + "\n\n")
		b.WriteString(hintStyle.Render("Get it from music.apple.com DevTools:"))
		b.WriteString(hintStyle.Render("\n  A) Application → Storage → Cookies → media-user-token"))
		b.WriteString(hintStyle.Render("\n  B) Network → refresh → check several requests' Cookie lines"))
		b.WriteString(hintStyle.Render("\n     for 'media-user-token=' (not always in the first request)\n\n"))
		b.WriteString(inputStyle.Render(m.inputs[4].View()) + "\n")
		b.WriteString(hintStyle.Render("ENTER to deploy"))
		if m.errMsg != "" {
			b.WriteString("\n" + errStyle.Render(m.errMsg))
		}
		return panelStyle.Render(b.String())
	case sDone:
		if m.failed {
			diagnostics := redactDiagnosticText(m.deployLog)
			if diagnostics == "" {
				diagnostics = "(no diagnostic output)"
			}
			return panelStyle.Render(
				errStyle.Render("Deployment failed:\n"+m.errMsg) + "\n\n" +
					hintStyle.Render("Diagnostics:\n"+diagnostics) + "\n\n" +
					promptStyle.Render("Press CTRL+C to exit"),
			)
		}
		return panelStyle.Render(
			okStyle.Render("✓ Deployment complete!") + "\n\n" +
				"Run ez-am-downloader.exe, or use the shell engine:\n" +
				inputStyle.Render("  ~/amd/dl.sh '<apple-music-url>' '<alac|atmos|aac>'") + "\n\n" +
				warnStyle.Render("Reminder: start Docker Desktop before downloading.") + "\n\n" +
				promptStyle.Render("Press CTRL+C to exit"),
		)
	}
	return ""
}

// ---------- helpers ----------
func orDefault(s, d string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return d
	}
	return s
}

func wslScriptArgs(distro, shellSource, argv0 string, args ...string) []string {
	if strings.TrimSpace(distro) == "" {
		distro = "Ubuntu"
	}
	result := []string{"-d", distro, "--exec", "bash", "-lc", shellSource, argv0}
	return append(result, args...)
}

func wslDistro() string {
	return orDefault(os.Getenv("EZ_WSL_DISTRO"), "Ubuntu")
}

func isSixDigitCode(code string) bool {
	if len(code) != 6 {
		return false
	}
	for i := 0; i < len(code); i++ {
		if code[i] < '0' || code[i] > '9' {
			return false
		}
	}
	return true
}

func environmentValue(env []string, key string) string {
	for i := len(env) - 1; i >= 0; i-- {
		name, value, found := strings.Cut(env[i], "=")
		if found && strings.EqualFold(name, key) {
			return value
		}
	}
	return ""
}

func setEnvironmentValue(env []string, key, value string) []string {
	result := make([]string, 0, len(env)+1)
	for _, entry := range env {
		name, _, found := strings.Cut(entry, "=")
		if found && strings.EqualFold(name, key) {
			continue
		}
		result = append(result, entry)
	}
	return append(result, key+"="+value)
}

func mergedWSLEnv(existing string, names ...string) string {
	conflicts := make(map[string]bool, len(names))
	canonicalNames := make([]string, 0, len(names))
	for _, name := range names {
		canonical := strings.ToUpper(name)
		if canonical == "" || conflicts[canonical] {
			continue
		}
		conflicts[canonical] = true
		canonicalNames = append(canonicalNames, canonical+"/u")
	}

	parts := make([]string, 0, len(canonicalNames)+1)
	for _, part := range strings.Split(existing, ":") {
		if part == "" {
			continue
		}
		name, _, _ := strings.Cut(part, "/")
		if conflicts[strings.ToUpper(name)] {
			continue
		}
		parts = append(parts, part)
	}
	parts = append(parts, canonicalNames...)
	return strings.Join(parts, ":")
}

var diagnosticSecretPattern = regexp.MustCompile(`(?i)\b(password|token)(?:[-_][A-Za-z0-9.+/=:-]+|[=:][ \t]*[^\s]+)`)

func redactSecrets(text string, secrets ...string) string {
	ordered := make([]string, 0, len(secrets))
	seen := make(map[string]struct{}, len(secrets))
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		if _, ok := seen[secret]; ok {
			continue
		}
		seen[secret] = struct{}{}
		ordered = append(ordered, secret)
	}
	sort.SliceStable(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
	for _, secret := range ordered {
		text = strings.ReplaceAll(text, secret, "[REDACTED]")
	}
	return text
}

func redactDiagnosticText(text string) string {
	return diagnosticSecretPattern.ReplaceAllString(text, "$1=[REDACTED]")
}

func programExitCode(final tea.Model, runErr error) int {
	switch final := final.(type) {
	case model:
		if final.cancelled {
			return 130
		}
	case *model:
		if final != nil && final.cancelled {
			return 130
		}
	}
	if runErr != nil {
		return 1
	}
	switch final := final.(type) {
	case model:
		if final.failed {
			return 1
		}
	case *model:
		if final != nil && final.failed {
			return 1
		}
	}
	return 0
}

func main() {
	p := tea.NewProgram(initialModel())
	final, err := p.Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
	}
	if code := programExitCode(final, err); code != 0 {
		os.Exit(code)
	}
}
