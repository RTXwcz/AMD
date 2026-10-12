// Package control owns the pre-flight experience of the unified ez-am
// application: it runs the doctor, guides the user through the fixes it can
// perform itself, re-checks, and only then hands control to the media engine.
package control

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"ez-am/internal/doctor"
	"ez-am/internal/platform"
	"ez-am/internal/platform/wsl"
	"ez-am/internal/runtime"
)

// Options configures one supervisor run.
type Options struct {
	Runner platform.CommandRunner
	Distro string
	Base   string
	// ReadyPause is how long a green report stays on screen before the media
	// engine takes over. Zero disables the pause.
	ReadyPause time.Duration
}

// Result tells the caller what to do next.
type Result struct {
	LaunchMedia bool
	Distro      string
	Base        string
}

// Run shows the doctor, drives repair actions, and returns once the runtime is
// ready (LaunchMedia) or the user quit.
func Run(ctx context.Context, opts Options) (Result, error) {
	if opts.Runner == nil {
		return Result{}, fmt.Errorf("control: a command runner is required")
	}
	if opts.ReadyPause == 0 {
		opts.ReadyPause = 900 * time.Millisecond
	}
	m := newModel(ctx, opts)
	program := tea.NewProgram(m)
	final, err := program.Run()
	if err != nil {
		return Result{}, err
	}
	if typed, ok := final.(model); ok {
		return typed.result, typed.err
	}
	return Result{}, nil
}

// ---------- styles ----------

var (
	cyan  = lipgloss.Color("#22d3ee")
	green = lipgloss.Color("#34d399")
	amber = lipgloss.Color("#fbbf24")
	rose  = lipgloss.Color("#f87171")
	slate = lipgloss.Color("#64748b")

	titleStyle = lipgloss.NewStyle().Foreground(cyan).Bold(true).Padding(0, 1)
	hintStyle  = lipgloss.NewStyle().Foreground(slate)
	okStyle    = lipgloss.NewStyle().Foreground(green)
	warnStyle  = lipgloss.NewStyle().Foreground(amber)
	failStyle  = lipgloss.NewStyle().Foreground(rose)
	panelStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cyan).Padding(1, 2)
	fieldStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#e2e8f0"))
	labelStyle = lipgloss.NewStyle().Foreground(cyan)
)

// ---------- phases ----------

type phase int

const (
	phaseChecking phase = iota
	phaseReport
	phaseFixes
	phaseDistro
	phaseCreds
	phaseRunning
	phaseLogin
)

type actionKind int

const (
	actionRerun actionKind = iota
	actionInstallRuntime
	actionCollectCreds
	actionLogin
	actionChooseDistro
	actionInstruction
	actionQuit
)

type action struct {
	kind   actionKind
	title  string
	detail string
}

// ---------- messages ----------

type spinnerMsg time.Time
type reportMsg struct {
	report doctor.Report
	err    error
}
type actionDoneMsg struct {
	log string
	err error
}
type distroListMsg struct {
	names []string
	err   error
}
type readyPauseMsg struct{}

// ---------- model ----------

type model struct {
	ctx      context.Context
	opts     Options
	phase    phase
	spin     int
	report   doctor.Report
	actions  []action
	cursor   int
	choices  []string
	inputs   []textinput.Model
	focus    int
	log      string
	busy     bool
	err      error
	result   Result
	quitting bool
}

type credentials struct {
	user       string
	password   string
	storefront string
	token      string
}

func newModel(ctx context.Context, opts Options) model {
	m := model{ctx: ctx, opts: opts, phase: phaseChecking}
	specs := []struct {
		label  string
		prompt string
		secret bool
	}{
		{"Apple ID", "you@example.com", false},
		{"Apple ID password", "", true},
		{"Storefront", "us", false},
		{"media-user-token", "paste from music.apple.com DevTools", true},
	}
	for _, spec := range specs {
		input := textinput.New()
		input.Placeholder = spec.prompt
		input.CharLimit = 2000
		if spec.secret {
			input.EchoMode = textinput.EchoPassword
			input.EchoCharacter = '•'
		}
		m.inputs = append(m.inputs, input)
	}
	return m
}

