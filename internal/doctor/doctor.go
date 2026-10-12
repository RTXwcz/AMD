// Package doctor performs the read-only environment diagnosis of the unified
// ez-am application: WSL, distribution selection, Docker, runtime files,
// credentials, images and wrapper health. It never installs, starts, repairs,
// logs in or deletes anything.
package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"ez-am/internal/platform"
	"ez-am/internal/platform/wsl"
	"ez-am/internal/runtime"
)

// Code is the single stable outcome of a diagnosis.
type Code string

const (
	Ready               Code = "READY"
	NeedsDistro         Code = "NEEDS_DISTRO"
	NeedsSelection      Code = "NEEDS_SELECTION"
	UnsupportedDistro   Code = "UNSUPPORTED_DISTRO"
	DockerUnavailable   Code = "DOCKER_UNAVAILABLE"
	NeedsRuntime        Code = "NEEDS_RUNTIME"
	NeedsDeploy         Code = "NEEDS_DEPLOY"
	NeedsAuth           Code = "NEEDS_AUTH"
	AuthPending         Code = "AUTH_PENDING"
	BrokenRuntime       Code = "BROKEN_RUNTIME"
	UnsupportedPlatform Code = "UNSUPPORTED_PLATFORM"
)

// Status is the per-check severity.
type Status string

const (
	StatusOK   Status = "ok"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
)

// Action identifies a repair the caller may offer for a failing check.
const (
	ActionInstallRuntime = "install-runtime"
	ActionCollectCreds   = "collect-credentials"
	ActionLogin          = "login"
	ActionChooseDistro   = "choose-distro"
	ActionStartDocker    = "start-docker"
	ActionInstallBuildx  = "install-buildx"
)

// Check is one user-visible diagnosis row.
type Check struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status Status `json:"status"`
	Detail string `json:"detail,omitempty"`
	Action string `json:"action,omitempty"`
}

// Inputs configures one diagnosis.
type Inputs struct {
	Runner platform.CommandRunner
	Distro string // explicit distribution; empty means auto-detect
	Base   string // explicit runtime base; empty means auto (legacy or XDG)
}

// Report is the complete diagnosis.
type Report struct {
	Code         Code                  `json:"code"`
	Checks       []Check               `json:"checks"`
	Distribution platform.Distribution `json:"-"`
	Distro       string                `json:"distribution,omitempty"`
	Base         string                `json:"base,omitempty"`
}

// Ready reports whether the runtime can serve a download request.
func (r Report) Ready() bool { return r.Code == Ready }

// Failed returns the failing rows in order.
func (r Report) Failed() []Check {
	var failed []Check
	for _, check := range r.Checks {
		if check.Status == StatusFail {
			failed = append(failed, check)
		}
	}
	return failed
}

// Actions returns the de-duplicated repair actions in check order: failing rows
// first, then warnings such as a missing buildx plugin.
func (r Report) Actions() []string {
	seen := make(map[string]bool, len(r.Checks))
	var actions []string
	for _, check := range r.Checks {
		if check.Status != StatusFail || check.Action == "" || seen[check.Action] {
			continue
		}
		seen[check.Action] = true
		actions = append(actions, check.Action)
	}
	for _, check := range r.Checks {
		if check.Status != StatusWarn || check.Action == "" || seen[check.Action] {
			continue
		}
		seen[check.Action] = true
		actions = append(actions, check.Action)
	}
	return actions
}

// Run performs the diagnosis. It never returns an error: an unusable platform
// is reported through the UnsupportedPlatform code.
func Run(ctx context.Context, in Inputs) Report {
	report := diagnose(ctx, in)
	report.Code = Ready
	for _, check := range report.Checks {
		if check.Status != StatusFail {
			continue
		}
		report.Code = codeFor(check)
		return report
	}
	return report
}

