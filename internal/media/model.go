package media

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"ez-am/internal/i18n"
)

var (
	primary = lipgloss.Color("#22d3ee")
	green   = lipgloss.Color("#34d399")
	yellow  = lipgloss.Color("#fbbf24")
	red     = lipgloss.Color("#f87171")
	dim     = lipgloss.Color("#64748b")

	titleStyle  = lipgloss.NewStyle().Foreground(primary).Bold(true).Padding(0, 1)
	hintStyle   = lipgloss.NewStyle().Foreground(dim)
	okStyle     = lipgloss.NewStyle().Foreground(green)
	warnStyle   = lipgloss.NewStyle().Foreground(yellow)
	errStyle    = lipgloss.NewStyle().Foreground(red)
	panelStyle  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(primary).Padding(1, 2)
	inputStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#e2e8f0"))
	promptStyle = lipgloss.NewStyle().Foreground(primary)
)

// Target identifies the WSL runtime the media engine drives. The zero value
// keeps the historical defaults: the EZ_WSL_DISTRO override or "Ubuntu", and
// the legacy $HOME/amd base.
type Target struct {
	Distribution string
	Base         string
}

// target is set once by RunWithTarget before the UI starts. Command builders
// read it through wslDistro and downloadShellSource.
var target Target

func runWithTarget(t Target) int {
	target = t
	return Run()
}

type stage int

const (
	sLink stage = iota
	sMetadata
	sTracks
	sFormat
	sDownload
	sDone
)

var formats = []struct {
	name string
	arg  string
}{
	{"ALAC (source quality, up to 24/192)", "alac"},
	{"Dolby Atmos", "atmos"},
	{"AAC 256", "aac"},
}

type model struct {
	stage               stage
	url                 textinput.Model
	format              int
	dlLog               string
	errMsg              string
	failed              bool
	cancel              context.CancelFunc
	cancelled           bool
	jobName             string
	childStopped        bool
	cleanupDone         bool
	cleanupStarted      bool
	finalCleanupDone    bool
	finalCleanupStarted bool
	metadataRequestID   uint64
	appleURL            AppleMusicURL
	album               AlbumMetadata
	tracks              []trackChoice
	trackCursor         int
	jobs                []downloadJob
	jobIndex            int
	completedJobs       int
	runID               uint64
	processEvents       <-chan tea.Msg
	progressStage       string
	width               int
	height              int
}

func (m model) Init() tea.Cmd {
	return textinput.Blink
}