func (m model) Init() tea.Cmd {
	return tea.Batch(spinnerTick(), m.doctorCmd())
}

func spinnerTick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return spinnerMsg(time.Now()) })
}

func (m model) doctorCmd() tea.Cmd {
	opts := doctor.Inputs{Runner: m.opts.Runner, Distro: m.opts.Distro, Base: m.opts.Base}
	ctx := m.ctx
	return func() tea.Msg {
		return reportMsg{report: doctor.Run(ctx, opts)}
	}
}

// ---------- update ----------

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinnerMsg:
		if m.phase == phaseChecking || m.busy {
			m.spin++
			return m, spinnerTick()
		}
		return m, nil
	case reportMsg:
		m.busy = false
		if msg.err != nil {
			m.err = msg.err
			m.quitting = true
			return m, tea.Quit
		}
		m.report = msg.report
		if m.report.Distro != "" {
			m.opts.Distro = m.report.Distro
		}
		if m.report.Base != "" {
			m.opts.Base = m.report.Base
		}
		m.phase = phaseReport
		m.log = ""
		if m.report.Ready() {
			m.result = Result{LaunchMedia: true, Distro: m.opts.Distro, Base: m.opts.Base}
			if m.opts.ReadyPause <= 0 {
				return m, tea.Quit
			}
			pause := m.opts.ReadyPause
			return m, tea.Tick(pause, func(time.Time) tea.Msg { return readyPauseMsg{} })
		}
		m.actions = actionsFor(m.report)
		m.cursor = 0
		return m, nil
	case readyPauseMsg:
		return m, tea.Quit
	case distroListMsg:
		m.busy = false
		if msg.err != nil {
			m.log = "could not list distributions: " + msg.err.Error()
			return m, nil
		}
		m.choices = msg.names
		m.cursor = 0
		m.phase = phaseDistro
		return m, nil
	case actionDoneMsg:
		m.busy = false
		m.log = msg.log
		if msg.err != nil {
			m.log = strings.TrimSpace(m.log + "\n" + msg.err.Error())
			m.phase = phaseFixes
			return m, nil
		}
		m.phase = phaseChecking
		return m, m.doctorCmd()
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		m.clearSecrets()
		m.quitting = true
		return m, tea.Quit
	}
	switch m.phase {
	case phaseChecking:
		if m.busy {
			return m, nil
		}
		if msg.String() == "q" {
			m.quitting = true
			return m, tea.Quit
		}
	case phaseReport:
		switch msg.String() {
		case "q":
			m.quitting = true
			return m, tea.Quit
		case "r":
			m.phase = phaseChecking
			m.busy = true
			return m, tea.Batch(spinnerTick(), m.doctorCmd())
		default:
			m.phase = phaseFixes
			m.cursor = 0
		}
	case phaseFixes:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor+1 < len(m.actions) {
				m.cursor++
			}
		case "r":
			m.phase = phaseChecking
			m.busy = true
			return m, tea.Batch(spinnerTick(), m.doctorCmd())
		case "enter":
			return m.runAction()
		}
	case phaseDistro:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor+1 < len(m.choices) {
				m.cursor++
			}
		case "esc":
			m.phase = phaseFixes
		case "enter":
			if m.cursor < len(m.choices) {
				m.opts.Distro = m.choices[m.cursor]
				m.phase = phaseChecking
				m.busy = true
				return m, tea.Batch(spinnerTick(), m.doctorCmd())
			}
		}
	case phaseCreds:
		switch msg.String() {
		case "esc":
			m.clearSecrets()
			m.phase = phaseFixes
		case "tab", "shift+tab", "up", "down":
			m.focus = (m.focus + 1) % len(m.inputs)
			m.refocus()
		case "enter":
			if m.focus+1 < len(m.inputs) {
				m.focus++
				m.refocus()
				return m, nil
			}
			return m.startSetup()
		}
		inputs, cmd := m.updateInputs(msg)
		m.inputs = inputs
		return m, cmd
	case phaseLogin:
		if msg.String() == "enter" {
			return m.submitCode()
		}
		inputs, cmd := m.updateInputs(msg)
		m.inputs = inputs
		return m, cmd
	case phaseRunning:
		if msg.String() == "q" && !m.busy {
			m.phase = phaseFixes
		}
	}
	return m, nil
}