func diagnose(ctx context.Context, in Inputs) Report {
	report := Report{Code: Ready}

	if in.Runner == nil {
		return Report{Code: UnsupportedPlatform, Checks: []Check{{
			ID: "wsl", Title: "WSL", Status: StatusFail,
			Detail: "no command runner was provided", Action: "",
		}}}
	}

	report.add(checkWSL(ctx, in))

	distro, distroCheck, decided := checkDistributions(ctx, in)
	report.add(distroCheck)
	if !decided {
		return report
	}
	report.Distro = distro

	distribution, probeErr := wsl.ProbeDistribution(ctx, in.Runner, distro)
	if probeErr != nil {
		report.add(Check{
			ID: "distro", Title: "Distribution", Status: StatusFail,
			Detail: sanitize(probeErr.Error()),
		})
		return report
	}
	report.Distribution = distribution

	base, legacy, baseCheck := resolveBase(ctx, in, distro, distribution)
	if baseCheck != nil {
		report.add(*baseCheck)
		return report
	}
	report.Base = base
	report.Distribution.LegacyBase = legacy

	report.add(checkDocker(ctx, in, distro))
	report.add(checkBuildx(ctx, in, distro))
	report.add(checkRuntimeFiles(ctx, in, distro, base))
	report.add(checkConfig(ctx, in, distro, base))

	layout := layoutFor(distribution.Home, legacy, base)
	report.add(checkImages(ctx, in, distro))
	report.add(checkWrapper(ctx, in, distro, base))
	report.add(checkDownloads(ctx, in, distro, layout.Downloads))
	return report
}

func (r *Report) add(check Check) {
	r.Checks = append(r.Checks, check)
}

func codeFor(check Check) Code {
	switch check.ID {
	case "wsl":
		return UnsupportedPlatform
	case "distro":
		if strings.Contains(check.Detail, "choose") {
			return NeedsSelection
		}
		if strings.Contains(check.Detail, "no user distribution") {
			return NeedsDistro
		}
		return UnsupportedDistro
	case "docker":
		return DockerUnavailable
	case "runtime":
		return NeedsRuntime
	case "config":
		return NeedsDeploy
	case "wrapper":
		if strings.Contains(check.Detail, "authentication") {
			return AuthPending
		}
		return NeedsAuth
	}
	return BrokenRuntime
}

// ---------- individual checks ----------

func checkWSL(ctx context.Context, in Inputs) Check {
	result, err := in.Runner.Run(ctx, platform.CommandRequest{Name: "wsl.exe", Args: []string{"--status"}})
	if err != nil {
		return Check{
			ID: "wsl", Title: "WSL", Status: StatusFail,
			Detail: "wsl.exe is not available on this machine: " + sanitize(err.Error()),
		}
	}
	// `wsl --status` answers in UTF-16 on Windows, so the raw bytes are noise
	// until they are decoded.
	raw := result.Stdout
	if len(raw) == 0 {
		raw = result.Stderr
	}
	detail := ""
	if decoded, decodeErr := wsl.DecodeConsoleOutput(raw); decodeErr == nil {
		detail = summarizeWSLStatus(decoded)
	}
	if detail == "" {
		detail = "wsl.exe is available"
	}
	return Check{ID: "wsl", Title: "WSL", Status: StatusOK, Detail: detail}
}

// summarizeWSLStatus turns the localized `wsl --status` banner into one short
// line: the first "label: value" pair, otherwise the first non-empty line.
func summarizeWSLStatus(text string) string {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(strings.Trim(line, "\x00"))
		if trimmed == "" {
			continue
		}
		if _, value, found := strings.Cut(trimmed, ":"); found {
			if value = strings.TrimSpace(value); value != "" {
				return sanitize(value)
			}
		}
	}
	return firstLine(text)
}

func checkDistributions(ctx context.Context, in Inputs) (string, Check, bool) {
	const id, title = "distro", "Distribution"
	result, err := in.Runner.Run(ctx, platform.CommandRequest{Name: "wsl.exe", Args: []string{"-l", "-q"}})
	if err != nil {
		return "", Check{ID: id, Title: title, Status: StatusFail, Detail: sanitize(err.Error())}, false
	}
	decoded, err := wsl.DecodeConsoleOutput(result.Stdout)
	if err != nil {
		return "", Check{ID: id, Title: title, Status: StatusFail, Detail: sanitize(err.Error())}, false
	}
	parsed, err := wsl.ParseDistributionNames([]byte(decoded))
	if err != nil {
		return "", Check{ID: id, Title: title, Status: StatusFail, Detail: sanitize(err.Error())}, false
	}
	selection := wsl.Decide(parsed)

	if requested := strings.TrimSpace(in.Distro); requested != "" {
		if containsDistribution(parsed, requested) {
			return requested, Check{ID: id, Title: title, Status: StatusOK, Detail: requested + " (explicit)"}, true
		}
		if selection.Kind == wsl.Selected {
			return selection.Distribution.Name, Check{
				ID: id, Title: title, Status: StatusWarn,
				Detail: fmt.Sprintf("requested distribution %q not found; using %q", requested, selection.Distribution.Name),
			}, true
		}
	}

	switch selection.Kind {
	case wsl.ProvisionUbuntu:
		return "", Check{
			ID: id, Title: title, Status: StatusFail,
			Detail: "no user distribution found (docker-desktop is not a user distribution)",
		}, false
	case wsl.NeedsChoice:
		names := make([]string, 0, len(selection.Choices))
		for _, item := range selection.Choices {
			names = append(names, item.Name)
		}
		return "", Check{
			ID: id, Title: title, Status: StatusFail, Action: ActionChooseDistro,
			Detail: "choose one of: " + strings.Join(names, ", "),
		}, false
	default:
		return selection.Distribution.Name, Check{
			ID: id, Title: title, Status: StatusOK, Detail: selection.Distribution.Name,
		}, true
	}
}