func initialModel() model {
	ti := textinput.New()
	ti.Placeholder = "https://music.apple.com/us/album/..."
	ti.CharLimit = 2000
	ti.Focus()
	m := model{stage: sLink, url: ti, progressStage: progressValidating}
	return m
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.url.Width = maxInt(1, msg.Width-12)
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			if m.stage == sDownload {
				m.cancelled = true
				m.progressStage = "stopping"
				if m.cancel != nil {
					m.cancel()
				}
				if m.jobName == "" {
					m.jobName = newDownloadContainerName()
				}
				if m.cleanupStarted {
					return m, nil
				}
				m.cleanupStarted = true
				return m, cancelDownloadCommandFor(m.runID, m.jobIndex, m.jobName)
			}
			return m, tea.Quit
		case "enter":
			return m.handleEnter()
		case "up", "k":
			if m.stage == sFormat {
				m.format = (m.format + len(formats) - 1) % len(formats)
				return m, nil
			}
			if m.stage == sTracks && len(m.tracks) > 0 {
				m.trackCursor = (m.trackCursor + len(m.tracks) - 1) % len(m.tracks)
				return m, nil
			}
		case "down", "j":
			if m.stage == sFormat {
				m.format = (m.format + 1) % len(formats)
				return m, nil
			}
			if m.stage == sTracks && len(m.tracks) > 0 {
				m.trackCursor = (m.trackCursor + 1) % len(m.tracks)
				return m, nil
			}
		case "left", "h":
			if m.stage == sTracks && len(m.tracks) > 0 {
				m.tracks[m.trackCursor] = cycleProfile(m.tracks[m.trackCursor], -1)
				return m, nil
			}
		case "right", "l":
			if m.stage == sTracks && len(m.tracks) > 0 {
				m.tracks[m.trackCursor] = cycleProfile(m.tracks[m.trackCursor], 1)
				return m, nil
			}
		case " ", "space":
			if m.stage == sTracks && len(m.tracks) > 0 {
				m.tracks[m.trackCursor].selected = !m.tracks[m.trackCursor].selected
				m.errMsg = ""
				return m, nil
			}
		case "a":
			if m.stage == sTracks {
				selectAll := false
				for _, choice := range m.tracks {
					if !choice.selected {
						selectAll = true
						break
					}
				}
				for i := range m.tracks {
					m.tracks[i].selected = selectAll
				}
				m.errMsg = ""
				return m, nil
			}
		case "g":
			if m.stage == sTracks && len(m.tracks) > 0 {
				profile := m.tracks[m.trackCursor].profile
				for i := range m.tracks {
					if m.tracks[i].selected && containsProfile(availableProfiles(m.tracks[i].track), profile) {
						m.tracks[i].profile = profile
					}
				}
				return m, nil
			}
		}
	case metadataResultMsg:
		if msg.requestID != m.metadataRequestID || m.stage != sMetadata {
			return m, nil
		}
		if msg.err != nil || len(msg.album.Tracks) == 0 {
			m.stage = sFormat
			if msg.err != nil {
				m.errMsg = "Catalog metadata unavailable; whole-link download remains available: " + msg.err.Error()
			} else {
				m.errMsg = "Catalog returned no tracks; whole-link download remains available"
			}
			return m, nil
		}
		m.album = msg.album
		m.tracks = make([]trackChoice, len(msg.album.Tracks))
		requestedSong := m.appleURL.SongID
		requestedFound := false
		for _, track := range msg.album.Tracks {
			if requestedSong != "" && track.ID == requestedSong {
				requestedFound = true
				break
			}
		}
		for i, track := range msg.album.Tracks {
			selected := requestedSong == "" || (requestedFound && track.ID == requestedSong)
			m.tracks[i] = trackChoice{track: track, selected: selected, profile: defaultProfile(track)}
		}
		m.trackCursor = 0
		m.errMsg = ""
		if requestedSong != "" && !requestedFound {
			m.errMsg = fmt.Sprintf("requested song %s is not in this storefront; select tracks manually", requestedSong)
		}
		m.stage = sTracks
		return m, nil
	case processLineMsg:
		if msg.runID != m.runID || msg.jobIndex != m.jobIndex || m.stage != sDownload {
			return m, nil
		}
		m.dlLog = appendRecentLog(m.dlLog, msg.line, 100, 32*1024)
		if progress := stageFromLine(msg.line); progress != "" {
			m.progressStage = progress
		}
		if m.processEvents != nil {
			return m, waitProcessEvent(m.processEvents)
		}
		return m, nil
	case processDoneMsg:
		if msg.runID != m.runID || msg.jobIndex != m.jobIndex || m.stage != sDownload {
			return m, nil
		}
		m.childStopped = true
		if m.cancel != nil {
			m.cancel()
			m.cancel = nil
		}
		if msg.err != nil {
			m.dlLog = appendRecentLog(m.dlLog, "wsl.exe download command failed: "+msg.err.Error(), 100, 32*1024)
		}
		if m.cancelled {
			if !m.finalCleanupStarted {
				m.finalCleanupStarted = true
				return m, finalCancelDownloadCommandFor(m.runID, m.jobIndex, m.jobName)
			}
			if m.finalCleanupDone {
				m.finishCancelled()
				return m, tea.Quit
			}
			return m, nil
		}
		if msg.err != nil {
			failedProfile := "current profile"
			if m.jobIndex >= 0 && m.jobIndex < len(m.jobs) {
				failedProfile = profileLabel(m.jobs[m.jobIndex].profile)
			}
			m.dlLog = appendRecentLog(m.dlLog, fmt.Sprintf("partial completion: %d/%d groups complete; %s failed", m.completedJobs, len(m.jobs), failedProfile), 100, 32*1024)
			m.stage = sDone
			m.failed = true
			return m, nil
		}
		m.completedJobs = m.jobIndex + 1
		if m.jobIndex+1 < len(m.jobs) {
			m.jobIndex++
			m.progressStage = progressValidating
			m.childStopped = false
			m.cleanupDone = false
			m.cleanupStarted = false
			m.finalCleanupDone = false
			m.finalCleanupStarted = false
			return m.beginCurrentJob()
		}
		m.progressStage = progressComplete
		m.stage = sDone
		m.failed = false
		return m, nil
	case dlTick:
		m.dlLog = appendLog(m.dlLog, msg.log)
		if msg.finished {
			if m.cancel != nil {
				m.cancel()
				m.cancel = nil
			}
			if m.cancelled {
				m.childStopped = true
				if !m.finalCleanupStarted {
					m.finalCleanupStarted = true
					return m, finalCancelDownloadCommandFor(m.runID, m.jobIndex, m.jobName)
				}
				if m.finalCleanupDone {
					m.finishCancelled()
					return m, tea.Quit
				}
				return m, nil
			}
			m.stage = sDone
			m.failed = msg.failed
		}
		return m, nil
	case cancelTick:
		if msg.runID != m.runID || msg.jobIndex != m.jobIndex {
			return m, nil
		}
		if msg.log != "" {
			m.dlLog = appendRecentLog(m.dlLog, msg.log, 100, 32*1024)
		}
		if msg.final {
			m.finalCleanupDone = true
		} else {
			m.cleanupDone = true
		}
		if msg.final && m.cancelled && m.childStopped {
			m.finishCancelled()
			return m, tea.Quit
		}
		return m, nil
	}
	if m.stage == sLink {
		var cmd tea.Cmd
		m.url, cmd = m.url.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m model) handleEnter() (tea.Model, tea.Cmd) {
	switch m.stage {
	case sLink:
		raw := strings.TrimSpace(m.url.Value())
		if raw == "" {
			m.errMsg = "paste an Apple Music URL"
			return m, nil
		}
		canonical, err := canonicalAppleMusicURL(raw)
		if err != nil {
			m.errMsg = err.Error()
			return m, nil
		}
		m.url.SetValue(canonical)
		m.errMsg = ""
		link, parseErr := ParseAppleMusicURL(canonical)
		if parseErr == nil && link.Kind == "album" {
			m.appleURL = link
			m.metadataRequestID++
			m.stage = sMetadata
			return m, fetchAlbumMetadataCmd(m.metadataRequestID, link)
		}
		if parseErr != nil && (strings.Contains(strings.ToLower(canonical), "/album/") || strings.Contains(strings.ToLower(canonical), "/song/")) {
			m.errMsg = parseErr.Error()
			return m, nil
		}
		m.stage = sFormat
		return m, nil
	case sTracks:
		jobs := buildDownloadJobs(m.tracks)
		if len(jobs) == 0 {
			m.errMsg = "select at least one track"
			return m, nil
		}
		m.errMsg = ""
		return m.startDownloads(jobs)
	case sFormat:
		return m.startDownloads([]downloadJob{{profile: formats[m.format].arg}})
	case sDone:
		return m, tea.Quit
	}
	return m, nil
}