func (m model) updateInputs(msg tea.Msg) ([]textinput.Model, tea.Cmd) {
	var cmd tea.Cmd
	for i := range m.inputs {
		m.inputs[i], cmd = m.inputs[i].Update(msg)
	}
	return m.inputs, cmd
}

func (m *model) refocus() {
	for i := range m.inputs {
		if i == m.focus {
			m.inputs[i].Focus()
		} else {
			m.inputs[i].Blur()
		}
	}
}

func (m *model) clearSecrets() {
	for i := range m.inputs {
		m.inputs[i].SetValue("")
	}
}

func (m model) runAction() (tea.Model, tea.Cmd) {
	if m.cursor >= len(m.actions) {
		return m, nil
	}
	chosen := m.actions[m.cursor]
	distro, base := m.opts.Distro, m.opts.Base
	runner := m.opts.Runner
	ctx := m.ctx
	switch chosen.kind {
	case actionQuit:
		m.quitting = true
		return m, tea.Quit
	case actionRerun:
		m.phase = phaseChecking
		m.busy = true
		return m, tea.Batch(spinnerTick(), m.doctorCmd())
	case actionChooseDistro:
		m.busy = true
		return m, func() tea.Msg {
			result, err := runner.Run(ctx, platform.CommandRequest{Name: "wsl.exe", Args: []string{"-l", "-q"}})
			if err != nil {
				return distroListMsg{err: err}
			}
			decoded, err := wsl.DecodeConsoleOutput(result.Stdout)
			if err != nil {
				return distroListMsg{err: err}
			}
			parsed, err := wsl.ParseDistributionNames([]byte(decoded))
			if err != nil {
				return distroListMsg{err: err}
			}
			selectable := wsl.SelectableDistributions(parsed)
			names := make([]string, 0, len(selectable))
			for _, item := range selectable {
				names = append(names, item.Name)
			}
			return distroListMsg{names: names}
		}
	case actionInstallRuntime:
		m.busy = true
		m.phase = phaseRunning
		m.log = "installing the bundled runtime…"
		home := m.report.Distribution.Home
		legacy := m.report.Distribution.LegacyBase
		explicit := m.opts.Base
		return m, func() tea.Msg {
			layout, err := resolveLayout(home, legacy, explicit)
			if err != nil {
				return actionDoneMsg{err: err}
			}
			assets, err := runtime.EmbeddedRuntime()
			if err != nil {
				return actionDoneMsg{err: err}
			}
			requests, err := runtime.BuildInstallRequests(distro, layout, assets)
			if err != nil {
				return actionDoneMsg{err: err}
			}
			var log strings.Builder
			fmt.Fprintf(&log, "runtime base: %s\n", layout.Base)
			for _, request := range requests {
				execResult, runErr := runner.Run(ctx, request)
				if runErr != nil {
					return actionDoneMsg{log: log.String(), err: runErr}
				}
				if execResult.Code != 0 {
					return actionDoneMsg{log: log.String(), err: fmt.Errorf("install step %q exited with code %d: %s", request.Args[len(request.Args)-1], execResult.Code, firstLine(string(execResult.Stderr)))}
				}
			}
			log.WriteString("runtime files installed and verified.")
			return actionDoneMsg{log: log.String()}
		}
	case actionCollectCreds:
		m.phase = phaseCreds
		m.focus = 0
		m.refocus()
		return m, textinput.Blink
	case actionLogin:
		m.busy = true
		m.phase = phaseRunning
		m.log = "starting the Apple authentication request…"
		return m, func() tea.Msg {
			result, err := runner.Run(ctx, platform.CommandRequest{
				Name: "wsl.exe",
				Args: []string{"-d", distro, "--exec", "bash", base + "/login.sh", "start"},
			})
			if err != nil {
				return actionDoneMsg{err: err}
			}
			output := strings.TrimSpace(string(result.Stdout) + "\n" + string(result.Stderr))
			if result.Code != 0 {
				return actionDoneMsg{log: output, err: fmt.Errorf("login.sh start exited with code %d", result.Code)}
			}
			return actionDoneMsg{log: output + "\n\nEnter the 6-digit code Apple sent you."}
		}
	case actionInstruction:
		m.phase = phaseRunning
		m.log = chosen.detail
		return m, nil
	}
	return m, nil
}