func containsDistribution(items []platform.Distribution, name string) bool {
	for _, item := range items {
		if strings.EqualFold(item.Name, name) {
			return true
		}
	}
	return false
}

func resolveBase(ctx context.Context, in Inputs, distro string, distribution platform.Distribution) (string, bool, *Check) {
	if explicit := strings.TrimSpace(in.Base); explicit != "" {
		return strings.TrimSuffix(explicit, "/"), false, nil
	}
	result, err := runScript(ctx, in, distro, legacyProbeScript)
	if err == nil && result.Code == 0 {
		if legacy := strings.TrimSpace(string(result.Stdout)); legacy != "" {
			return strings.TrimSuffix(legacy, "/"), true, nil
		}
	}
	if strings.TrimSpace(distribution.Home) == "" {
		check := Check{
			ID: "runtime", Title: "Runtime files", Status: StatusFail,
			Detail: "the distribution home directory could not be discovered",
		}
		return "", false, &check
	}
	layout := runtime.ResolveRuntimeLayout(distribution.Home, runtime.XDGDirs{}, false)
	return strings.TrimSuffix(layout.Base, "/"), false, nil
}

func layoutFor(home string, legacy bool, base string) runtime.RuntimeLayout {
	layout := runtime.ResolveRuntimeLayout(home, runtime.XDGDirs{}, legacy)
	if strings.TrimSpace(base) != "" {
		layout.Base = base
	}
	return layout
}

func checkDocker(ctx context.Context, in Inputs, distro string) Check {
	const id, title = "docker", "Docker"
	result, err := in.Runner.Run(ctx, platform.CommandRequest{
		Name: "wsl.exe",
		Args: []string{"-d", distro, "--exec", "docker", "info", "--format", "{{.ServerVersion}}"},
	})
	if err != nil {
		return Check{ID: id, Title: title, Status: StatusFail, Action: ActionStartDocker, Detail: sanitize(err.Error())}
	}
	if result.Code != 0 {
		detail := firstLine(string(result.Stderr))
		if detail == "" {
			detail = fmt.Sprintf("docker info exited with code %d", result.Code)
		}
		return Check{ID: id, Title: title, Status: StatusFail, Action: ActionStartDocker, Detail: detail}
	}
	version := firstLine(string(result.Stdout))
	if version == "" {
		version = "reachable"
	}
	return Check{ID: id, Title: title, Status: StatusOK, Detail: "server " + version}
}

func checkBuildx(ctx context.Context, in Inputs, distro string) Check {
	const id, title = "buildx", "BuildKit"
	result, err := in.Runner.Run(ctx, platform.CommandRequest{
		Name: "wsl.exe",
		Args: []string{"-d", distro, "--exec", "docker", "buildx", "version"},
	})
	if err != nil || result.Code != 0 {
		return Check{
			ID: id, Title: title, Status: StatusWarn, Action: ActionInstallBuildx,
			Detail: "docker buildx is missing; needed once to build the wrapper image",
		}
	}
	return Check{ID: id, Title: title, Status: StatusOK, Detail: firstLine(string(result.Stdout))}
}

