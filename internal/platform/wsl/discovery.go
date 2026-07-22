package wsl

import (
	"bytes"
	"encoding/binary"
	"fmt"
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
