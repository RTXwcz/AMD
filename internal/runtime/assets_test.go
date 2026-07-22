package runtime_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"strings"
	"testing"

	"ez-am/internal/platform"
	"ez-am/internal/runtime"
)

var expectedRuntimeAssets = []struct {
	name   string
	mode   fs.FileMode
	digest string
}{
	{name: "setup.sh", mode: 0o755, digest: "b4a0045410f466af2d27bea6289a8d4213e4a755d4048d89fae9e77d6467ed3b"},
	{name: "login.sh", mode: 0o755, digest: "325aac0eead1e840efcf8d0ceedc0e4344205785612cfb3c1f76907841e08813"},
	{name: "dl.sh", mode: 0o755, digest: "6170c3251d3060df45bb3b9cdf5a5495d3cb3d6c09e7fa0806f1a0ebcb89d8e3"},
	{name: "compose.yaml", mode: 0o644, digest: "0d890e3f433305908021e9463ac3f96c8134813eed6b74d0e8e368933c28ec53"},
}

func TestEmbeddedRuntimeReturnsAuthoritativeAssetsWithoutFilesystemLookup(t *testing.T) {
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	unrelatedWD := t.TempDir()
	t.Cleanup(func() {
		if err := os.Chdir(originalWD); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	if err := os.Chdir(unrelatedWD); err != nil {
		t.Fatalf("change to unrelated working directory: %v", err)
	}

	assets, err := runtime.EmbeddedRuntime()
	if err != nil {
		t.Fatalf("EmbeddedRuntime() error = %v", err)
	}
	if len(assets) != len(expectedRuntimeAssets) {
		t.Fatalf("EmbeddedRuntime() returned %d assets, want %d", len(assets), len(expectedRuntimeAssets))
	}
	for i, want := range expectedRuntimeAssets {
		got := assets[i]
		if got.Name != want.name || got.Mode != want.mode {
			t.Errorf("asset[%d] identity = (%q, %04o), want (%q, %04o)", i, got.Name, got.Mode, want.name, want.mode)
		}
		digest := sha256.Sum256(got.Data)
		if hex.EncodeToString(digest[:]) != want.digest || got.SHA256 != digest {
			t.Errorf("asset[%d] digest = %x (declared %x), want %s", i, digest, got.SHA256, want.digest)
		}
		source, err := os.ReadFile(filepath.Join(repoRoot, want.name))
		if err != nil {
			t.Fatalf("read authoritative %s: %v", want.name, err)
		}
		if !bytes.Equal(got.Data, source) {
			t.Errorf("embedded %s differs from the authoritative root asset", want.name)
		}
	}

	assets[0].Name = "changed"
	assets[0].Mode = 0
	assets[0].SHA256 = [sha256.Size]byte{}
	assets[0].Data[0] ^= 0xff
	again, err := runtime.EmbeddedRuntime()
	if err != nil {
		t.Fatalf("second EmbeddedRuntime() error = %v", err)
	}
	if again[0].Name != expectedRuntimeAssets[0].name || again[0].Mode != expectedRuntimeAssets[0].mode || again[0].Data[0] == assets[0].Data[0] {
		t.Fatalf("EmbeddedRuntime() returned state aliased by a previous caller")
	}
}

func TestResolveRuntimeLayoutUsesXDGDefaultsAndOverrides(t *testing.T) {
	tests := []struct {
		name string
		home string
		xdg  runtime.XDGDirs
		want runtime.RuntimeLayout
	}{
		{
			name: "defaults",
			home: "/home/alice",
			want: runtime.RuntimeLayout{
				Base: "/home/alice/.local/share/ez-am", Config: "/home/alice/.config/ez-am",
				Data: "/home/alice/.local/share/ez-am", Cache: "/home/alice/.cache/ez-am",
				State: "/home/alice/.local/state/ez-am", Downloads: "/home/alice/.local/share/ez-am/downloads",
			},
		},
		{
			name: "individual XDG overrides",
			home: "/home/alice",
			xdg:  runtime.XDGDirs{Data: "/data dir", Config: "/cfg;dir", Cache: "/cache", State: "/state"},
			want: runtime.RuntimeLayout{
				Base: "/data dir/ez-am", Config: "/cfg;dir/ez-am", Data: "/data dir/ez-am",
				Cache: "/cache/ez-am", State: "/state/ez-am", Downloads: "/data dir/ez-am/downloads",
			},
		},
		{
			name: "partial XDG override",
			home: "/home/alice",
			xdg:  runtime.XDGDirs{Cache: "/var/cache/alice"},
			want: runtime.RuntimeLayout{
				Base: "/home/alice/.local/share/ez-am", Config: "/home/alice/.config/ez-am",
				Data: "/home/alice/.local/share/ez-am", Cache: "/var/cache/alice/ez-am",
				State: "/home/alice/.local/state/ez-am", Downloads: "/home/alice/.local/share/ez-am/downloads",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := runtime.ResolveRuntimeLayout(tt.home, tt.xdg, false); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ResolveRuntimeLayout() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestResolveRuntimeLayoutPreservesLegacyAMDLayout(t *testing.T) {
	got := runtime.ResolveRuntimeLayout("/home/alice", runtime.XDGDirs{Data: "/ignored"}, true)
	want := runtime.RuntimeLayout{
		Base: "/home/alice/amd", Data: "/home/alice/amd", Config: "/home/alice/amd/config",
		Cache: "/home/alice/amd/wrapper-bin", State: "/home/alice/amd/wrapper-data", Downloads: "/home/alice/amd/downloads",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ResolveRuntimeLayout(legacy) = %#v, want %#v", got, want)
	}
}

func TestResolveRuntimeLayoutDoesNotTurnInvalidHomesIntoInstallableLayouts(t *testing.T) {
	assets := mustEmbeddedRuntime(t)
	for _, home := range []string{"", "home/alice", "/home/alice/", "/home/alice/../bob", "/home/alice\nother"} {
		t.Run(fmt.Sprintf("home_%q", home), func(t *testing.T) {
			layout := runtime.ResolveRuntimeLayout(home, runtime.XDGDirs{}, false)
			assertInstallRejected(t, "Ubuntu", layout, assets)
		})
	}
}

func TestBuildInstallRequestsUsesFixedScriptsSeparateArgumentsAndStdin(t *testing.T) {
	assets := mustEmbeddedRuntime(t)
	layout := runtime.RuntimeLayout{
		Base: "/srv/user data;printf-not-run/ez-am", Data: "/srv/user data;printf-not-run/ez-am",
		Config: "/srv/config/ez-am", Cache: "/srv/cache/ez-am", State: "/srv/state/ez-am",
		Downloads: "/srv/user data;printf-not-run/ez-am/downloads",
	}
	distro := "Team Distro; printf not-run"

	requests, err := runtime.BuildInstallRequests(distro, layout, assets)
	if err != nil {
		t.Fatalf("BuildInstallRequests() error = %v", err)
	}
	if len(requests) != len(assets)+1 {
		t.Fatalf("BuildInstallRequests() returned %d requests, want %d assets plus activation", len(requests), len(assets))
	}
	again, err := runtime.BuildInstallRequests(distro, layout, assets)
	if err != nil || !reflect.DeepEqual(requests, again) {
		t.Fatalf("BuildInstallRequests() is not deterministic: error=%v equal=%v", err, reflect.DeepEqual(requests, again))
	}

	var versionDir string
	for i, asset := range assets {
		request := requests[i]
		if request.Name != "wsl.exe" || len(request.Args) != 11 || !reflect.DeepEqual(request.Args[:5], []string{"-d", distro, "--", "sh", "-c"}) {
			t.Fatalf("request[%d] command = %q %#v, want wsl.exe -d <distro> -- sh -c ...", i, request.Name, request.Args)
		}
		if len(request.Env) != 0 || !bytes.Equal(request.Stdin, asset.Data) {
			t.Fatalf("request[%d] transport = Env %q Stdin length %d, want empty Env and asset stdin", i, request.Env, len(request.Stdin))
		}
		if request.Args[8] != asset.Name || request.Args[9] != fmt.Sprintf("%04o", asset.Mode.Perm()) || request.Args[10] != hex.EncodeToString(asset.SHA256[:]) {
			t.Fatalf("request[%d] asset argv = %#v", i, request.Args[8:])
		}
		if i == 0 {
			versionDir = request.Args[7]
		} else if request.Args[7] != versionDir {
			t.Fatalf("request[%d] version dir = %q, want %q", i, request.Args[7], versionDir)
		}
		script := request.Args[5]
		for _, dynamic := range []string{distro, layout.Base, asset.Name, request.Args[9], request.Args[10]} {
			if strings.Contains(script, dynamic) {
				t.Fatalf("request[%d] fixed shell source contains dynamic value %q", i, dynamic)
			}
		}
		for _, operation := range []string{"mktemp", "sha256sum", "chmod", "mv"} {
			if !strings.Contains(script, operation) {
				t.Errorf("request[%d] shell source lacks atomic install operation %q", i, operation)
			}
		}
	}
	if !strings.HasPrefix(versionDir, layout.Base+"/runtime/versions/") || len(strings.TrimPrefix(versionDir, layout.Base+"/runtime/versions/")) != sha256.Size*2 {
		t.Fatalf("version directory %q does not use an aggregate SHA-256", versionDir)
	}

	activation := requests[len(requests)-1]
	if activation.Name != "wsl.exe" || len(activation.Stdin) != 0 || len(activation.Env) != 0 || len(activation.Args) < 8 {
		t.Fatalf("activation request = %#v", activation)
	}
	if !strings.Contains(activation.Args[5], "ln -s") || !strings.Contains(activation.Args[5], "mv") {
		t.Fatalf("activation shell source is not an atomic symlink switch: %q", activation.Args[5])
	}
	for _, dynamic := range []string{distro, layout.Base, versionDir} {
		if strings.Contains(activation.Args[5], dynamic) {
			t.Fatalf("activation shell source contains dynamic value %q", dynamic)
		}
	}

	assets[0].Data[0] ^= 0xff
	if bytes.Equal(requests[0].Stdin, assets[0].Data) {
		t.Fatalf("request stdin aliases caller-owned asset data")
	}

	otherLayout := runtime.RuntimeLayout{
		Base: "/opt/other/ez-am", Data: "/opt/other/ez-am", Config: "/opt/cfg/ez-am",
		Cache: "/opt/cache/ez-am", State: "/opt/state/ez-am", Downloads: "/opt/other/ez-am/downloads",
	}
	otherAssets := mustEmbeddedRuntime(t)
	other, err := runtime.BuildInstallRequests("Other $(distro)", otherLayout, otherAssets)
	if err != nil {
		t.Fatalf("BuildInstallRequests(other values) error = %v", err)
	}
	for i := range requests {
		if requests[i].Args[5] != other[i].Args[5] {
			t.Fatalf("request[%d] shell source changed with dynamic inputs", i)
		}
	}

	pristine := again
	requests[0].Args[0] = "changed"
	requests[0].Stdin[0] ^= 0xff
	requests[len(requests)-1].Args[0] = "changed"
	third, err := runtime.BuildInstallRequests(distro, layout, mustEmbeddedRuntime(t))
	if err != nil || !reflect.DeepEqual(third, pristine) {
		t.Fatalf("caller mutation polluted a later install plan: error=%v", err)
	}
}

func TestBuildInstallRequestsCanonicalizesAssetOrder(t *testing.T) {
	assets := mustEmbeddedRuntime(t)
	shuffled := []runtime.RuntimeAsset{assets[3], assets[1], assets[0], assets[2]}
	layout := validLayout("/home/alice/.local/share/ez-am")

	requests, err := runtime.BuildInstallRequests("Ubuntu", layout, shuffled)
	if err != nil {
		t.Fatalf("BuildInstallRequests(shuffled) error = %v", err)
	}
	for i, want := range expectedRuntimeAssets {
		if requests[i].Args[8] != want.name {
			t.Errorf("request[%d] asset = %q, want canonical order %q", i, requests[i].Args[8], want.name)
		}
	}
	if len(strings.TrimPrefix(requests[0].Args[7], layout.Base+"/runtime/versions/")) != sha256.Size*2 {
		t.Fatalf("version directory %q does not end in an aggregate SHA-256", requests[0].Args[7])
	}
}

func TestBuildInstallRequestsRejectsCallerSelfSignedAssetContent(t *testing.T) {
	assets := mustEmbeddedRuntime(t)
	assets[0].Data = append(append([]byte(nil), assets[0].Data...), '\n')
	assets[0].SHA256 = sha256.Sum256(assets[0].Data)

	requests, err := runtime.BuildInstallRequests("Ubuntu", validLayout("/home/alice/.local/share/ez-am"), assets)
	if err == nil {
		t.Fatalf("BuildInstallRequests() accepted caller-self-signed runtime content")
	}
	if len(requests) != 0 {
		t.Fatalf("BuildInstallRequests() returned %d partial requests with error %v", len(requests), err)
	}
}

func TestBuildInstallRequestsRejectsUnsafeDistributionLayoutAndAssetsWithoutPartialPlan(t *testing.T) {
	assets := mustEmbeddedRuntime(t)
	layout := validLayout("/home/alice/.local/share/ez-am")

	for _, distro := range []string{"", "bad/name", `bad\name`, "bad\nname", "bad\x00name"} {
		t.Run(fmt.Sprintf("distro_%q", distro), func(t *testing.T) {
			assertInstallRejected(t, distro, layout, assets)
		})
	}

	layoutMutations := []struct {
		name   string
		mutate func(*runtime.RuntimeLayout)
	}{
		{name: "empty base", mutate: func(value *runtime.RuntimeLayout) { value.Base = "" }},
		{name: "relative data", mutate: func(value *runtime.RuntimeLayout) { value.Data = "relative/data" }},
		{name: "noncanonical config", mutate: func(value *runtime.RuntimeLayout) { value.Config = "/tmp/../config" }},
		{name: "trailing cache slash", mutate: func(value *runtime.RuntimeLayout) { value.Cache += "/" }},
		{name: "control in state", mutate: func(value *runtime.RuntimeLayout) { value.State += "\nother" }},
		{name: "root downloads", mutate: func(value *runtime.RuntimeLayout) { value.Downloads = "/" }},
		{name: "base data mismatch", mutate: func(value *runtime.RuntimeLayout) { value.Data = "/different/ez-am" }},
	}
	for _, tt := range layoutMutations {
		t.Run(tt.name, func(t *testing.T) {
			invalid := layout
			tt.mutate(&invalid)
			assertInstallRejected(t, "Ubuntu", invalid, assets)
		})
	}

	assetCases := []struct {
		name   string
		assets func() []runtime.RuntimeAsset
	}{
		{name: "nil", assets: func() []runtime.RuntimeAsset { return nil }},
		{name: "missing", assets: func() []runtime.RuntimeAsset { return cloneAssets(assets[:3]) }},
		{name: "duplicate", assets: func() []runtime.RuntimeAsset { return append(cloneAssets(assets), assets[0]) }},
		{name: "unknown", assets: func() []runtime.RuntimeAsset {
			result := cloneAssets(assets)
			result[0].Name = "other.sh"
			return result
		}},
		{name: "forward slash", assets: func() []runtime.RuntimeAsset {
			result := cloneAssets(assets)
			result[0].Name = "dir/setup.sh"
			return result
		}},
		{name: "backslash", assets: func() []runtime.RuntimeAsset {
			result := cloneAssets(assets)
			result[0].Name = `dir\setup.sh`
			return result
		}},
		{name: "control", assets: func() []runtime.RuntimeAsset {
			result := cloneAssets(assets)
			result[0].Name = "setup.sh\n"
			return result
		}},
		{name: "wrong script mode", assets: func() []runtime.RuntimeAsset { result := cloneAssets(assets); result[0].Mode = 0o644; return result }},
		{name: "wrong compose mode", assets: func() []runtime.RuntimeAsset { result := cloneAssets(assets); result[3].Mode = 0o755; return result }},
		{name: "special mode bits", assets: func() []runtime.RuntimeAsset {
			result := cloneAssets(assets)
			result[0].Mode |= fs.ModeSetuid
			return result
		}},
		{name: "declared digest mismatch", assets: func() []runtime.RuntimeAsset {
			result := cloneAssets(assets)
			result[0].SHA256[0] ^= 0xff
			return result
		}},
		{name: "tampered data", assets: func() []runtime.RuntimeAsset { result := cloneAssets(assets); result[0].Data[0] ^= 0xff; return result }},
	}
	for _, tt := range assetCases {
		t.Run(tt.name, func(t *testing.T) {
			assertInstallRejected(t, "Ubuntu", layout, tt.assets())
		})
	}
}

func TestInstallRequestShellWritesAtomicallyAndActivatesOnlyCompleteVersion(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("generated POSIX shell is exercised by the required WSL test run")
	}
	assets := mustEmbeddedRuntime(t)
	base := filepath.ToSlash(filepath.Join(t.TempDir(), `data dir;not-a-command\still-data`, "ez-am"))
	layout := validLayout(base)
	requests, err := runtime.BuildInstallRequests("Ubuntu;not-a-command", layout, assets)
	if err != nil {
		t.Fatalf("BuildInstallRequests() error = %v", err)
	}
	versionDir := requests[0].Args[7]
	current := filepath.Join(layout.Base, "runtime", "current")

	if output, err := runGeneratedShell(requests[len(requests)-1]); err == nil {
		t.Fatalf("activation before asset installation succeeded; output=%s", output)
	}
	if _, err := os.Lstat(current); !os.IsNotExist(err) {
		t.Fatalf("activation before a complete version created current: %v", err)
	}

	if err := os.MkdirAll(versionDir, 0o700); err != nil {
		t.Fatalf("create fixture version directory: %v", err)
	}
	target := filepath.Join(versionDir, assets[0].Name)
	oldData := []byte("previous complete bytes\n")
	if err := os.WriteFile(target, oldData, 0o600); err != nil {
		t.Fatalf("write existing target: %v", err)
	}
	bad := requests[0]
	bad.Stdin = []byte("corrupt stdin\n")
	if output, err := runGeneratedShell(bad); err == nil {
		t.Fatalf("digest-mismatched install succeeded; output=%s", output)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read preserved target: %v", err)
	}
	if !bytes.Equal(got, oldData) {
		t.Fatalf("failed install changed target to %q, want %q", got, oldData)
	}
	assertNoPartFiles(t, versionDir)

	if err := os.Remove(target); err != nil {
		t.Fatalf("remove target before symlink fixture: %v", err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, target); err != nil {
		t.Fatalf("create target symlink fixture: %v", err)
	}
	if output, err := runGeneratedShell(requests[0]); err != nil {
		t.Fatalf("replace target symlink atomically: %v\n%s", err, output)
	}
	info, err := os.Lstat(target)
	if err != nil || info.Mode()&fs.ModeSymlink != 0 {
		t.Fatalf("installed target remains a symlink: mode=%v error=%v", infoMode(info), err)
	}
	outsideEntries, err := os.ReadDir(outside)
	if err != nil || len(outsideEntries) != 0 {
		t.Fatalf("install followed target symlink: entries=%v error=%v", outsideEntries, err)
	}

	for i := range assets {
		if output, err := runGeneratedShell(requests[i]); err != nil {
			t.Fatalf("run asset request %d: %v\n%s", i, err, output)
		}
		installed, err := os.ReadFile(filepath.Join(versionDir, assets[i].Name))
		if err != nil || !bytes.Equal(installed, assets[i].Data) {
			t.Fatalf("installed asset %s = %q, error=%v", assets[i].Name, installed, err)
		}
		info, err := os.Stat(filepath.Join(versionDir, assets[i].Name))
		if err != nil || info.Mode().Perm() != assets[i].Mode.Perm() {
			t.Fatalf("installed asset %s mode = %v, error=%v, want %04o", assets[i].Name, infoMode(info), err, assets[i].Mode.Perm())
		}
		assertNoPartFiles(t, versionDir)
	}
	if output, err := runGeneratedShell(requests[len(requests)-1]); err != nil {
		t.Fatalf("run activation request: %v\n%s", err, output)
	}
	link, err := os.Readlink(current)
	if err != nil || link != versionDir {
		t.Fatalf("current symlink = %q, error=%v, want %q", link, err, versionDir)
	}
	for _, asset := range assets {
		installed, err := os.ReadFile(filepath.Join(current, asset.Name))
		if err != nil || !bytes.Equal(installed, asset.Data) {
			t.Fatalf("active asset %s = %q, error=%v", asset.Name, installed, err)
		}
	}
}

func TestInstallRequestShellRejectsSymlinkedRuntimeParents(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("generated POSIX shell is exercised by the required WSL test run")
	}
	assets := mustEmbeddedRuntime(t)
	base := filepath.Join(t.TempDir(), "ez-am")
	requests, err := runtime.BuildInstallRequests("Ubuntu", validLayout(filepath.ToSlash(base)), assets)
	if err != nil {
		t.Fatalf("BuildInstallRequests() error = %v", err)
	}

	outside := t.TempDir()
	runtimeRoot := filepath.Join(base, "runtime")
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatalf("create base: %v", err)
	}
	if err := os.Symlink(outside, runtimeRoot); err != nil {
		t.Fatalf("symlink runtime parent: %v", err)
	}
	if output, err := runGeneratedShell(requests[0]); err == nil {
		t.Fatalf("install accepted symlinked runtime parent: %s", output)
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatalf("read outside directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("install wrote through symlinked runtime parent: %v", entries)
	}
}

func mustEmbeddedRuntime(t *testing.T) []runtime.RuntimeAsset {
	t.Helper()
	assets, err := runtime.EmbeddedRuntime()
	if err != nil {
		t.Fatalf("EmbeddedRuntime() error = %v", err)
	}
	return assets
}

func validLayout(base string) runtime.RuntimeLayout {
	return runtime.RuntimeLayout{
		Base: base, Data: base, Config: base + "-config", Cache: base + "-cache",
		State: base + "-state", Downloads: base + "/downloads",
	}
}

func assertInstallRejected(t *testing.T, distro string, layout runtime.RuntimeLayout, assets []runtime.RuntimeAsset) {
	t.Helper()
	requests, err := runtime.BuildInstallRequests(distro, layout, assets)
	if err == nil {
		t.Fatalf("BuildInstallRequests(%q, %#v, assets=%d) returned no error", distro, layout, len(assets))
	}
	if len(requests) != 0 {
		t.Fatalf("BuildInstallRequests() returned %d partial requests with error %v", len(requests), err)
	}
}

func cloneAssets(assets []runtime.RuntimeAsset) []runtime.RuntimeAsset {
	result := make([]runtime.RuntimeAsset, len(assets))
	for i, asset := range assets {
		result[i] = asset
		result[i].Data = append([]byte(nil), asset.Data...)
	}
	return result
}

func runGeneratedShell(request platform.CommandRequest) ([]byte, error) {
	command := exec.Command(request.Args[3], request.Args[4:]...)
	command.Stdin = bytes.NewReader(request.Stdin)
	return command.CombinedOutput()
}

func assertNoPartFiles(t *testing.T, directory string) {
	t.Helper()
	parts, err := filepath.Glob(filepath.Join(directory, "*.part*"))
	if err != nil {
		t.Fatalf("glob temporary files: %v", err)
	}
	if len(parts) != 0 {
		t.Fatalf("temporary install files remain: %q", parts)
	}
}

func infoMode(info fs.FileInfo) fs.FileMode {
	if info == nil {
		return 0
	}
	return info.Mode().Perm()
}