type metadataResultMsg struct {
	requestID uint64
	album     AlbumMetadata
	err       error
}

func fetchAlbumMetadataCmd(requestID uint64, link AppleMusicURL) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		album, err := NewCatalogClient(nil).FetchAlbum(ctx, link, "")
		return metadataResultMsg{requestID: requestID, album: album, err: err}
	}
}

func validAppleMusicWebURL(raw string) bool {
	_, err := canonicalAppleMusicURL(raw)
	return err == nil
}

func canonicalAppleMusicURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.User != nil || parsed.Port() != "" {
		return "", errors.New("URL must use https://music.apple.com/ without a port")
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host != "music.apple.com" && host != "www.music.apple.com" {
		return "", errors.New("URL must use https://music.apple.com/")
	}
	if parsed.Path == "" || parsed.Path == "/" {
		return "", errors.New("Apple Music URL is missing a resource path")
	}
	parsed.Scheme = "https"
	parsed.Host = "music.apple.com"
	return parsed.String(), nil
}

func containsProfile(profiles []string, want string) bool {
	for _, profile := range profiles {
		if profile == want {
			return true
		}
	}
	return false
}

type dlTick struct {
	log      string
	finished bool
	failed   bool
}

type cancelTick struct {
	runID    uint64
	jobIndex int
	final    bool
	log      string
}

const downloadScriptShell = `exec "$HOME/amd/dl.sh" "$@"`
const cancelDownloadShell = `name="$1"; case "$name" in amd-downloader-*) docker rm -f "$name" >/dev/null 2>&1 || true ;; *) exit 2 ;; esac`
const finalCancelDownloadShell = `name="$1"; case "$name" in amd-downloader-*) for attempt in 1 2 3 4 5 6 7 8; do docker rm -f "$name" >/dev/null 2>&1 || true; sleep 0.2; done ;; *) exit 2 ;; esac`
const downloadContainerEnv = "EZ_DOWNLOAD_CONTAINER"
const downloadCommandWaitDelay = 3 * time.Second

