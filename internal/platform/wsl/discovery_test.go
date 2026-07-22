package wsl_test

import (
	"encoding/binary"
	"reflect"
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