func resolveLayout(home string, legacy bool, explicitBase string) (runtime.RuntimeLayout, error) {
	if strings.TrimSpace(home) == "" {
		return runtime.RuntimeLayout{}, fmt.Errorf("the distribution home directory is unknown")
	}
	layout := runtime.ResolveRuntimeLayout(home, runtime.XDGDirs{}, legacy)
	if base := strings.TrimSpace(explicitBase); base != "" {
		layout.Base = strings.TrimSuffix(base, "/")
		layout.Config = layout.Base + "/config"
		layout.Data = layout.Base
		layout.Downloads = layout.Base + "/downloads"
	}
	return layout, nil
}

func (m model) startSetup() (tea.Model, tea.Cmd) {
	creds := credentials{
		user:       strings.TrimSpace(m.inputs[0].Value()),
		password:   m.inputs[1].Value(),
		storefront: strings.TrimSpace(m.inputs[2].Value()),
		token:      strings.TrimSpace(m.inputs[3].Value()),
	}
	if creds.storefront == "" {
		creds.storefront = "us"
	}
	if creds.user == "" || creds.password == "" || creds.token == "" {
		m.log = "Apple ID, password and media-user-token are all required."
		return m, nil
	}
	distro, base := m.opts.Distro, m.opts.Base
	runner, ctx := m.opts.Runner, m.ctx
	m.busy = true
	m.phase = phaseRunning
	m.log = "writing configuration and preparing images (this can take minutes)…"
	return m, func() tea.Msg {
		env := []string{
			"EZ_USER=" + creds.user,
			"EZ_PASS=" + creds.password,
			"EZ_TOKEN=" + creds.token,
			"EZ_STOREFRONT=" + creds.storefront,
			"WSLENV=EZ_USER/u:EZ_PASS/u:EZ_TOKEN/u:EZ_STOREFRONT/u",
		}
		result, err := runner.Run(ctx, platform.CommandRequest{
			Name: "wsl.exe",
			Args: []string{"-d", distro, "--exec", "bash", base + "/setup.sh"},
			Env:  env,
		})
		output := redact(strings.TrimSpace(string(result.Stdout)+"\n"+string(result.Stderr)), creds.password, creds.token, creds.user)
		if err != nil {
			return actionDoneMsg{log: output, err: err}
		}
		if result.Code != 0 {
			return actionDoneMsg{log: output, err: fmt.Errorf("setup.sh exited with code %d", result.Code)}
		}
		return actionDoneMsg{log: output}
	}
}

