package wsl_test

import (
	"context"
	"encoding/binary"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"

	"ez-am/internal/platform"
	"ez-am/internal/platform/wsl"
)

func TestDecodeConsoleOutputAcceptsWSLConsoleEncodings(t *testing.T) {
	want := "Ubuntu\r\n\u4e2d\u6587 Distro\r\n"

	tests := []struct {
		name string
		raw  []byte
	}{
		{name: "UTF-8", raw: []byte(want)},
		{name: "UTF-16LE without BOM", raw: encodeUTF16(want, binary.LittleEndian, false)},
		{name: "UTF-16LE with BOM", raw: encodeUTF16(want, binary.LittleEndian, true)},
		{name: "UTF-16BE with BOM", raw: encodeUTF16(want, binary.BigEndian, true)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := wsl.DecodeConsoleOutput(tt.raw)
			if err != nil {
				t.Fatalf("DecodeConsoleOutput() error = %v", err)
			}
			if got != want {
				t.Fatalf("DecodeConsoleOutput() = %q, want %q", got, want)
			}
		})
	}
}

func TestDecodeConsoleOutputDetectsNULHeavyUTF16BeforeUTF8(t *testing.T) {
	raw := encodeUTF16("Ubuntu\r\nDebian\r\n", binary.LittleEndian, false)

	got, err := wsl.DecodeConsoleOutput(raw)
	if err != nil {
		t.Fatalf("DecodeConsoleOutput() error = %v", err)
	}
	if got != "Ubuntu\r\nDebian\r\n" {
		t.Fatalf("DecodeConsoleOutput() = %q", got)
	}
}

func TestDecodeConsoleOutputAcceptsUnicodeOnlyUTF16LEWithoutBOM(t *testing.T) {
	want := "\u4e2d\u6587\u53d1\u884c\u7248\r\n"
	raw := encodeUTF16(want, binary.LittleEndian, false)

	got, err := wsl.DecodeConsoleOutput(raw)
	if err != nil {
		t.Fatalf("DecodeConsoleOutput() error = %v", err)
	}
	if got != want {
		t.Fatalf("DecodeConsoleOutput() = %q, want %q", got, want)
	}
}

func TestDecodeConsoleOutputRejectsMalformedInput(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
	}{
		{name: "odd UTF-16 byte count", raw: []byte{0xff, 0xfe, 0x41}},
		{name: "unpaired high surrogate", raw: encodeUTF16Units([]uint16{0xd800}, binary.LittleEndian, true)},
		{name: "unpaired low surrogate", raw: encodeUTF16Units([]uint16{0xdc00}, binary.LittleEndian, true)},
		{name: "invalid UTF-8", raw: []byte{0xc3, 0x28}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := wsl.DecodeConsoleOutput(tt.raw); err == nil {
				t.Fatalf("DecodeConsoleOutput(%v) = %q, want error", tt.raw, got)
			}
		})
	}
}

func TestParseDistributionNamesFiltersDockerManagedEntriesAndPreservesOrder(t *testing.T) {
	raw := encodeUTF16(
		"\r\nUbuntu 24.04\r\nDOCKER-DESKTOP\r\ndocker-desktop-data\r\ndocker-desktop-dev\r\nFedora.Test\r\n\u4e2d\u6587_\u53d1\u884c-\u7248\r\n",
		binary.LittleEndian,
		false,
	)

	got, err := wsl.ParseDistributionNames(raw)
	if err != nil {
		t.Fatalf("ParseDistributionNames() error = %v", err)
	}
	want := []platform.Distribution{
		{Name: "Ubuntu 24.04"},
		{Name: "docker-desktop-dev"},
		{Name: "Fedora.Test"},
		{Name: "\u4e2d\u6587_\u53d1\u884c-\u7248"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseDistributionNames() = %#v, want %#v", got, want)
	}
}

func TestParseDistributionNamesAllowsReasonableNameCharacters(t *testing.T) {
	raw := []byte("Ubuntu 24.04\nopenSUSE_Tumbleweed\nMy-Distro\n\u6d4b\u8bd5 \u53d1\u884c\u7248\n")

	got, err := wsl.ParseDistributionNames(raw)
	if err != nil {
		t.Fatalf("ParseDistributionNames() error = %v", err)
	}
	want := []platform.Distribution{
		{Name: "Ubuntu 24.04"},
		{Name: "openSUSE_Tumbleweed"},
		{Name: "My-Distro"},
		{Name: "\u6d4b\u8bd5 \u53d1\u884c\u7248"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseDistributionNames() = %#v, want %#v", got, want)
	}
}

func TestParseDistributionNamesRejectsUnsafeNames(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
	}{
		{name: "forward slash", raw: []byte("bad/name\n")},
		{name: "backslash", raw: []byte("bad\\name\n")},
		{name: "NUL", raw: []byte("bad\x00name\n")},
		{name: "short UTF-8 name with NUL", raw: []byte{'A', 0, '\r', '\n'}},
		{name: "control character", raw: []byte("bad\tname\n")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := wsl.ParseDistributionNames(tt.raw); err == nil {
				t.Fatalf("ParseDistributionNames(%q) = %#v, want error", tt.raw, got)
			}
		})
	}
}

