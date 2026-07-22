package wsl

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"path"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"ez-am/internal/platform"
)

// SelectionKind describes whether setup can proceed without asking the user
// to choose a distribution.
type SelectionKind uint8

const (
	ProvisionUbuntu SelectionKind = iota
	Selected
	NeedsChoice
)

// Selection is the result of applying the distribution selection policy.
type Selection struct {
	Kind         SelectionKind
	Distribution platform.Distribution
	Choices      []platform.Distribution
}

const distributionProbeScript = `if [ ! -r /etc/os-release ]; then
  exit 65
fi
. /etc/os-release
printf '\036uid=%s\036user=%s\036home=%s\036architecture=%s\036os_id=%s\036os_id_like=%s\036os_version_id=%s' \
  "$(id -u)" "$(id -un)" "${HOME-}" "$(uname -m)" "${ID-}" "${ID_LIKE-}" "${VERSION_ID-}"`

var requiredProbeFields = [...]string{
	"uid",
	"user",
	"home",
	"architecture",
	"os_id",
	"os_id_like",
	"os_version_id",
}

// RootDefaultUserError reports a distribution whose configured default user
// would make ez-am write application state as root.
type RootDefaultUserError struct {
	Distribution string
	UID          int
	User         string
	Home         string
	Action       string
}

func (err *RootDefaultUserError) Error() string {
	return fmt.Sprintf(
		"WSL distribution %q uses root as its default user (uid=%d, user=%q, home=%q); %s",
		err.Distribution,
		err.UID,
		err.User,
		err.Home,
		err.Action,
	)
}

// LinuxHomeError identifies a home path that is unsafe for user-owned runtime
// data. Reason is stable enough for diagnostics; callers should not parse Error.
type LinuxHomeError struct {
	Home   string
	UID    int
	Reason string
}

func (err *LinuxHomeError) Error() string {
	return fmt.Sprintf("invalid Linux home %q for uid %d: %s", err.Home, err.UID, err.Reason)
}

// ProbeDistribution discovers machine-readable metadata from the configured
// default user of one WSL distribution.
func ProbeDistribution(ctx context.Context, run platform.CommandRunner, name string) (platform.Distribution, error) {
	if run == nil {
		return platform.Distribution{}, fmt.Errorf("probe WSL distribution %q: command runner is nil", name)
	}
	if err := validateDistributionName(name); err != nil {
		return platform.Distribution{}, err
	}

	result, err := run.Run(ctx, platform.CommandRequest{
		Name: "wsl.exe",
		Args: []string{"-d", name, "--", "sh", "-c", distributionProbeScript},
	})
	if err != nil {
		return platform.Distribution{}, fmt.Errorf("probe WSL distribution %q: %w", name, err)
	}
	if result.Code != 0 {
		return platform.Distribution{}, fmt.Errorf(
			"probe WSL distribution %q exited with code %d; verify that the distribution starts and retry",
			name,
			result.Code,
		)
	}

	fields, err := parseProbeFields(result.Stdout)
	if err != nil {
		return platform.Distribution{}, fmt.Errorf("parse probe result for WSL distribution %q: %w", name, err)
	}
	uid, err := parseProbeUID(fields["uid"])
	if err != nil {
		return platform.Distribution{}, fmt.Errorf("parse probe result for WSL distribution %q: %w", name, err)
	}

	user := fields["user"]
	home := fields["home"]
	if uid == 0 || user == "root" || isRootHome(home) {
		return platform.Distribution{}, &RootDefaultUserError{
			Distribution: name,
			UID:          uid,
			User:         user,
			Home:         home,
			Action:       "configure a non-root default user for this distribution and retry",
		}
	}
	if err := ValidateLinuxHome(home, uid); err != nil {
		return platform.Distribution{}, fmt.Errorf("probe WSL distribution %q: %w", name, err)
	}

	idLike := strings.Fields(fields["os_id_like"])
	return platform.Distribution{
		Name:         name,
		Family:       platform.Family(ClassifyFamily(fields["os_id"], idLike)),
		Version:      fields["os_version_id"],
		User:         user,
		Home:         home,
		Architecture: fields["architecture"],
	}, nil
}

// ClassifyFamily maps os-release identity fields to the supported installer
// families. An empty result means the distribution family is unsupported.
func ClassifyFamily(id string, idLike []string) string {
	identities := make([]string, 0, len(idLike)+1)
	identities = append(identities, id)
	identities = append(identities, idLike...)
	for _, identity := range identities {
		switch strings.ToLower(strings.TrimSpace(identity)) {
		case "debian", "ubuntu":
			return string(platform.FamilyDebian)
		case "arch":
			return string(platform.FamilyArch)
		}
	}
	return string(platform.FamilyUnknown)
}

