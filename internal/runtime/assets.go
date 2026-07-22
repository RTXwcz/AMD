package runtime

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"unicode"

	"ez-am/internal/platform"
)

//go:embed assets/setup.sh
var embeddedSetup []byte

//go:embed assets/login.sh
var embeddedLogin []byte

//go:embed assets/dl.sh
var embeddedDownloader []byte

//go:embed assets/compose.yaml
var embeddedCompose []byte

// XDGDirs contains the Linux XDG base-directory overrides discovered for a
// distribution. Empty fields use the XDG defaults derived from Home.
type XDGDirs struct {
	Data   string
	Config string
	Cache  string
	State  string
}

// RuntimeLayout identifies every application-owned directory used by ez-am.
type RuntimeLayout struct {
	Base      string
	Config    string
	Data      string
	Cache     string
	State     string
	Downloads string
}

// RuntimeAsset is one embedded runtime file and its verified manifest entry.
type RuntimeAsset struct {
	Name   string
	Mode   fs.FileMode
	SHA256 [sha256.Size]byte
	Data   []byte
}

type embeddedAsset struct {
	name   string
	mode   fs.FileMode
	digest string
	data   []byte
}

var embeddedRuntimeAssets = [...]embeddedAsset{
	{
		name:   "setup.sh",
		mode:   0o755,
		digest: "fd908aee0eaed20e05ba04cf5c4cb6623912c2e62c99ebd1e6f326631e9d56ce",
		data:   embeddedSetup,
	},
	{
		name:   "login.sh",
		mode:   0o755,
		digest: "a2a0cc6ec2a2dbb7620edd4ad80e3085d72d8db86c7a15320e02149edfab5628",
		data:   embeddedLogin,
	},
	{
		name:   "dl.sh",
		mode:   0o755,
		digest: "87ff095a50d8fc19e1532bec47b845fc49517e51957d18bebbb6f9f440a5e068",
		data:   embeddedDownloader,
	},
	{
		name:   "compose.yaml",
		mode:   0o644,
		digest: "5b5e0bbe41bbbd139933f4aadfa3decb07a55b8b58fe6e6528327375895cc276",
		data:   embeddedCompose,
	},
}

const installAssetScript = `set -eu
[ "$#" -eq 4 ] || exit 64
umask 077
version_dir=$1
asset_name=$2
asset_mode=$3
asset_sha256=$4
mkdir -p -- "$version_dir"
target="$version_dir/$asset_name"
temporary=$(mktemp "$version_dir/.${asset_name}.part.XXXXXX")
trap 'rm -f -- "$temporary"' EXIT HUP INT TERM
cat >"$temporary"
printf '%s  %s\n' "$asset_sha256" "$temporary" | sha256sum -c - >/dev/null
chmod "$asset_mode" "$temporary"
mv -Tf -- "$temporary" "$target"
trap - EXIT HUP INT TERM`

const activateRuntimeScript = `set -eu
[ "$#" -eq 14 ] || exit 64
runtime_root=$1
version_dir=$2
shift 2
while [ "$#" -gt 0 ]; do
  asset_name=$1
  asset_mode=$2
  asset_sha256=$3
  shift 3
  target="$version_dir/$asset_name"
  [ -f "$target" ] && [ ! -L "$target" ] || exit 65
  printf '%s  %s\n' "$asset_sha256" "$target" | sha256sum -c - >/dev/null
  [ "$(stat -c '%a' -- "$target")" = "${asset_mode#0}" ] || exit 66
done
mkdir -p -- "$runtime_root"
temporary=$(mktemp "$runtime_root/.current.part.XXXXXX")
rm -f -- "$temporary"
trap 'rm -f -- "$temporary"' EXIT HUP INT TERM
ln -s -- "$version_dir" "$temporary"
mv -Tf -- "$temporary" "$runtime_root/current"
trap - EXIT HUP INT TERM`