func TestParseDistributionNamesRejectsCaseInsensitiveDuplicates(t *testing.T) {
	if got, err := wsl.ParseDistributionNames([]byte("Ubuntu\r\nubuntu\r\n")); err == nil {
		t.Fatalf("ParseDistributionNames() = %#v, want duplicate-name error", got)
	}
}

func TestParseDistributionNamesRejectsInvalidEncoding(t *testing.T) {
	raw := encodeUTF16Units([]uint16{'U', 'b', 0xd800}, binary.LittleEndian, true)

	if got, err := wsl.ParseDistributionNames(raw); err == nil {
		t.Fatalf("ParseDistributionNames() = %#v, want encoding error", got)
	}
}

func TestSelectableDistributionsFiltersExactDockerNamesWithoutMutatingInput(t *testing.T) {
	items := []platform.Distribution{
		{Name: "Ubuntu", Version: "24.04"},
		{Name: "Docker-Desktop"},
		{Name: "docker-desktop-data"},
		{Name: "docker-desktop-dev", User: "dev"},
	}
	original := append([]platform.Distribution(nil), items...)

	got := wsl.SelectableDistributions(items)
	want := []platform.Distribution{items[0], items[3]}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SelectableDistributions() = %#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(items, original) {
		t.Fatalf("SelectableDistributions() mutated input: got %#v, want %#v", items, original)
	}
	got[0].Name = "changed"
	if items[0].Name != "Ubuntu" {
		t.Fatalf("SelectableDistributions() returned an alias of its input")
	}
}

func TestDecideProvisionsUbuntuWhenNoSelectableDistributionExists(t *testing.T) {
	for _, items := range [][]platform.Distribution{
		nil,
		{{Name: "docker-desktop"}, {Name: "DOCKER-DESKTOP-DATA"}},
	} {
		got := wsl.Decide(items)
		if got.Kind != wsl.ProvisionUbuntu {
			t.Fatalf("Decide(%#v).Kind = %v, want ProvisionUbuntu", items, got.Kind)
		}
		if got.Distribution != (platform.Distribution{}) || len(got.Choices) != 0 {
			t.Fatalf("Decide(%#v) = %#v, want no automatic selection or choices", items, got)
		}
	}
}

func TestDecideSelectsTheOnlySelectableDistribution(t *testing.T) {
	want := platform.Distribution{Name: "Ubuntu", Version: "24.04", User: "alice"}
	items := []platform.Distribution{{Name: "docker-desktop"}, want}

	got := wsl.Decide(items)
	if got.Kind != wsl.Selected {
		t.Fatalf("Decide().Kind = %v, want Selected", got.Kind)
	}
	if got.Distribution != want || len(got.Choices) != 0 {
		t.Fatalf("Decide() = %#v, want selected distribution %#v", got, want)
	}
}

func TestDecideRequiresUserChoiceForMultipleDistributionsInDiscoveryOrder(t *testing.T) {
	items := []platform.Distribution{
		{Name: "Debian", User: "deb"},
		{Name: "docker-desktop"},
		{Name: "Ubuntu", User: "ubuntu"},
		{Name: "Fedora", User: "fedora"},
	}
	wantChoices := []platform.Distribution{items[0], items[2], items[3]}

	got := wsl.Decide(items)
	if got.Kind != wsl.NeedsChoice {
		t.Fatalf("Decide().Kind = %v, want NeedsChoice", got.Kind)
	}
	if got.Distribution != (platform.Distribution{}) {
		t.Fatalf("Decide() automatically selected %#v", got.Distribution)
	}
	if !reflect.DeepEqual(got.Choices, wantChoices) {
		t.Fatalf("Decide().Choices = %#v, want %#v", got.Choices, wantChoices)
	}
	got.Choices[0].Name = "changed"
	if items[0].Name != "Debian" {
		t.Fatalf("Decide() returned choices that alias the input")
	}
}