func checkRuntimeFiles(ctx context.Context, in Inputs, distro, base string) Check {
	const id, title = "runtime", "Runtime files"
	result, err := runScript(ctx, in, distro, runtimeProbeScript, base)
	if err != nil {
		return Check{ID: id, Title: title, Status: StatusFail, Action: ActionInstallRuntime, Detail: sanitize(err.Error())}
	}
	output := strings.TrimSpace(string(result.Stdout))
	if strings.HasPrefix(output, "missing=") {
		if missing := strings.TrimSpace(strings.TrimPrefix(output, "missing=")); missing != "" {
			return Check{
				ID: id, Title: title, Status: StatusFail, Action: ActionInstallRuntime,
				Detail: "missing under " + base + ": " + missing,
			}
		}
	}
	if result.Code != 0 {
		detail := firstLine(output)
		if detail == "" {
			detail = firstLine(string(result.Stderr))
		}
		if detail == "" {
			detail = "the runtime directory could not be inspected"
		}
		return Check{ID: id, Title: title, Status: StatusFail, Action: ActionInstallRuntime, Detail: detail}
	}
	return Check{ID: id, Title: title, Status: StatusOK, Detail: "installed under " + base}
}

func checkConfig(ctx context.Context, in Inputs, distro, base string) Check {
	const id, title = "config", "Credentials"
	result, err := runScript(ctx, in, distro, configProbeScript, base)
	if err != nil {
		return Check{ID: id, Title: title, Status: StatusFail, Action: ActionCollectCreds, Detail: sanitize(err.Error())}
	}
	if result.Code != 0 {
		return Check{
			ID: id, Title: title, Status: StatusFail, Action: ActionCollectCreds,
			Detail: "the Apple account and media token are not configured yet",
		}
	}
	return Check{ID: id, Title: title, Status: StatusOK, Detail: "account and media token are stored"}
}

func checkImages(ctx context.Context, in Inputs, distro string) Check {
	const id, title = "images", "Images"
	result, err := in.Runner.Run(ctx, platform.CommandRequest{
		Name: "wsl.exe",
		Args: []string{"-d", distro, "--exec", "docker", "images", "--format", "{{.Repository}}:{{.Tag}}"},
	})
	if err != nil || result.Code != 0 {
		return Check{ID: id, Title: title, Status: StatusWarn, Detail: "the local image list could not be read"}
	}
	list := string(result.Stdout)
	var missing []string
	if !strings.Contains(list, "ghcr.io/zhaarey/apple-music-downloader") {
		missing = append(missing, "downloader")
	}
	if !strings.Contains(list, "local/wrapper") {
		missing = append(missing, "wrapper")
	}
	if len(missing) > 0 {
		return Check{
			ID: id, Title: title, Status: StatusWarn,
			Detail: "not pulled yet: " + strings.Join(missing, ", ") + " (setup.sh fetches them)",
		}
	}
	return Check{ID: id, Title: title, Status: StatusOK, Detail: "downloader and wrapper images are present"}
}

func checkWrapper(ctx context.Context, in Inputs, distro, base string) Check {
	const id, title = "wrapper", "Wrapper session"
	result, err := in.Runner.Run(ctx, platform.CommandRequest{
		Name: "wsl.exe",
		Args: []string{"-d", distro, "--exec", "bash", base + "/login.sh", "status"},
	})
	if err != nil {
		return Check{ID: id, Title: title, Status: StatusFail, Action: ActionLogin, Detail: sanitize(err.Error())}
	}
	output := strings.TrimSpace(string(result.Stdout) + "\n" + string(result.Stderr))
	if result.Code == 0 {
		return Check{ID: id, Title: title, Status: StatusOK, Detail: "listening on 10020/20020/30020"}
	}
	if strings.Contains(output, "amd-wrapper-auth") || strings.Contains(output, "Authentication started") {
		return Check{
			ID: id, Title: title, Status: StatusFail, Action: ActionLogin,
			Detail: "an authentication request is already waiting for a code",
		}
	}
	return Check{ID: id, Title: title, Status: StatusFail, Action: ActionLogin, Detail: wrapperDetail(output)}
}

func wrapperDetail(output string) string {
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[!]") {
			return sanitize(strings.TrimSpace(strings.TrimPrefix(trimmed, "[!]")))
		}
	}
	if detail := firstLine(output); detail != "" {
		return detail
	}
	return "the wrapper is not healthy"
}

func checkDownloads(ctx context.Context, in Inputs, distro, downloads string) Check {
	const id, title = "downloads", "Output directory"
	if strings.TrimSpace(downloads) == "" {
		return Check{ID: id, Title: title, Status: StatusWarn, Detail: "the output directory could not be resolved"}
	}
	result, err := runScript(ctx, in, distro, writableProbeScript, downloads)
	if err != nil || result.Code != 0 {
		return Check{ID: id, Title: title, Status: StatusWarn, Detail: "not writable yet: " + downloads}
	}
	return Check{ID: id, Title: title, Status: StatusOK, Detail: downloads}
}

// ---------- scripting ----------