// ValidateLinuxHome rejects paths that cannot safely be used as a non-root
// user's application-data base.
func ValidateLinuxHome(home string, uid int) error {
	switch {
	case uid < 0:
		return &LinuxHomeError{Home: home, UID: uid, Reason: "uid must not be negative"}
	case uid == 0:
		return &LinuxHomeError{Home: home, UID: uid, Reason: "root is not an allowed default user"}
	case home == "":
		return &LinuxHomeError{Home: home, UID: uid, Reason: "home is missing"}
	case containsControl(home):
		return &LinuxHomeError{Home: home, UID: uid, Reason: "home contains a control character"}
	case !path.IsAbs(home):
		return &LinuxHomeError{Home: home, UID: uid, Reason: "home must be an absolute Linux path"}
	case path.Clean(home) != home:
		return &LinuxHomeError{Home: home, UID: uid, Reason: "home must be a canonical Linux path"}
	case home == "/":
		return &LinuxHomeError{Home: home, UID: uid, Reason: "the filesystem root is not an allowed home"}
	case isRootHome(home):
		return &LinuxHomeError{Home: home, UID: uid, Reason: "root's home tree is not an allowed target"}
	default:
		return nil
	}
}

func parseProbeFields(raw []byte) (map[string]string, error) {
	separator := bytes.IndexByte(raw, 0x1e)
	if separator < 0 {
		return nil, fmt.Errorf("probe output is missing its record separator")
	}
	payload := raw[separator+1:]
	if !utf8.Valid(payload) {
		return nil, fmt.Errorf("probe payload is not valid UTF-8")
	}

	fields := make(map[string]string, len(requiredProbeFields))
	for _, record := range strings.Split(string(payload), "\x1e") {
		key, value, ok := strings.Cut(record, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("probe output contains a malformed record")
		}
		if !isProbeField(key) {
			return nil, fmt.Errorf("probe output contains unknown field %q", key)
		}
		if _, exists := fields[key]; exists {
			return nil, fmt.Errorf("probe output contains duplicate field %q", key)
		}
		if containsControl(value) {
			return nil, fmt.Errorf("probe field %q contains a control character", key)
		}
		fields[key] = value
	}

	for _, key := range requiredProbeFields {
		if _, ok := fields[key]; !ok {
			return nil, fmt.Errorf("probe output is missing field %q", key)
		}
	}
	for _, key := range []string{"uid", "user", "home", "architecture", "os_id"} {
		if fields[key] == "" {
			return nil, fmt.Errorf("probe output field %q is empty", key)
		}
	}
	return fields, nil
}

func isProbeField(key string) bool {
	for _, expected := range requiredProbeFields {
		if key == expected {
			return true
		}
	}
	return false
}

func parseProbeUID(value string) (int, error) {
	for _, char := range value {
		if char < '0' || char > '9' {
			return 0, fmt.Errorf("probe field %q is not a decimal uid", "uid")
		}
	}
	uid, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("probe field %q is not a valid uid: %w", "uid", err)
	}
	return uid, nil
}

func containsControl(value string) bool {
	for _, char := range value {
		if unicode.IsControl(char) {
			return true
		}
	}
	return false
}

func isRootHome(home string) bool {
	clean := path.Clean(home)
	return clean == "/root" || strings.HasPrefix(clean, "/root/")
}

// DecodeConsoleOutput decodes the encodings emitted by Windows console tools.
func DecodeConsoleOutput(raw []byte) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}

	switch {
	case hasPrefix(raw, 0xff, 0xfe):
		return decodeUTF16(raw[2:], binary.LittleEndian)
	case hasPrefix(raw, 0xfe, 0xff):
		return decodeUTF16(raw[2:], binary.BigEndian)
	}

	if order, ok := likelyUTF16(raw); ok {
		return decodeUTF16(raw, order)
	}
	if !utf8.Valid(raw) {
		return "", fmt.Errorf("console output is neither valid UTF-8 nor recognizable UTF-16")
	}
	return string(raw), nil
}