func (m model) submitCode() (tea.Model, tea.Cmd) {
	code := strings.TrimSpace(m.inputs[0].Value())
	if !isSixDigits(code) {
		m.log = "the code must be exactly six digits."
		return m, nil
	}
	m.inputs[0].SetValue("")
	distro, base := m.opts.Distro, m.opts.Base
	runner, ctx := m.opts.Runner, m.ctx
	m.busy = true
	m.phase = phaseRunning
	m.log = "submitting the code and waiting for the wrapper to become healthy…"
	return m, func() tea.Msg {
		result, err := runner.Run(ctx, platform.CommandRequest{
			Name: "wsl.exe",
			Args: []string{"-d", distro, "--exec", "bash", base + "/login.sh", "submit", code},
		})
		output := strings.TrimSpace(string(result.Stdout) + "\n" + string(result.Stderr))
		if err != nil {
			return actionDoneMsg{log: output, err: err}
		}
		if result.Code != 0 {
			return actionDoneMsg{log: output, err: fmt.Errorf("login.sh submit exited with code %d", result.Code)}
		}
		return actionDoneMsg{log: output}
	}
}

// ---------- helpers ----------

func actionsFor(report doctor.Report) []action {
	var actions []action
	seen := map[string]bool{}
	for _, check := range report.Failed() {
		if check.Action == "" || seen[check.Action] {
			continue
		}
		seen[check.Action] = true
		switch check.Action {
		case doctor.ActionInstallRuntime:
			actions = append(actions, action{kind: actionInstallRuntime, title: "Install the bundled runtime files", detail: check.Detail})
		case doctor.ActionCollectCreds:
			actions = append(actions, action{kind: actionCollectCreds, title: "Enter Apple ID, media-user-token and deploy", detail: check.Detail})
		case doctor.ActionLogin:
			actions = append(actions, action{kind: actionLogin, title: "Log in to Apple (2FA)", detail: check.Detail})
		case doctor.ActionChooseDistro:
			actions = append(actions, action{kind: actionChooseDistro, title: "Choose a WSL distribution", detail: check.Detail})
		case doctor.ActionStartDocker:
			actions = append(actions, action{
				kind:   actionInstruction,
				title:  "Start Docker, then re-check",
				detail: "Start Docker Desktop, or run `sudo service docker start` inside the distribution, then press R to re-run the doctor.",
			})
		case doctor.ActionInstallBuildx:
			actions = append(actions, action{
				kind:   actionInstruction,
				title:  "Install the docker buildx plugin, then re-check",
				detail: "Install the docker-buildx package, or download the docker-buildx binary into ~/.docker/cli-plugins/. Only needed once, to build the wrapper image.",
			})
		}
	}
	actions = append(actions, action{kind: actionRerun, title: "Re-run the doctor"})
	actions = append(actions, action{kind: actionQuit, title: "Quit"})
	return actions
}

func firstLine(text string) string {
	text = strings.TrimSpace(text)
	if index := strings.IndexAny(text, "\r\n"); index >= 0 {
		text = text[:index]
	}
	if len(text) > 120 {
		text = text[:120] + "…"
	}
	return text
}