func TestProbeDistributionUsesFixedScriptAndSeparateDistributionArgument(t *testing.T) {
	ctx := context.Background()
	name := "Team Distro; printf not-executed"
	run := &recordingRunner{result: platform.CommandResult{Stdout: machineProbeOutput(
		"1000", "alice", "/srv/users/alice", "x86_64", "debian", "", "12",
	)}}

	got, err := wsl.ProbeDistribution(ctx, run, name)
	if err != nil {
		t.Fatalf("ProbeDistribution() error = %v", err)
	}
	want := platform.Distribution{
		Name:         name,
		Family:       platform.FamilyDebian,
		Version:      "12",
		User:         "alice",
		Home:         "/srv/users/alice",
		Architecture: "x86_64",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ProbeDistribution() = %#v, want %#v", got, want)
	}
	if len(run.requests) != 1 {
		t.Fatalf("runner received %d requests, want 1", len(run.requests))
	}

	request := run.requests[0]
	if request.Name != "wsl.exe" {
		t.Fatalf("request.Name = %q, want wsl.exe", request.Name)
	}
	if len(request.Args) != 6 || request.Args[0] != "-d" || request.Args[1] != name ||
		request.Args[2] != "--" || request.Args[3] != "sh" || request.Args[4] != "-c" {
		t.Fatalf("request.Args = %#v, want wsl.exe -d <name> -- sh -c <script>", request.Args)
	}
	for i, arg := range request.Args {
		if i != 1 && arg == name {
			t.Fatalf("distribution name also appeared in request.Args[%d]", i)
		}
	}
	script := request.Args[5]
	if strings.Contains(script, name) || strings.Contains(script, "alice") || strings.Contains(script, "/srv/users/alice") {
		t.Fatalf("probe script contains a dynamic value: %q", script)
	}
	for _, fragment := range []string{
		"id -u", "id -un", "${HOME-}", "uname -m", "/etc/os-release",
		`\036uid=`, `\036user=`, `\036home=`, `\036architecture=`,
		`\036os_id=`, `\036os_id_like=`, `\036os_version_id=`,
	} {
		if !strings.Contains(script, fragment) {
			t.Errorf("probe script %q does not contain %q", script, fragment)
		}
	}
	if len(request.Env) != 0 || len(request.Stdin) != 0 {
		t.Fatalf("probe request transports unexpected dynamic data: Env=%q Stdin=%q", request.Env, request.Stdin)
	}

	second := &recordingRunner{result: run.result}
	if _, err := wsl.ProbeDistribution(ctx, second, "Another Distro"); err != nil {
		t.Fatalf("second ProbeDistribution() error = %v", err)
	}
	if second.requests[0].Args[5] != script {
		t.Fatalf("probe script changed with distribution: first %q, second %q", script, second.requests[0].Args[5])
	}
}

func TestProbeDistributionClassifiesSupportedAndUnsupportedFamilies(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		idLike  string
		version string
		want    platform.Family
	}{
		{name: "Debian", id: "debian", version: "12", want: platform.FamilyDebian},
		{name: "Ubuntu", id: "ubuntu", idLike: "debian", version: "24.04", want: platform.FamilyDebian},
		{name: "Debian derivative", id: "linuxmint", idLike: "ubuntu debian", version: "22", want: platform.FamilyDebian},
		{name: "Arch", id: "arch", want: platform.FamilyArch},
		{name: "Arch derivative", id: "endeavouros", idLike: "arch", want: platform.FamilyArch},
		{name: "unsupported", id: "fedora", idLike: "rhel", version: "42", want: platform.FamilyUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := &recordingRunner{result: platform.CommandResult{Stdout: machineProbeOutput(
				"1000", "alice", "/home/alice", "x86_64", tt.id, tt.idLike, tt.version,
			)}}

			got, err := wsl.ProbeDistribution(context.Background(), run, tt.name)
			if err != nil {
				t.Fatalf("ProbeDistribution() error = %v", err)
			}
			if got.Family != tt.want {
				t.Fatalf("ProbeDistribution().Family = %q, want %q", got.Family, tt.want)
			}
			if got.Version != tt.version || got.Architecture != "x86_64" {
				t.Fatalf("ProbeDistribution() = %#v, want version %q and architecture x86_64", got, tt.version)
			}
		})
	}
}