// ResolveRuntimeLayout computes a Linux runtime layout without consulting the
// host environment. Invalid inputs return a zero layout for the installer to
// reject rather than cleaning an unsafe path into a different valid target.
func ResolveRuntimeLayout(home string, xdg XDGDirs, legacyValid bool) RuntimeLayout {
	if !validLayoutRoot(home) {
		return RuntimeLayout{}
	}

	if legacyValid {
		base := path.Join(home, "amd")
		return RuntimeLayout{
			Base:      base,
			Config:    path.Join(base, "config"),
			Data:      base,
			Cache:     path.Join(base, "wrapper-bin"),
			State:     path.Join(base, "wrapper-data"),
			Downloads: path.Join(base, "downloads"),
		}
	}

	dataRoot := xdg.Data
	if dataRoot == "" {
		dataRoot = path.Join(home, ".local", "share")
	}
	configRoot := xdg.Config
	if configRoot == "" {
		configRoot = path.Join(home, ".config")
	}
	cacheRoot := xdg.Cache
	if cacheRoot == "" {
		cacheRoot = path.Join(home, ".cache")
	}
	stateRoot := xdg.State
	if stateRoot == "" {
		stateRoot = path.Join(home, ".local", "state")
	}
	for _, root := range []string{dataRoot, configRoot, cacheRoot, stateRoot} {
		if !validLayoutRoot(root) {
			return RuntimeLayout{}
		}
	}

	data := path.Join(dataRoot, "ez-am")
	return RuntimeLayout{
		Base:      data,
		Config:    path.Join(configRoot, "ez-am"),
		Data:      data,
		Cache:     path.Join(cacheRoot, "ez-am"),
		State:     path.Join(stateRoot, "ez-am"),
		Downloads: path.Join(data, "downloads"),
	}
}

// EmbeddedRuntime returns freshly owned copies of the verified embedded
// runtime manifest in installation order.
func EmbeddedRuntime() ([]RuntimeAsset, error) {
	assets := make([]RuntimeAsset, 0, len(embeddedRuntimeAssets))
	for _, embedded := range embeddedRuntimeAssets {
		expectedDigest, err := parseEmbeddedDigest(embedded)
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256(embedded.data)
		if digest != expectedDigest {
			return nil, fmt.Errorf("embedded runtime asset %q failed SHA-256 verification", embedded.name)
		}
		assets = append(assets, RuntimeAsset{
			Name:   embedded.name,
			Mode:   embedded.mode,
			SHA256: digest,
			Data:   bytes.Clone(embedded.data),
		})
	}
	return assets, nil
}

// BuildInstallRequests creates a deterministic, non-executing WSL plan that
// installs every asset before atomically activating the complete version.
func BuildInstallRequests(distro string, layout RuntimeLayout, assets []RuntimeAsset) ([]platform.CommandRequest, error) {
	if err := validateDistributionName(distro); err != nil {
		return nil, err
	}
	if err := validateRuntimeLayout(layout); err != nil {
		return nil, err
	}
	ordered, err := validateAndOrderAssets(assets)
	if err != nil {
		return nil, err
	}

	manifestDigest := aggregateManifestDigest(ordered)
	runtimeRoot := path.Join(layout.Base, "runtime")
	versionDir := path.Join(runtimeRoot, "versions", hex.EncodeToString(manifestDigest[:]))
	requests := make([]platform.CommandRequest, 0, len(ordered)+1)
	for _, asset := range ordered {
		requests = append(requests, platform.CommandRequest{
			Name: "wsl.exe",
			Args: []string{
				"-d", distro, "--", "sh", "-c", installAssetScript, "ez-am-install-asset",
				versionDir, asset.Name, fmt.Sprintf("%04o", asset.Mode.Perm()), hex.EncodeToString(asset.SHA256[:]),
			},
			Stdin: bytes.Clone(asset.Data),
		})
	}

	activationArgs := []string{
		"-d", distro, "--", "sh", "-c", activateRuntimeScript, "ez-am-activate",
		runtimeRoot, versionDir,
	}
	for _, asset := range ordered {
		activationArgs = append(
			activationArgs,
			asset.Name,
			fmt.Sprintf("%04o", asset.Mode.Perm()),
			hex.EncodeToString(asset.SHA256[:]),
		)
	}
	requests = append(requests, platform.CommandRequest{Name: "wsl.exe", Args: activationArgs})
	return requests, nil
}