type commandRunner func(*exec.Cmd) ([]byte, error)

func (m model) startDownload() tea.Cmd {
	return m.startDownloadWithContext(context.Background())
}

func (m model) startDownloadWithContext(ctx context.Context) tea.Cmd {
	name := m.jobName
	if !validDownloadContainerName(name) {
		name = newDownloadContainerName()
	}
	return m.startDownloadWithJobContext(ctx, name)
}

func (m model) startDownloadWithJobContext(ctx context.Context, name string) tea.Cmd {
	return func() tea.Msg {
		return m.runDownloadWithContainer(ctx, name, combinedOutput)
	}
}

func (m model) startDownloads(jobs []downloadJob) (tea.Model, tea.Cmd) {
	m.stage = sDownload
	m.jobs = append([]downloadJob(nil), jobs...)
	m.jobIndex = 0
	m.completedJobs = 0
	m.runID = uint64(time.Now().UnixNano())
	if m.runID == 0 {
		m.runID = 1
	}
	m.dlLog = ""
	m.progressStage = progressValidating
	m.cancelled = false
	m.failed = false
	m.childStopped = false
	m.cleanupDone = false
	m.cleanupStarted = false
	m.finalCleanupDone = false
	m.finalCleanupStarted = false
	return m.beginCurrentJob()
}

func (m model) beginCurrentJob() (model, tea.Cmd) {
	m.jobName = newDownloadContainerName()
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	job := m.jobs[m.jobIndex]
	cmd, err := newDownloadCommandContextWithContainerTracks(ctx, strings.TrimSpace(m.url.Value()), job.profile, job.trackIDs, m.jobName)
	if err != nil {
		m.processEvents = nil
		return m, func() tea.Msg {
			return processDoneMsg{runID: m.runID, jobIndex: m.jobIndex, err: err}
		}
	}
	events, start := startProcessEvents(cmd, m.runID, m.jobIndex)
	m.processEvents = events
	return m, start
}

func (m model) runDownload(run commandRunner) dlTick {
	return m.runDownloadContext(context.Background(), run)
}

func (m model) runDownloadContext(ctx context.Context, run commandRunner) dlTick {
	name := m.jobName
	if !validDownloadContainerName(name) {
		name = newDownloadContainerName()
	}
	return m.runDownloadWithContainer(ctx, name, run)
}

func (m model) runDownloadWithContainer(ctx context.Context, name string, run commandRunner) dlTick {
	url := strings.TrimSpace(m.url.Value())
	arg := formats[m.format].arg
	cmd, err := newDownloadCommandContextWithContainer(ctx, url, arg, name)
	if err != nil {
		return dlTick{log: err.Error(), finished: true, failed: true}
	}
	out, err := run(cmd)
	log := strings.TrimSpace(string(out))
	if err != nil {
		message := fmt.Sprintf("wsl.exe download command failed: %v", err)
		if log == "" {
			log = message
		} else {
			log += "\n" + message
		}
	}
	return dlTick{log: log, finished: true, failed: err != nil}
}

func (m *model) finishCancelled() {
	m.stage = sDone
	m.failed = true
	if strings.TrimSpace(m.dlLog) == "" {
		m.dlLog = "download cancelled"
	}
}

func newDownloadCommand(url, format string) *exec.Cmd {
	return newDownloadCommandContext(context.Background(), url, format)
}

func newDownloadCommandContext(ctx context.Context, url, format string) *exec.Cmd {
	cmd, _ := newDownloadCommandContextWithContainer(ctx, url, format, newDownloadContainerName())
	return cmd
}

func newDownloadCommandContextWithContainer(ctx context.Context, url, format, name string) (*exec.Cmd, error) {
	return newDownloadCommandContextWithContainerTracks(ctx, url, format, nil, name)
}

func newDownloadCommandContextWithContainerTracks(ctx context.Context, url, profile string, trackIDs []string, name string) (*exec.Cmd, error) {
	if !validDownloadContainerName(name) {
		return nil, fmt.Errorf("invalid downloader container name")
	}
	args := wslScriptArgs(wslDistro(), downloadShellSource(), "ez-am-downloader", url, profile)
	args = append(args, trackIDs...)
	cmd := exec.CommandContext(ctx, "wsl.exe", args...)
	cmd.WaitDelay = downloadCommandWaitDelay
	env := os.Environ()
	env = setEnvironmentValue(env, downloadContainerEnv, name)
	wslEnv := mergedWSLEnv(environmentValue(env, "WSLENV"), downloadContainerEnv)
	env = setEnvironmentValue(env, "WSLENV", wslEnv)
	cmd.Env = env
	return cmd, nil
}