func TestClassifyFamilyUsesIDAndIDLikeTokens(t *testing.T) {
	tests := []struct {
		id     string
		idLike []string
		want   string
	}{
		{id: "debian", want: "debian"},
		{id: "Ubuntu", want: "debian"},
		{id: "linuxmint", idLike: []string{"ubuntu", "debian"}, want: "debian"},
		{id: "arch", want: "arch"},
		{id: "EndeavourOS", idLike: []string{"ARCH"}, want: "arch"},
		{id: "fedora", idLike: []string{"rhel"}, want: ""},
	}

	for _, tt := range tests {
		if got := wsl.ClassifyFamily(tt.id, tt.idLike); got != tt.want {
			t.Errorf("ClassifyFamily(%q, %q) = %q, want %q", tt.id, tt.idLike, got, tt.want)
		}
	}
}

func TestProbeDistributionRejectsRootDefaultUserWithActionableTypedError(t *testing.T) {
	tests := []struct {
		name string
		uid  string
		user string
		home string
	}{
		{name: "uid zero", uid: "0", user: "root", home: "/root"},
		{name: "root home", uid: "1000", user: "alice", home: "/root"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := &recordingRunner{result: platform.CommandResult{Stdout: machineProbeOutput(
				tt.uid, tt.user, tt.home, "x86_64", "ubuntu", "debian", "24.04",
			)}}

			_, err := wsl.ProbeDistribution(context.Background(), run, "Ubuntu")
			var rootErr *wsl.RootDefaultUserError
			if !errors.As(err, &rootErr) {
				t.Fatalf("ProbeDistribution() error = %T %v, want *wsl.RootDefaultUserError", err, err)
			}
			if rootErr.Distribution != "Ubuntu" || rootErr.UID != mustAtoi(t, tt.uid) ||
				rootErr.User != tt.user || rootErr.Home != tt.home || rootErr.Action == "" {
				t.Fatalf("RootDefaultUserError = %#v, want distribution/user/home/uid and action", rootErr)
			}
			if !strings.Contains(err.Error(), "non-root") {
				t.Fatalf("error %q does not tell the user to configure a non-root default user", err)
			}
		})
	}
}

func TestValidateLinuxHomeRejectsUnsafeValues(t *testing.T) {
	tests := []struct {
		name string
		home string
		uid  int
	}{
		{name: "empty", home: "", uid: 1000},
		{name: "relative", home: "home/alice", uid: 1000},
		{name: "newline", home: "/home/alice\nother", uid: 1000},
		{name: "control character", home: "/home/alice\x1eother", uid: 1000},
		{name: "root home", home: "/root", uid: 1000},
		{name: "root uid", home: "/home/root", uid: 0},
		{name: "negative uid", home: "/home/alice", uid: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := wsl.ValidateLinuxHome(tt.home, tt.uid); err == nil {
				t.Fatalf("ValidateLinuxHome(%q, %d) = nil, want error", tt.home, tt.uid)
			}
		})
	}
	for _, home := range []string{"/home/alice", "/srv/users/alice", "/"} {
		if err := wsl.ValidateLinuxHome(home, 1000); err != nil {
			t.Errorf("ValidateLinuxHome(%q, 1000) error = %v", home, err)
		}
	}
}