const doctorArgv0 = "ez-am-doctor"

// runScript runs a FIXED script through bash inside the distribution. Dynamic
// values travel as separate arguments ($1, $2, …); they are never interpolated
// into the script source.
func runScript(ctx context.Context, in Inputs, distro, script string, args ...string) (platform.CommandResult, error) {
	argv := []string{"-d", distro, "--exec", "bash", "-lc", script, doctorArgv0}
	argv = append(argv, args...)
	return in.Runner.Run(ctx, platform.CommandRequest{Name: "wsl.exe", Args: argv})
}

const legacyProbeScript = `if [ -f "$HOME/amd/login.sh" ]; then printf '%s' "$HOME/amd"; else exit 1; fi`

const runtimeProbeScript = `base="$1"
missing=""
for name in dl.sh login.sh compose.yaml; do
  if [ ! -f "$base/$name" ]; then missing="$missing$name,"; fi
done
printf 'missing=%s' "${missing%,}"
if [ -n "$missing" ]; then exit 1; fi
exit 0`

const configProbeScript = `base="$1"
if [ -s "$base/.env" ] && [ -s "$base/config/config.yaml" ]; then exit 0; fi
exit 1`

const writableProbeScript = `target="$1"
if [ -d "$target" ] && [ -w "$target" ]; then exit 0; fi
exit 1`

// ---------- rendering ----------

var checkOrder = []string{"wsl", "distro", "docker", "buildx", "runtime", "config", "images", "wrapper", "downloads"}

// Text renders a plain, ANSI-free report for terminals and logs.
func (r Report) Text() string {
	var builder strings.Builder
	builder.WriteString("ez-am doctor\n")
	for _, wanted := range checkOrder {
		for _, check := range r.Checks {
			if check.ID != wanted {
				continue
			}
			builder.WriteString(fmt.Sprintf("%s %-16s %s\n", marker(check.Status), check.Title, check.Detail))
		}
	}
	builder.WriteString("\nResult: " + string(r.Code))
	if r.Ready() {
		builder.WriteString(" — ready to download")
	}
	builder.WriteString("\n")
	if !r.Ready() {
		if actions := r.Actions(); len(actions) > 0 {
			builder.WriteString("Suggested actions: " + strings.Join(actions, ", ") + "\n")
		}
	}
	return builder.String()
}

func marker(status Status) string {
	switch status {
	case StatusOK:
		return "OK  "
	case StatusWarn:
		return "WARN"
	default:
		return "FAIL"
	}
}

// JSON renders a stable machine-readable report.
func (r Report) JSON() ([]byte, error) {
	type jsonCheck struct {
		ID     string `json:"id"`
		Title  string `json:"title"`
		Status string `json:"status"`
		Detail string `json:"detail,omitempty"`
		Action string `json:"action,omitempty"`
	}
	type jsonReport struct {
		Code         string      `json:"code"`
		Ready        bool        `json:"ready"`
		Distribution string      `json:"distribution,omitempty"`
		Base         string      `json:"base,omitempty"`
		Actions      []string    `json:"actions,omitempty"`
		Checks       []jsonCheck `json:"checks"`
	}
	out := jsonReport{
		Code:         string(r.Code),
		Ready:        r.Ready(),
		Distribution: r.Distro,
		Base:         r.Base,
		Actions:      r.Actions(),
		Checks:       make([]jsonCheck, 0, len(r.Checks)),
	}
	for _, check := range r.Checks {
		out.Checks = append(out.Checks, jsonCheck{
			ID: check.ID, Title: check.Title, Status: string(check.Status),
			Detail: check.Detail, Action: check.Action,
		})
	}
	return json.MarshalIndent(out, "", "  ")
}

// ---------- text helpers ----------

// sanitize collapses arbitrary command output into one bounded line.
func sanitize(text string) string {
	var builder strings.Builder
	for _, r := range text {
		if r == '\n' || r == '\r' || r == '\t' {
			builder.WriteRune(' ')
			continue
		}
		if r < 0x20 || r == 0x7f {
			continue
		}
		builder.WriteRune(r)
	}
	collapsed := strings.Join(strings.Fields(builder.String()), " ")
	const limit = 120
	runes := []rune(collapsed)
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return collapsed
}

func firstLine(text string) string {
	trimmed := strings.TrimSpace(text)
	if index := strings.IndexAny(trimmed, "\r\n"); index >= 0 {
		trimmed = trimmed[:index]
	}
	return sanitize(trimmed)
}