func newCancelDownloadCommandContext(ctx context.Context, name string) (*exec.Cmd, error) {
	if !validDownloadContainerName(name) {
		return nil, fmt.Errorf("invalid downloader container name")
	}
	args := wslScriptArgs(wslDistro(), cancelDownloadShell, "ez-am-cancel", name)
	cmd := exec.CommandContext(ctx, "wsl.exe", args...)
	cmd.WaitDelay = downloadCommandWaitDelay
	return cmd, nil
}

func newFinalCancelDownloadCommandContext(ctx context.Context, name string) (*exec.Cmd, error) {
	if !validDownloadContainerName(name) {
		return nil, fmt.Errorf("invalid downloader container name")
	}
	args := wslScriptArgs(wslDistro(), finalCancelDownloadShell, "ez-am-final-cancel", name)
	cmd := exec.CommandContext(ctx, "wsl.exe", args...)
	cmd.WaitDelay = downloadCommandWaitDelay
	return cmd, nil
}

func cancelDownloadCommand(name string) tea.Cmd {
	return cancelDownloadCommandFor(0, 0, name)
}

func cancelDownloadCommandFor(runID uint64, jobIndex int, name string) tea.Cmd {
	return cancelDownloadCommandForPhase(runID, jobIndex, name, false)
}

func finalCancelDownloadCommandFor(runID uint64, jobIndex int, name string) tea.Cmd {
	return cancelDownloadCommandForPhase(runID, jobIndex, name, true)
}

func cancelDownloadCommandForPhase(runID uint64, jobIndex int, name string, final bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var cmd *exec.Cmd
		var err error
		if final {
			cmd, err = newFinalCancelDownloadCommandContext(ctx, name)
		} else {
			cmd, err = newCancelDownloadCommandContext(ctx, name)
		}
		if err != nil {
			return cancelTick{runID: runID, jobIndex: jobIndex, final: final, log: err.Error()}
		}
		out, err := combinedOutput(cmd)
		if err != nil {
			log := strings.TrimSpace(string(out))
			if log == "" {
				log = fmt.Sprintf("download cleanup failed: %v", err)
			} else {
				log += fmt.Sprintf("\ndownload cleanup failed: %v", err)
			}
			return cancelTick{runID: runID, jobIndex: jobIndex, final: final, log: log}
		}
		return cancelTick{runID: runID, jobIndex: jobIndex, final: final}
	}
}

func newDownloadContainerName() string {
	return fmt.Sprintf("amd-downloader-%d-%d", os.Getpid(), time.Now().UnixNano())
}

func validDownloadContainerName(name string) bool {
	const prefix = "amd-downloader-"
	if len(name) <= len(prefix) || len(name) > 96 || !strings.HasPrefix(name, prefix) {
		return false
	}
	suffix := name[len(prefix):]
	for i := 0; i < len(suffix); i++ {
		c := suffix[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' && c != '_' && c != '.' {
			return false
		}
	}
	first := suffix[0]
	return (first >= 'a' && first <= 'z') || (first >= '0' && first <= '9')
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
	conflicts := make(map[string]struct{}, len(names))
	canonical := make([]string, 0, len(names))
	for _, name := range names {
		base := strings.ToUpper(name)
		if base == "" {
			continue
		}
		if _, ok := conflicts[base]; ok {
			continue
		}
		conflicts[base] = struct{}{}
		canonical = append(canonical, base+"/u")
	}
	parts := make([]string, 0, len(canonical)+1)
	for _, part := range strings.Split(existing, ":") {
		if part == "" {
			continue
		}
		name, _, _ := strings.Cut(part, "/")
		if _, ok := conflicts[strings.ToUpper(name)]; ok {
			continue
		}
		parts = append(parts, part)
	}
	return strings.Join(append(parts, canonical...), ":")
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

func combinedOutput(cmd *exec.Cmd) ([]byte, error) {
	return cmd.CombinedOutput()
}

func wslScriptArgs(distro, shellSource, argv0 string, args ...string) []string {
	if strings.TrimSpace(distro) == "" {
		distro = "Ubuntu"
	}
	result := []string{"-d", distro, "--exec", "bash", "-lc", shellSource, argv0}
	return append(result, args...)
}

func wslDistro() string {
	if distro := strings.TrimSpace(target.Distribution); distro != "" {
		return distro
	}
	if distro := strings.TrimSpace(os.Getenv("EZ_WSL_DISTRO")); distro != "" {
		return distro
	}
	return "Ubuntu"
}

// validRuntimeBase accepts only an absolute Linux path made of characters that
// are safe to embed in a fixed shell script. Anything else falls back to the
// legacy $HOME/amd contract.
func validRuntimeBase(base string) bool {
	if len(base) < 2 || base[0] != '/' {
		return false
	}
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '/', r == '.', r == '_', r == '-':
		default:
			return false
		}
	}
	return !strings.Contains(base, "..")
}