func validateAndOrderAssets(assets []RuntimeAsset) ([]RuntimeAsset, error) {
	if len(assets) != len(embeddedRuntimeAssets) {
		return nil, fmt.Errorf("runtime manifest has %d assets, want %d", len(assets), len(embeddedRuntimeAssets))
	}

	byName := make(map[string]RuntimeAsset, len(assets))
	for _, asset := range assets {
		if err := validateAssetName(asset.Name); err != nil {
			return nil, err
		}
		if _, duplicate := byName[asset.Name]; duplicate {
			return nil, fmt.Errorf("runtime manifest contains duplicate asset %q", asset.Name)
		}
		expected, ok := expectedAsset(asset.Name)
		if !ok {
			return nil, fmt.Errorf("runtime manifest contains unknown asset %q", asset.Name)
		}
		if asset.Mode != expected.mode {
			return nil, fmt.Errorf("runtime asset %q has mode %v, want %04o", asset.Name, asset.Mode, expected.mode)
		}
		expectedDigest, err := parseEmbeddedDigest(expected)
		if err != nil {
			return nil, err
		}
		if asset.SHA256 != expectedDigest {
			return nil, fmt.Errorf("runtime asset %q does not match the embedded manifest SHA-256", asset.Name)
		}
		digest := sha256.Sum256(asset.Data)
		if digest != asset.SHA256 {
			return nil, fmt.Errorf("runtime asset %q does not match its declared SHA-256", asset.Name)
		}
		asset.Data = bytes.Clone(asset.Data)
		byName[asset.Name] = asset
	}

	ordered := make([]RuntimeAsset, 0, len(embeddedRuntimeAssets))
	for _, expected := range embeddedRuntimeAssets {
		asset, ok := byName[expected.name]
		if !ok {
			return nil, fmt.Errorf("runtime manifest is missing asset %q", expected.name)
		}
		ordered = append(ordered, asset)
	}
	return ordered, nil
}

func expectedAsset(name string) (embeddedAsset, bool) {
	for _, expected := range embeddedRuntimeAssets {
		if name == expected.name {
			return expected, true
		}
	}
	return embeddedAsset{}, false
}

func validateAssetName(name string) error {
	if name == "" {
		return fmt.Errorf("runtime asset name is empty")
	}
	for _, char := range name {
		if char == '/' || char == '\\' || unicode.IsControl(char) {
			return fmt.Errorf("runtime asset name %q contains a prohibited character", name)
		}
	}
	if _, ok := expectedAsset(name); !ok {
		return fmt.Errorf("runtime manifest contains unknown asset %q", name)
	}
	return nil
}

func parseEmbeddedDigest(asset embeddedAsset) ([sha256.Size]byte, error) {
	raw, err := hex.DecodeString(asset.digest)
	if err != nil || len(raw) != sha256.Size {
		return [sha256.Size]byte{}, fmt.Errorf("embedded runtime asset %q has an invalid manifest SHA-256", asset.name)
	}
	var digest [sha256.Size]byte
	copy(digest[:], raw)
	return digest, nil
}

func aggregateManifestDigest(assets []RuntimeAsset) [sha256.Size]byte {
	hash := sha256.New()
	for _, asset := range assets {
		hash.Write([]byte(asset.Name))
		hash.Write([]byte{0})
		hash.Write([]byte(fmt.Sprintf("%04o", asset.Mode.Perm())))
		hash.Write([]byte{0})
		hash.Write(asset.SHA256[:])
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	return digest
}

func validateDistributionName(name string) error {
	if name == "" {
		return fmt.Errorf("invalid WSL distribution name: name is empty")
	}
	for _, char := range name {
		if char == '/' || char == '\\' || unicode.IsControl(char) {
			return fmt.Errorf("invalid WSL distribution name %q: contains a prohibited character", name)
		}
	}
	return nil
}

func validateRuntimeLayout(layout RuntimeLayout) error {
	paths := []struct {
		name  string
		value string
	}{
		{name: "base", value: layout.Base},
		{name: "config", value: layout.Config},
		{name: "data", value: layout.Data},
		{name: "cache", value: layout.Cache},
		{name: "state", value: layout.State},
		{name: "downloads", value: layout.Downloads},
	}
	for _, candidate := range paths {
		if !validLayoutRoot(candidate.value) {
			return fmt.Errorf("runtime layout %s path %q must be an absolute canonical Linux path", candidate.name, candidate.value)
		}
	}
	if layout.Base != layout.Data {
		return fmt.Errorf("runtime layout base %q does not match data %q", layout.Base, layout.Data)
	}
	if layout.Downloads != path.Join(layout.Data, "downloads") {
		return fmt.Errorf("runtime layout downloads %q is outside data directory %q", layout.Downloads, layout.Data)
	}
	return nil
}

func validLayoutRoot(value string) bool {
	return value != "" && value != "/" && path.IsAbs(value) && path.Clean(value) == value && !containsControl(value)
}

func containsControl(value string) bool {
	return strings.IndexFunc(value, unicode.IsControl) >= 0
}
