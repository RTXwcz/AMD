// Package doctor performs the read-only environment diagnosis of the unified
// ez-am application: WSL, distribution selection, Docker, runtime files,
// credentials, images and wrapper health. It never installs, starts, repairs,
// logs in or deletes anything.
//
// The consolidated probe keeps its concurrent answers in a transient scratch
// directory inside the distribution and removes it again; nothing outside that
// scratch directory is written, and credential files are only tested for
// existence.
package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"ez-am/internal/i18n"
	"ez-am/internal/platform"
	"ez-am/internal/platform/wsl"
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
	ActionInstallWSL     = "install-wsl"
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
			Detail: i18n.T("doctor.no_runner"), Action: "",
		}}}
	}

	report.add(checkWSL(ctx, in))

	distro, distroCheck, decided := checkDistributions(ctx, in)
	report.add(distroCheck)
	if !decided {
		return report
	}
	report.Distro = distro

	probed, probeErr := probeFacts(ctx, in, distro, in.Base)
	if probeErr != nil {
		report.add(Check{
			ID: "distro", Title: "Distribution", Status: StatusFail,
			Detail: i18n.Tf("doctor.probe.failed", sanitize(probeErr.Error())),
		})
		return report
	}
	report.Base = probed.base
	report.Distribution = platform.Distribution{
		Name:         distro,
		Family:       platform.Family(wsl.ClassifyFamily(probed.osID, strings.Fields(probed.osIDLike))),
		Version:      probed.osVersion,
		User:         probed.user,
		Home:         probed.home,
		Architecture: probed.architecture,
		LegacyBase:   probed.legacy,
	}

	// Drop the inventory row: the probe replaces it with one that also carries
	// the family and the default-user check.
	if last := len(report.Checks) - 1; last >= 0 && report.Checks[last].ID == "distro" {
		report.Checks = report.Checks[:last]
	}
	for _, check := range rows(probed) {
		report.add(check)
	}
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
			ID: "wsl", Title: i18n.T("doctor.wsl.title"), Status: StatusFail, Action: ActionInstallWSL,
			Detail: i18n.Tf("doctor.wsl.unavailable", sanitize(err.Error())),
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
		detail = i18n.T("doctor.wsl.available")
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

// ---------- rendering ----------

var checkOrder = []string{"wsl", "distro", "docker", "buildx", "runtime", "config", "images", "wrapper", "downloads"}

// Text renders a plain, ANSI-free report for terminals and logs.
func (r Report) Text() string {
	var builder strings.Builder
	builder.WriteString(i18n.T("doctor.header") + "\n")
	for _, wanted := range checkOrder {
		for _, check := range r.Checks {
			if check.ID != wanted {
				continue
			}
			builder.WriteString(fmt.Sprintf("%s %s %s\n", marker(check.Status), padTitle(check.Title), check.Detail))
		}
	}
	builder.WriteString("\n" + i18n.Tf("doctor.result", string(r.Code)))
	if r.Ready() {
		builder.WriteString(i18n.T("doctor.result.ready"))
	}
	builder.WriteString("\n")
	if !r.Ready() {
		if actions := r.Actions(); len(actions) > 0 {
			builder.WriteString(i18n.Tf("doctor.result.suggested", strings.Join(actions, ", ")) + "\n")
		}
	}
	return builder.String()
}

// padTitle pads a row title to a fixed display width. Chinese titles are made
// of double-width characters, so counting runes (as %-16s does) would leave the
// detail column ragged in the Chinese report.
func padTitle(title string) string {
	const width = 16
	if current := lipgloss.Width(title); current < width {
		return title + strings.Repeat(" ", width-current)
	}
	return title
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