func downloadShellSource() string {
	if base := strings.TrimSpace(target.Base); validRuntimeBase(base) {
		return `exec "` + base + `/dl.sh" "$@"`
	}
	return downloadScriptShell
}

// windowsDownloadsPath renders the Windows-visible location of the downloads
// directory for the active runtime.
func windowsDownloadsPath() string {
	base := strings.TrimSpace(target.Base)
	if !validRuntimeBase(base) {
		return fmt.Sprintf(`\\wsl$\%s\home\<user>\amd\downloads`, wslDistro())
	}
	unixPath := strings.TrimSuffix(base, "/") + "/downloads"
	return `\\wsl$\` + wslDistro() + `\` + strings.ReplaceAll(strings.TrimPrefix(unixPath, "/"), "/", `\`)
}

// unixDownloadsPath renders the Linux-side downloads directory.
func unixDownloadsPath() string {
	base := strings.TrimSpace(target.Base)
	if !validRuntimeBase(base) {
		return "$HOME/amd/downloads"
	}
	return strings.TrimSuffix(base, "/") + "/downloads"
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
		if final != nil {
			if final.failed {
				return 1
			}
		}
	}
	return 0
}

func (m model) legacyView() string {
	switch m.stage {
	case sLink:
		var b strings.Builder
		b.WriteString(titleStyle.Render("ez-am-downloader") + "\n\n")
		b.WriteString(hintStyle.Render("Paste an Apple Music link (album / song / playlist):") + "\n\n")
		b.WriteString(inputStyle.Render(m.url.View()) + "\n")
		b.WriteString(hintStyle.Render("ENTER to continue · CTRL+C to quit"))
		if m.errMsg != "" {
			b.WriteString("\n" + errStyle.Render(m.errMsg))
		}
		return panelStyle.Render(b.String())
	case sFormat:
		var b strings.Builder
		b.WriteString(titleStyle.Render("Select format") + "\n\n")
		for i, f := range formats {
			cursor := "  "
			style := hintStyle
			if i == m.format {
				cursor = "› "
				style = okStyle
			}
			b.WriteString(fmt.Sprintf("%s%s\n", cursor, style.Render(f.name)))
		}
		b.WriteString("\n" + hintStyle.Render("↑↓ to choose · ENTER to download"))
		return panelStyle.Render(b.String())
	case sDownload:
		if m.cancelled {
			return panelStyle.Render(
				titleStyle.Render("Stopping download") + "\n\n" +
					hintStyle.Render("Stopping the WSL job and waiting for the child process...") + "\n\n" +
					hintStyle.Render(m.dlLog),
			)
		}
		return panelStyle.Render(
			titleStyle.Render("Downloading…") + "\n\n" +
				okStyle.Render("● in progress") + "\n\n" +
				hintStyle.Render(m.dlLog),
		)
	case sDone:
		if m.failed {
			return panelStyle.Render(errStyle.Render("Download failed:\n" + m.dlLog))
		}
		return panelStyle.Render(
			okStyle.Render("✓ Download complete!") + "\n\n" +
				hintStyle.Render("Files in: "+windowsDownloadsPath()) + "\n\n" +
				promptStyle.Render("Press CTRL+C to exit"),
		)
	}
	return ""
}

func (m model) View() string {
	switch m.stage {
	case sLink:
		var b strings.Builder
		b.WriteString(titleStyle.Render("ez-am-downloader") + "\n\n")
		b.WriteString(hintStyle.Render(i18n.T("media.link_prompt")) + "\n\n")
		b.WriteString(inputStyle.Render(m.url.View()) + "\n")
		b.WriteString(hintStyle.Render(i18n.T("media.link_hint")))
		if m.errMsg != "" {
			b.WriteString("\n" + errStyle.Render(m.errMsg))
		}
		return panelStyle.Render(b.String())
	case sMetadata:
		return panelStyle.Render(
			titleStyle.Render(i18n.T("media.metadata_title")) + "\n\n" +
				okStyle.Render("● Apple Music Catalog") + "\n\n" +
				hintStyle.Render(i18n.T("media.metadata_detail")),
		)
	case sTracks:
		return m.trackSelectionView()
	case sFormat:
		var b strings.Builder
		b.WriteString(titleStyle.Render(i18n.T("media.format_title")) + "\n\n")
		for i, f := range formats {
			cursor := "  "
			style := hintStyle
			if i == m.format {
				cursor = "> "
				style = okStyle
			}
			b.WriteString(fmt.Sprintf("%s%s\n", cursor, style.Render(f.name)))
		}
		b.WriteString("\n" + hintStyle.Render(i18n.T("media.format_hint")))
		if m.errMsg != "" {
			b.WriteString("\n" + warnStyle.Render(m.errMsg))
		}
		return panelStyle.Render(b.String())
	case sDownload:
		return m.downloadView()
	case sDone:
		if m.failed {
			summary := ""
			if len(m.jobs) > 0 {
				failedProfile := i18n.T("media.quality_current")
				if m.jobIndex >= 0 && m.jobIndex < len(m.jobs) {
					failedProfile = profileLabel(m.jobs[m.jobIndex].profile)
				}
				summary = i18n.Tf("media.jobs_done", m.completedJobs, len(m.jobs), failedProfile)
			}
			return panelStyle.Render(
				errStyle.Render(i18n.T("media.download_failed")) + "\n\n" +
					hintStyle.Render(summary) +
					hintStyle.Render(fitLines(m.dlLog, maxInt(1, m.height-10), maxInt(1, m.width-6))) + "\n\n" +
					promptStyle.Render(i18n.T("media.exit_hint")),
			)
		}
		return panelStyle.Render(
			okStyle.Render(i18n.T("media.done")) + "\n\n" +
				hintStyle.Render(i18n.Tf("media.files_in", unixDownloadsPath())) + "\n" +
				hintStyle.Render("Windows："+windowsDownloadsPath()) + "\n\n" +
				promptStyle.Render(i18n.T("media.exit_hint")),
		)
	}
	return ""
}

func (m model) trackSelectionView() string {
	var b strings.Builder
	contentWidth := maxInt(1, m.width-6)
	selected := selectedTrackCount(m.tracks)
	title := m.album.Name
	if title == "" {
		title = i18n.T("media.tracks_title")
	}
	b.WriteString(titleStyle.Render(truncateText(title, maxInt(1, contentWidth-2))) + "\n")
	if m.album.ArtistName != "" {
		b.WriteString(hintStyle.Render(truncateText(m.album.ArtistName, contentWidth)) + "\n")
	}
	b.WriteString(i18n.Tf("media.tracks_selected", selected, len(m.tracks)))
	start, end := visibleTrackRange(m.trackCursor, len(m.tracks), m.height-14)
	for i := start; i < end; i++ {
		choice := m.tracks[i]
		cursor := "  "
		if i == m.trackCursor {
			cursor = "> "
		}
		check := "[ ]"
		if choice.selected {
			check = "[x]"
		}
		number := choice.track.TrackNumber
		if number == 0 {
			number = i + 1
		}
		prefix := fmt.Sprintf("%s%s %02d. ", cursor, check, number)
		profile := profileLabel(choice.profile)
		maxName := maxInt(1, contentWidth-lipgloss.Width(prefix)-lipgloss.Width(profile)-2)
		row := prefix + truncateText(choice.track.Name, maxName) + "  " + profile
		if i == m.trackCursor {
			b.WriteString(okStyle.Render(row) + "\n")
		} else if choice.selected {
			b.WriteString(inputStyle.Render(row) + "\n")
		} else {
			b.WriteString(hintStyle.Render(row) + "\n")
		}
	}
	if start > 0 || end < len(m.tracks) {
		b.WriteString(hintStyle.Render(i18n.Tf("media.tracks_window", start+1, end, len(m.tracks))) + "\n")
	}
	if len(m.tracks) > 0 {
		atmos := i18n.T("media.unavailable")
		if containsProfile(availableProfiles(m.tracks[m.trackCursor].track), profileAtmos) {
			atmos = i18n.T("media.available")
		}
		b.WriteString("\n" + hintStyle.Render(truncateText("Dolby Atmos: "+atmos, contentWidth)))
	}
	b.WriteString("\n" + hintStyle.Render(truncateText(i18n.T("media.tracks_hint"), contentWidth)))
	if m.errMsg != "" {
		b.WriteString("\n" + errStyle.Render(truncateText(m.errMsg, contentWidth)))
	}
	return panelStyle.Render(b.String())
}

func (m model) downloadView() string {
	if m.cancelled {
		return panelStyle.Render(
			titleStyle.Render(i18n.T("media.stopping")) + "\n\n" +
				hintStyle.Render(i18n.T("media.stopping_detail")) + "\n\n" +
				hintStyle.Render(fitLines(m.dlLog, maxInt(1, m.height-10), maxInt(1, m.width-6))),
		)
	}
	jobText := ""
	if len(m.jobs) > 0 && m.jobIndex < len(m.jobs) {
		jobText = i18n.Tf("media.group_progress", m.jobIndex+1, len(m.jobs), profileLabel(m.jobs[m.jobIndex].profile))
	}
	logText := fitLines(m.dlLog, maxInt(1, minInt(14, m.height-10)), maxInt(1, m.width-6))
	if strings.TrimSpace(logText) == "" {
		logText = i18n.T("media.awaiting_wsl")
	}
	return panelStyle.Render(
		titleStyle.Render(i18n.T("media.downloading")) + "\n\n" +
			okStyle.Render("● "+progressLabel(m.progressStage)) + "\n" +
			hintStyle.Render(jobText) + "\n\n" +
			hintStyle.Render(logText) + "\n\n" +
			promptStyle.Render(i18n.T("media.stop_hint")),
	)
}

func selectedTrackCount(tracks []trackChoice) int {
	count := 0
	for _, track := range tracks {
		if track.selected {
			count++
		}
	}
	return count
}

func profileLabel(profile string) string {
	switch profile {
	case profileALAC48:
		return "ALAC <=48 kHz"
	case profileALAC96:
		return "ALAC <=96 kHz"
	case profileALAC192, "alac", "hires":
		return "ALAC <=192 kHz"
	case profileAtmos:
		return "Dolby Atmos"
	case profileAAC:
		return "AAC 256"
	default:
		return profile
	}
}

func progressLabel(progress string) string {
	switch progress {
	case progressValidating:
		return i18n.T("media.stage.validate")
	case progressWrapper:
		return i18n.T("media.stage.wrapper")
	case progressContainer:
		return i18n.T("media.stage.container")
	case progressDownload:
		return i18n.T("media.stage.media")
	case progressFinalize:
		return i18n.T("media.stage.tag")
	case progressComplete:
		return i18n.T("media.stage.done")
	case "stopping":
		return i18n.T("media.stage.stopping")
	default:
		return i18n.T("media.stage.prepare")
	}
}

func visibleTrackRange(cursor, total, limit int) (int, int) {
	if total <= 0 {
		return 0, 0
	}
	if limit < 1 {
		limit = 1
	}
	if limit >= total {
		return 0, total
	}
	start := cursor - limit/2
	if start < 0 {
		start = 0
	}
	if start+limit > total {
		start = total - limit
	}
	return start, start + limit
}

func truncateText(value string, maxWidth int) string {
	if maxWidth < 1 {
		return ""
	}
	if lipgloss.Width(value) <= maxWidth {
		return value
	}
	if maxWidth <= 3 {
		return strings.Repeat(".", maxWidth)
	}
	var prefix strings.Builder
	for _, r := range value {
		candidate := prefix.String() + string(r)
		if lipgloss.Width(candidate)+3 > maxWidth {
			break
		}
		prefix.WriteRune(r)
	}
	return prefix.String() + "..."
}

func tailLines(value string, count int) string {
	lines := strings.Split(strings.TrimSpace(value), "\n")
	if count > 0 && len(lines) > count {
		lines = lines[len(lines)-count:]
	}
	return strings.Join(lines, "\n")
}

func fitLines(value string, count, width int) string {
	lines := strings.Split(tailLines(value, count), "\n")
	for i := range lines {
		lines[i] = truncateText(lines[i], width)
	}
	return strings.Join(lines, "\n")
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