// ParseDistributionNames decodes a quiet WSL distribution list and validates
// each name without depending on localized status or version text.
func ParseDistributionNames(raw []byte) ([]platform.Distribution, error) {
	decoded, err := DecodeConsoleOutput(raw)
	if err != nil {
		return nil, fmt.Errorf("decode WSL distribution list: %w", err)
	}

	var items []platform.Distribution
	var seen []string
	for _, line := range strings.Split(decoded, "\n") {
		line = strings.TrimSuffix(line, "\r")
		name := strings.Trim(line, " ")
		if name == "" {
			continue
		}
		if err := validateDistributionName(name); err != nil {
			return nil, err
		}
		if isDockerManagedName(name) {
			continue
		}
		for _, previous := range seen {
			if strings.EqualFold(previous, name) {
				return nil, fmt.Errorf("duplicate WSL distribution name %q", name)
			}
		}
		seen = append(seen, name)
		items = append(items, platform.Distribution{Name: name})
	}
	return items, nil
}

// SelectableDistributions removes WSL's Docker-managed utility distributions
// while preserving discovery order and ownership of the input slice.
func SelectableDistributions(items []platform.Distribution) []platform.Distribution {
	var selectable []platform.Distribution
	for _, item := range items {
		if isDockerManagedName(item.Name) {
			continue
		}
		selectable = append(selectable, item)
	}
	return selectable
}

// Decide applies the zero, one, or many distribution selection policy.
func Decide(items []platform.Distribution) Selection {
	selectable := SelectableDistributions(items)
	switch len(selectable) {
	case 0:
		return Selection{Kind: ProvisionUbuntu}
	case 1:
		return Selection{Kind: Selected, Distribution: selectable[0]}
	default:
		return Selection{Kind: NeedsChoice, Choices: selectable}
	}
}

func hasPrefix(raw []byte, first, second byte) bool {
	return len(raw) >= 2 && raw[0] == first && raw[1] == second
}

func likelyUTF16(raw []byte) (binary.ByteOrder, bool) {
	if len(raw) < 2 {
		return nil, false
	}

	var evenZeros, oddZeros, littleEndianBreaks, bigEndianBreaks int
	for i, value := range raw {
		if value != 0 {
			continue
		}
		if i%2 == 0 {
			evenZeros++
		} else {
			oddZeros++
		}
	}
	for i := 0; i+1 < len(raw); i += 2 {
		switch {
		case (raw[i] == '\r' || raw[i] == '\n') && raw[i+1] == 0:
			littleEndianBreaks++
		case raw[i] == 0 && (raw[i+1] == '\r' || raw[i+1] == '\n'):
			bigEndianBreaks++
		}
	}
	if littleEndianBreaks > bigEndianBreaks {
		return binary.LittleEndian, true
	}
	if bigEndianBreaks > littleEndianBreaks {
		return binary.BigEndian, true
	}
	if bytes.IndexByte(raw, '\n') >= 0 {
		return nil, false
	}

	evenSlots := (len(raw) + 1) / 2
	oddSlots := len(raw) / 2
	if oddSlots > 0 && oddZeros*2 >= oddSlots && oddZeros > evenZeros*2 {
		return binary.LittleEndian, true
	}
	if evenSlots > 0 && evenZeros*2 >= evenSlots && evenZeros > oddZeros*2 {
		return binary.BigEndian, true
	}
	return nil, false
}

func decodeUTF16(raw []byte, order binary.ByteOrder) (string, error) {
	if len(raw)%2 != 0 {
		return "", fmt.Errorf("UTF-16 console output has odd byte count %d", len(raw))
	}

	units := make([]uint16, len(raw)/2)
	for i := range units {
		units[i] = order.Uint16(raw[i*2 : i*2+2])
	}
	for i := 0; i < len(units); i++ {
		switch unit := units[i]; {
		case 0xd800 <= unit && unit <= 0xdbff:
			if i+1 >= len(units) || units[i+1] < 0xdc00 || units[i+1] > 0xdfff {
				return "", fmt.Errorf("UTF-16 console output has unpaired high surrogate at code unit %d", i)
			}
			i++
		case 0xdc00 <= unit && unit <= 0xdfff:
			return "", fmt.Errorf("UTF-16 console output has unpaired low surrogate at code unit %d", i)
		}
	}
	return string(utf16.Decode(units)), nil
}

func validateDistributionName(name string) error {
	if name == "" {
		return fmt.Errorf("invalid WSL distribution name: name is empty")
	}
	for _, char := range name {
		if char == 0 || char == '/' || char == '\\' || unicode.IsControl(char) {
			return fmt.Errorf("invalid WSL distribution name %q: contains a prohibited character", name)
		}
	}
	return nil
}

func isDockerManagedName(name string) bool {
	return strings.EqualFold(name, "docker-desktop") || strings.EqualFold(name, "docker-desktop-data")
}