// isSixDigits reports whether the value is exactly six ASCII digits.
func isSixDigits(value string) bool {
	if len(value) != 6 {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func redact(text string, secrets ...string) string {
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		text = strings.ReplaceAll(text, secret, "[REDACTED]")
	}
	return text
}

// ---------- view ----------

func (m model) View() string {
	var builder strings.Builder
	builder.WriteString(titleStyle.Render("ez-am"))
	if m.opts.Distro != "" {
		builder.WriteString(hintStyle.Render("  distribution: " + m.opts.Distro))
	}
	builder.WriteString("\n\n")

	switch m.phase {
	case phaseChecking:
		builder.WriteString(panelStyle.Render(
			labelStyle.Render("Environment check") + "\n\n" +
				hintStyle.Render(spinnerFrames[m.spin%len(spinnerFrames)]+" probing WSL, Docker, runtime files, credentials, images and the wrapper session…") + "\n\n" +
				hintStyle.Render("q quits"),
		))
		return builder.String()
	case phaseReport, phaseFixes, phaseRunning:
		builder.WriteString(m.renderReport())
		if m.log != "" {
			builder.WriteString("\n" + panelStyle.Render(labelStyle.Render("Details")+"\n\n"+boundedLog(m.log)))
		}
		if m.phase == phaseReport {
			builder.WriteString("\n" + hintStyle.Render("Press ENTER to fix the issues · R re-run · Q quit"))
		}
		if m.phase == phaseFixes {
			builder.WriteString("\n" + m.renderActions())
		}
		if m.phase == phaseRunning {
			if m.busy {
				builder.WriteString("\n" + hintStyle.Render(spinnerFrames[m.spin%len(spinnerFrames)]+" working…"))
			} else {
				builder.WriteString("\n" + hintStyle.Render("Press ENTER or R to re-run the doctor · Q quit"))
			}
		}
		return builder.String()
	case phaseDistro:
		var choices strings.Builder
		for index, name := range m.choices {
			cursor := "  "
			if index == m.cursor {
				cursor = "› "
			}
			choices.WriteString(cursor + fieldStyle.Render(name) + "\n")
		}
		builder.WriteString(panelStyle.Render(labelStyle.Render("Choose a WSL distribution") + "\n\n" + choices.String() + "\n" + hintStyle.Render("ENTER selects · ESC goes back")))
		return builder.String()
	case phaseCreds:
		var form strings.Builder
		labels := []string{"Apple ID email", "Apple ID password", "Storefront (default us)", "media-user-token"}
		for index, label := range labels {
			prefix := "  "
			if index == m.focus {
				prefix = "› "
			}
			form.WriteString(prefix + hintStyle.Render(label) + "\n" + fieldStyle.Render(m.inputs[index].View()) + "\n\n")
		}
		builder.WriteString(panelStyle.Render(labelStyle.Render("Apple account") + "\n\n" + form.String() + hintStyle.Render("TAB/↑↓ moves · ENTER continues · ESC cancels")))
		if m.log != "" {
			builder.WriteString("\n" + m.log)
		}
		return builder.String()
	case phaseLogin:
		builder.WriteString(panelStyle.Render(
			labelStyle.Render("Additional verification — 2FA") + "\n\n" +
				hintStyle.Render("Apple sent a 6-digit code to your trusted device.") + "\n\n" +
				fieldStyle.Render(m.inputs[0].View()) + "\n\n" +
				hintStyle.Render("ENTER submits"),
		))
		if m.log != "" {
			builder.WriteString("\n" + boundedLog(m.log))
		}
		return builder.String()
	}
	return builder.String()
}

func (m model) renderReport() string {
	var rows strings.Builder
	for _, check := range m.report.Checks {
		mark, style := "✓", okStyle
		switch check.Status {
		case doctor.StatusWarn:
			mark, style = "!", warnStyle
		case doctor.StatusFail:
			mark, style = "✗", failStyle
		}
		line := fmt.Sprintf("%s %-16s %s", style.Render(mark), check.Title, check.Detail)
		rows.WriteString(strings.TrimRight(line, " ") + "\n")
	}
	headline := okStyle.Render("Environment is ready — starting the downloader")
	if !m.report.Ready() {
		headline = warnStyle.Render("Setup needed: " + string(m.report.Code))
	}
	return panelStyle.Render(labelStyle.Render("Doctor") + "\n\n" + rows.String() + "\n" + headline)
}

func (m model) renderActions() string {
	var rows strings.Builder
	for index, item := range m.actions {
		cursor := "  "
		if index == m.cursor {
			cursor = "› "
		}
		rows.WriteString(cursor + fieldStyle.Render(item.title) + "\n")
	}
	return panelStyle.Render(labelStyle.Render("Fix") + "\n\n" + rows.String() + "\n" + hintStyle.Render("↑↓ moves · ENTER runs · R re-check · Q quits"))
}

func boundedLog(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	const maxLines = 12
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return hintStyle.Render(strings.Join(lines, "\n"))
}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