func TestProbeDistributionRejectsMalformedMachineFields(t *testing.T) {
	valid := []string{
		"uid=1000",
		"user=alice",
		"home=/home/alice",
		"architecture=x86_64",
		"os_id=ubuntu",
		"os_id_like=debian",
		"os_version_id=24.04",
	}
	tests := []struct {
		name   string
		fields []string
	}{
		{name: "missing uid", fields: valid[1:]},
		{name: "missing user", fields: removeProbeField(valid, "user")},
		{name: "missing home", fields: removeProbeField(valid, "home")},
		{name: "missing architecture", fields: removeProbeField(valid, "architecture")},
		{name: "missing os id", fields: removeProbeField(valid, "os_id")},
		{name: "missing os id-like record", fields: removeProbeField(valid, "os_id_like")},
		{name: "missing os version record", fields: removeProbeField(valid, "os_version_id")},
		{name: "empty user", fields: replaceProbeField(valid, "user", "")},
		{name: "empty home", fields: replaceProbeField(valid, "home", "")},
		{name: "empty architecture", fields: replaceProbeField(valid, "architecture", "")},
		{name: "empty os id", fields: replaceProbeField(valid, "os_id", "")},
		{name: "malformed uid", fields: replaceProbeField(valid, "uid", "1000x")},
		{name: "user newline", fields: replaceProbeField(valid, "user", "alice\nbob")},
		{name: "home newline", fields: replaceProbeField(valid, "home", "/home/alice\n/root")},
		{name: "architecture newline", fields: replaceProbeField(valid, "architecture", "x86_64\narm64")},
		{name: "os release newline", fields: replaceProbeField(valid, "os_id", "ubuntu\ndebian")},
		{name: "duplicate field", fields: append(append([]string(nil), valid...), "home=/tmp")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := &recordingRunner{result: platform.CommandResult{Stdout: encodeProbeFields(tt.fields)}}
			if got, err := wsl.ProbeDistribution(context.Background(), run, "Ubuntu"); err == nil {
				t.Fatalf("ProbeDistribution() = %#v, want malformed-output error", got)
			}
		})
	}
}

func TestProbeDistributionRequiresSuccessfulRunnerResult(t *testing.T) {
	tests := []struct {
		name   string
		result platform.CommandResult
		err    error
	}{
		{name: "runner error", err: errors.New("runner unavailable")},
		{name: "nonzero exit", result: platform.CommandResult{Code: 17, Stderr: []byte("localized failure")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := &recordingRunner{result: tt.result, err: tt.err}
			if got, err := wsl.ProbeDistribution(context.Background(), run, "Ubuntu"); err == nil {
				t.Fatalf("ProbeDistribution() = %#v, want runner failure", got)
			}
		})
	}
}

type recordingRunner struct {
	requests []platform.CommandRequest
	result   platform.CommandResult
	err      error
}

func (r *recordingRunner) Run(_ context.Context, request platform.CommandRequest) (platform.CommandResult, error) {
	r.requests = append(r.requests, request)
	return r.result, r.err
}

func machineProbeOutput(uid, user, home, architecture, id, idLike, version string) []byte {
	return encodeProbeFields([]string{
		"uid=" + uid,
		"user=" + user,
		"home=" + home,
		"architecture=" + architecture,
		"os_id=" + id,
		"os_id_like=" + idLike,
		"os_version_id=" + version,
	})
}

func encodeProbeFields(fields []string) []byte {
	return []byte("ignored WSL preamble\x1e" + strings.Join(fields, "\x1e"))
}

func removeProbeField(fields []string, key string) []string {
	result := make([]string, 0, len(fields)-1)
	for _, field := range fields {
		if !strings.HasPrefix(field, key+"=") {
			result = append(result, field)
		}
	}
	return result
}

func replaceProbeField(fields []string, key, value string) []string {
	result := append([]string(nil), fields...)
	for i, field := range result {
		if strings.HasPrefix(field, key+"=") {
			result[i] = key + "=" + value
		}
	}
	return result
}

func mustAtoi(t *testing.T, value string) int {
	t.Helper()
	result, err := strconv.Atoi(value)
	if err != nil {
		t.Fatalf("parse fixture uid %q: %v", value, err)
	}
	return result
}

func encodeUTF16(value string, order binary.ByteOrder, withBOM bool) []byte {
	return encodeUTF16Units(utf16.Encode([]rune(value)), order, withBOM)
}

func encodeUTF16Units(units []uint16, order binary.ByteOrder, withBOM bool) []byte {
	raw := make([]byte, 0, len(units)*2+2)
	if withBOM {
		if order == binary.LittleEndian {
			raw = append(raw, 0xff, 0xfe)
		} else {
			raw = append(raw, 0xfe, 0xff)
		}
	}
	for _, unit := range units {
		var encoded [2]byte
		order.PutUint16(encoded[:], unit)
		raw = append(raw, encoded[:]...)
	}
	return raw
}
